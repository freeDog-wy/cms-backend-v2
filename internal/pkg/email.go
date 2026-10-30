package pkg

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sync/semaphore"
)

// EmailMessage 每封邮件只包含一个收件人。MessageID 不包含尖括号；调用方可在
// Outbox Payload 中保存稳定的 ID（例如 UUID@站点域名），使重试沿用相同标识。
// Message-ID 仅供追踪，不能保证 SMTP 服务端去重。
type EmailMessage struct {
	MessageID string
	To        string
	Subject   string
	Text      string
	HTML      string
}

type EmailSender interface {
	// Send 成功表示 SMTP 服务器接受邮件，不代表最终送达。
	Send(ctx context.Context, msg EmailMessage) error
}

type EmailTLSMode string

const (
	EmailTLS      EmailTLSMode = "tls"
	EmailSTARTTLS EmailTLSMode = "starttls"
)

type SMTPEmailConfig struct {
	Host           string        `env:"HOST"`
	Port           int           `env:"PORT" envDefault:"0"`       // 0 使用 TLS 的 465 或 STARTTLS 的 587。
	TLSMode        EmailTLSMode  `env:"TLS_MODE" envDefault:"tls"` // 必须显式选择；不允许明文回退。
	Username       string        `env:"USERNAME"`
	Password       string        `env:"PASSWORD"`
	From           string        `env:"FROM"`                             // 支持“站点名称 <sender@example.com>”。
	MaxConcurrency int           `env:"MAX_CONCURRENCY" envDefault:"3"`   // 必须大于 0，同一 Sender 的全部调用共享额度。
	ConnectTimeout time.Duration `env:"CONNECT_TIMEOUT" envDefault:"10s"` // 0 使用 10 秒。
	SendTimeout    time.Duration `env:"SEND_TIMEOUT" envDefault:"20s"`    // 0 使用 20 秒，包含等待额度、连接、握手和投递。
}

// EmailError 保留底层错误，供任务层通过 errors.As 判断是否允许重试。
// 不确定 SMTP 是否接受邮件的网络故障允许重试，但可能造成重复投递。
type EmailError struct {
	Op        string
	Retryable bool
	Err       error
}

func (e *EmailError) Error() string { return fmt.Sprintf("email %s: %v", e.Op, e.Err) }
func (e *EmailError) Unwrap() error { return e.Err }

func emailError(op string, err error) error {
	var reply *textproto.Error
	var network net.Error
	var cert *tls.CertificateVerificationError
	retry := false
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		retry = true
	case errors.As(err, &cert):
		// 证书和配置错误需要人工修正。
	case errors.As(err, &reply):
		retry = reply.Code >= 400 && reply.Code < 500
	case errors.As(err, &network), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		retry = true
	}
	return &EmailError{Op: op, Retryable: retry, Err: err}
}

// SMTPEmailSender 构造后可并发使用，每次发送创建独立连接。
// 每个 SMTP 账号应共享一个实例；不同实例或进程的并发额度相互独立。
type SMTPEmailSender struct {
	cfg         SMTPEmailConfig
	from        mail.Address
	tlsConfig   *tls.Config
	concurrency *semaphore.Weighted
}

var _ EmailSender = (*SMTPEmailSender)(nil)

func NewSMTPEmailSender(cfg SMTPEmailConfig) (*SMTPEmailSender, error) {
	if cfg.MaxConcurrency <= 0 {
		return nil, emailError("config", errors.New("SMTP max concurrency must be positive"))
	}
	if cfg.Host == "" || strings.ContainsAny(cfg.Host, " /\\[]") || hasEmailControl(cfg.Host) ||
		(strings.Contains(cfg.Host, ":") && net.ParseIP(cfg.Host) == nil) {
		return nil, emailError("config", errors.New("invalid SMTP host"))
	}
	if cfg.TLSMode != EmailTLS && cfg.TLSMode != EmailSTARTTLS {
		return nil, emailError("config", errors.New("TLS mode must be tls or starttls"))
	}
	if cfg.Port == 0 {
		cfg.Port = 465
		if cfg.TLSMode == EmailSTARTTLS {
			cfg.Port = 587
		}
	}
	if cfg.Port < 1 || cfg.Port > 65535 || cfg.ConnectTimeout < 0 || cfg.SendTimeout < 0 {
		return nil, emailError("config", errors.New("invalid SMTP port or timeout"))
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.SendTimeout == 0 {
		cfg.SendTimeout = 20 * time.Second
	}
	if (cfg.Username == "") != (cfg.Password == "") || strings.ContainsRune(cfg.Username, 0) || strings.ContainsRune(cfg.Password, 0) {
		return nil, emailError("config", errors.New("SMTP username and password must both be set or both be empty"))
	}
	from, err := parseEmailAddress(cfg.From)
	if err != nil {
		return nil, emailError("config", fmt.Errorf("invalid from address: %w", err))
	}
	return &SMTPEmailSender{
		cfg: cfg, from: *from,
		tlsConfig:   &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12},
		concurrency: semaphore.NewWeighted(int64(cfg.MaxConcurrency)),
	}, nil
}

func (s *SMTPEmailSender) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.concurrency.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	release := func() { s.concurrency.Release(1) }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (s *SMTPEmailSender) Send(ctx context.Context, msg EmailMessage) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.SendTimeout)
	defer cancel()
	release, err := s.acquire(ctx)
	if err != nil {
		return emailError("acquire", err)
	}
	// 比连接清理更早注册 defer，确保连接关闭后才归还额度。
	defer release()
	to, data, err := buildEmailMessage(s.from, msg)
	if err != nil {
		return emailError("message", err)
	}
	fail := func(op string, err error) error {
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				err = context.DeadlineExceeded
			}
		}
		return emailError(op, err)
	}
	conn, err := (&net.Dialer{Timeout: s.cfg.ConnectTimeout}).DialContext(ctx, "tcp", net.JoinHostPort(s.cfg.Host, fmt.Sprint(s.cfg.Port)))
	if err != nil {
		return fail("connect", err)
	}
	defer conn.Close()
	// 关闭连接可以中断 smtp.NewClient、STARTTLS 及全部 SMTP 读写。
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return fail("deadline", err)
	}
	var transport net.Conn = conn
	if s.cfg.TLSMode == EmailTLS {
		secure := tls.Client(conn, s.tlsConfig.Clone())
		if err := secure.HandshakeContext(ctx); err != nil {
			return fail("tls", err)
		}
		transport = secure
	}
	client, err := smtp.NewClient(transport, s.cfg.Host)
	if err != nil {
		return fail("greeting", err)
	}
	defer client.Close()
	if s.cfg.TLSMode == EmailSTARTTLS {
		if supported, _ := client.Extension("STARTTLS"); !supported {
			return fail("starttls", errors.New("SMTP server does not advertise STARTTLS"))
		}
		if err := client.StartTLS(s.tlsConfig.Clone()); err != nil {
			return fail("starttls", err)
		}
	}
	if s.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fail("auth", err)
		}
	}
	if err := client.Mail(s.from.Address); err != nil {
		return fail("from", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fail("recipient", err)
	}
	w, err := client.Data()
	if err != nil {
		return fail("data", err)
	}
	if _, err := w.Write(data); err != nil {
		return fail("write", err)
	}
	if err := w.Close(); err != nil {
		return fail("accept", err)
	}
	// DATA 已被接受，QUIT 失败不应导致重发。
	_ = client.Quit()
	return nil
}

func hasEmailControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

func parseEmailAddress(s string) (*mail.Address, error) {
	if !utf8.ValidString(s) || hasEmailControl(s) {
		return nil, errors.New("invalid mailbox")
	}
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return nil, errors.New("invalid mailbox")
	}
	// 当前 SMTP 实现未协商 SMTPUTF8，信封地址限 ASCII，显示名称可以为中文。
	if strings.ContainsFunc(addr.Address, func(r rune) bool { return r > 127 }) {
		return nil, errors.New("SMTPUTF8 mailboxes are not supported")
	}
	return addr, nil
}

func buildEmailMessage(from mail.Address, msg EmailMessage) (*mail.Address, []byte, error) {
	to, err := parseEmailAddress(msg.To)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(msg.Subject) == "" || !utf8.ValidString(msg.Subject) || hasEmailControl(msg.Subject) ||
		(msg.Text == "" && msg.HTML == "") || !utf8.ValidString(msg.Text) || !utf8.ValidString(msg.HTML) {
		return nil, nil, errors.New("subject and UTF-8 body are required; subject cannot contain control characters")
	}
	id := msg.MessageID
	if id == "" {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, nil, err
		}
		domain := from.Address[strings.LastIndexByte(from.Address, '@')+1:]
		if !validEmailMessageID("id@" + domain) {
			domain = "localhost"
		}
		id = hex.EncodeToString(raw[:]) + "@" + domain
	}
	if !validEmailMessageID(id) {
		return nil, nil, errors.New("invalid Message-ID; expected id@domain without angle brackets")
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s>\r\nMIME-Version: 1.0\r\n",
		from.String(), to.String(), mime.QEncoding.Encode("UTF-8", msg.Subject), time.Now().Format(time.RFC1123Z), id)
	if msg.Text != "" && msg.HTML != "" {
		parts := multipart.NewWriter(&buf)
		fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", parts.Boundary())
		for _, body := range []struct{ kind, content string }{{"text/plain", msg.Text}, {"text/html", msg.HTML}} {
			part, err := parts.CreatePart(textproto.MIMEHeader{
				"Content-Type":              {body.kind + "; charset=UTF-8"},
				"Content-Transfer-Encoding": {"quoted-printable"},
			})
			if err != nil {
				return nil, nil, err
			}
			if err := writeEmailBody(part, body.content); err != nil {
				return nil, nil, err
			}
		}
		if err := parts.Close(); err != nil {
			return nil, nil, err
		}
	} else {
		kind, body := "text/plain", msg.Text
		if msg.HTML != "" {
			kind, body = "text/html", msg.HTML
		}
		fmt.Fprintf(&buf, "Content-Type: %s; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", kind)
		if err := writeEmailBody(&buf, body); err != nil {
			return nil, nil, err
		}
	}
	return to, buf.Bytes(), nil
}

func validEmailMessageID(id string) bool {
	parts := strings.Split(id, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._+-@", r)) {
			return false
		}
	}
	return true
}

func writeEmailBody(w io.Writer, body string) error {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	encoded := quotedprintable.NewWriter(w)
	if _, err := io.WriteString(encoded, body); err != nil {
		return err
	}
	return encoded.Close()
}

// LogEmailSender 供开发环境显式注入，只验证邮件并记录元数据，不发送正文或记录凭据。
type LogEmailSender struct {
	logger *slog.Logger
	from   mail.Address
}

var _ EmailSender = (*LogEmailSender)(nil)

func NewLogEmailSender(from string, logger *slog.Logger) (*LogEmailSender, error) {
	addr, err := parseEmailAddress(from)
	if err != nil {
		return nil, emailError("config", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &LogEmailSender{from: *addr, logger: logger}, nil
}

func (s *LogEmailSender) Send(ctx context.Context, msg EmailMessage) error {
	if err := ctx.Err(); err != nil {
		return emailError("send", err)
	}
	if _, _, err := buildEmailMessage(s.from, msg); err != nil {
		return emailError("message", err)
	}
	s.logger.InfoContext(ctx, "email validated; development sender skipped delivery", "message_id", msg.MessageID)
	return nil
}
