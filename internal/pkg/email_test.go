package pkg

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func testEmailMessage() EmailMessage {
	return EmailMessage{MessageID: "event-123@example.com", To: "访客 <reader@example.com>", Subject: "内容更新通知", Text: "你好\n正文", HTML: "<p>你好</p>"}
}

func testSMTPConfig() SMTPEmailConfig {
	return SMTPEmailConfig{Host: "localhost", TLSMode: EmailTLS, From: "站点 <sender@example.com>", MaxConcurrency: 2}
}

func assertSMTPAllCapacityAvailable(t *testing.T, sender *SMTPEmailSender) {
	t.Helper()
	capacity := int64(sender.cfg.MaxConcurrency)
	if !sender.concurrency.TryAcquire(capacity) {
		t.Fatal("sender leaked concurrency capacity")
	}
	sender.concurrency.Release(capacity)
}

func TestEmailMIME(t *testing.T) {
	from, _ := mail.ParseAddress(testSMTPConfig().From)
	for _, kind := range []string{"multipart", "text", "html"} {
		t.Run(kind, func(t *testing.T) {
			msg := testEmailMessage()
			if kind == "text" {
				msg.HTML = ""
			}
			if kind == "html" {
				msg.Text = ""
			}
			_, raw, err := buildEmailMessage(*from, msg)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := mail.ReadMessage(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
			if err != nil || subject != msg.Subject || parsed.Header.Get("Message-ID") != "<"+msg.MessageID+">" {
				t.Fatalf("headers=%v err=%v", parsed.Header, err)
			}
			addr, err := parsed.Header.AddressList("From")
			if err != nil || addr[0].Name != "站点" {
				t.Fatalf("from=%v err=%v", addr, err)
			}
			media, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
			if err != nil {
				t.Fatal(err)
			}
			if kind == "multipart" {
				if media != "multipart/alternative" {
					t.Fatal(media)
				}
				reader := multipart.NewReader(parsed.Body, params["boundary"])
				for i, want := range []string{"你好\r\n正文", msg.HTML} {
					part, err := reader.NextPart() // 自动解码 quoted-printable。
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(part)
					if err != nil || string(body) != want {
						t.Fatalf("part %d=%q err=%v", i, body, err)
					}
				}
				if _, err := reader.NextPart(); !errors.Is(err, io.EOF) {
					t.Fatalf("unexpected part: %v", err)
				}
			} else {
				body, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
				want := msg.HTML
				if kind == "text" {
					want = "你好\r\n正文"
				}
				if err != nil || string(body) != want || media != "text/"+map[string]string{"text": "plain", "html": "html"}[kind] {
					t.Fatalf("body=%q type=%s err=%v", body, media, err)
				}
			}
		})
	}
}

func TestEmailValidation(t *testing.T) {
	for _, mutate := range []func(*SMTPEmailConfig){
		func(c *SMTPEmailConfig) { c.MaxConcurrency = 0 },
		func(c *SMTPEmailConfig) { c.MaxConcurrency = -1 },
		func(c *SMTPEmailConfig) { c.Host = "" },
		func(c *SMTPEmailConfig) { c.Host = "smtp.example.com:465" },
		func(c *SMTPEmailConfig) { c.TLSMode = "plain" },
		func(c *SMTPEmailConfig) { c.Port = 65536 },
		func(c *SMTPEmailConfig) { c.SendTimeout = -1 },
		func(c *SMTPEmailConfig) { c.From = "x@example.com\r\nBcc: other@example.com" },
		func(c *SMTPEmailConfig) { c.Username = "user" },
	} {
		cfg := testSMTPConfig()
		mutate(&cfg)
		if _, err := NewSMTPEmailSender(cfg); err == nil {
			t.Fatalf("accepted invalid configuration")
		}
	}
	from, _ := mail.ParseAddress(testSMTPConfig().From)
	for _, mutate := range []func(*EmailMessage){
		func(m *EmailMessage) { m.Subject = "hello\r\nBcc: x@example.com" },
		func(m *EmailMessage) { m.Subject = "" },
		func(m *EmailMessage) { m.To = "a@example.com,b@example.com" },
		func(m *EmailMessage) { m.To = "a@example.com\n" },
		func(m *EmailMessage) { m.To = "访客@example.com" },
		func(m *EmailMessage) { m.MessageID = "<invalid@example.com>" },
		func(m *EmailMessage) { m.Text = ""; m.HTML = "" },
		func(m *EmailMessage) { m.Text = string([]byte{0xff}) },
	} {
		msg := testEmailMessage()
		mutate(&msg)
		if _, _, err := buildEmailMessage(*from, msg); err == nil {
			t.Fatal("accepted invalid message")
		}
	}
	for _, mode := range []EmailTLSMode{EmailTLS, EmailSTARTTLS} {
		cfg := testSMTPConfig()
		cfg.TLSMode = mode
		sender, err := NewSMTPEmailSender(cfg)
		if err != nil || sender.cfg.SendTimeout <= 0 || sender.cfg.ConnectTimeout <= 0 {
			t.Fatalf("defaults: %v", err)
		}
	}
}

type smtpTestServer struct {
	mode           EmailTLSMode
	missingTLS     bool
	recipientCode  int
	authCode       int
	stallGreeting  bool
	closeAfterData bool
	untrusted      bool
}

func fakeSMTP(t *testing.T, options smtpTestServer) (*SMTPEmailSender, <-chan []byte, <-chan struct{}) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	data := make(chan []byte, 1)
	ready, done, stop := make(chan struct{}), make(chan struct{}), make(chan struct{})
	serverErr := make(chan error, 1)
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		close(ready)
		if options.stallGreeting {
			<-stop
			return
		}
		secure := options.mode == EmailTLS
		if secure {
			conn = tls.Server(conn, serverTLS)
		}
		proto := textproto.NewConn(conn)
		if err := proto.PrintfLine("220 local test SMTP"); err != nil {
			return
		}
		for {
			line, err := proto.ReadLine()
			if err != nil {
				return
			}
			command := strings.SplitN(line, " ", 2)[0]
			switch command {
			case "EHLO":
				if secure {
					err = proto.PrintfLine("250-localhost\r\n250 AUTH PLAIN")
				} else if options.missingTLS {
					err = proto.PrintfLine("250 localhost")
				} else {
					err = proto.PrintfLine("250-localhost\r\n250 STARTTLS")
				}
			case "STARTTLS":
				if err = proto.PrintfLine("220 begin TLS"); err != nil {
					return
				}
				conn = tls.Server(conn, serverTLS)
				proto = textproto.NewConn(conn)
				secure = true
			case "AUTH":
				if !secure {
					serverErr <- errors.New("authentication before TLS")
					return
				}
				if line != "AUTH PLAIN "+base64.StdEncoding.EncodeToString([]byte("\x00user\x00password")) {
					serverErr <- errors.New("unexpected credentials")
					return
				}
				code := options.authCode
				if code == 0 {
					code = 235
				}
				err = proto.PrintfLine("%d authentication response", code)
			case "*":
				err = proto.PrintfLine("501 authentication canceled")
			case "MAIL":
				if line != "MAIL FROM:<sender@example.com>" {
					serverErr <- fmt.Errorf("unexpected envelope: %s", line)
					return
				}
				err = proto.PrintfLine("250 sender accepted")
			case "RCPT":
				if line != "RCPT TO:<reader@example.com>" {
					serverErr <- fmt.Errorf("unexpected recipient: %s", line)
					return
				}
				code := options.recipientCode
				if code == 0 {
					code = 250
				}
				err = proto.PrintfLine("%d recipient response", code)
			case "DATA":
				if err = proto.PrintfLine("354 send data"); err != nil {
					return
				}
				body, err := proto.ReadDotBytes()
				if err != nil {
					return
				}
				data <- body
				err = proto.PrintfLine("250 accepted")
				if options.closeAfterData {
					return
				}
			case "QUIT":
				_ = proto.PrintfLine("221 goodbye")
				return
			default:
				serverErr <- fmt.Errorf("unexpected command: %s", command)
				return
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("SMTP test server did not stop")
		}
		select {
		case err := <-serverErr:
			t.Error(err)
		default:
		}
	})
	cfg := testSMTPConfig()
	cfg.Host, cfg.Port, cfg.TLSMode = "127.0.0.1", listener.Addr().(*net.TCPAddr).Port, options.mode
	cfg.Username, cfg.Password = "user", "password"
	sender, err := NewSMTPEmailSender(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !options.untrusted {
		trusted, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		sender.tlsConfig.RootCAs = x509.NewCertPool()
		sender.tlsConfig.RootCAs.AddCert(trusted)
	}
	return sender, data, ready
}

func TestSMTPSend(t *testing.T) {
	for _, mode := range []EmailTLSMode{EmailTLS, EmailSTARTTLS} {
		t.Run(string(mode), func(t *testing.T) {
			sender, data, _ := fakeSMTP(t, smtpTestServer{mode: mode, closeAfterData: true})
			if err := sender.Send(context.Background(), testEmailMessage()); err != nil {
				t.Fatal(err)
			}
			assertSMTPAllCapacityAvailable(t, sender)
			select {
			case body := <-data:
				parsed, err := mail.ReadMessage(bytes.NewReader(body))
				if err != nil || parsed.Header.Get("Message-ID") != "<event-123@example.com>" {
					t.Fatalf("message: %v", err)
				}
			default:
				t.Fatal("no message received")
			}
		})
	}
}

func TestSMTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  smtpTestServer
		retry bool
	}{
		{"temporary", smtpTestServer{mode: EmailTLS, recipientCode: 451}, true},
		{"permanent", smtpTestServer{mode: EmailSTARTTLS, recipientCode: 550}, false},
		{"auth", smtpTestServer{mode: EmailTLS, authCode: 535}, false},
		{"missing-starttls", smtpTestServer{mode: EmailSTARTTLS, missingTLS: true}, false},
		{"untrusted-cert", smtpTestServer{mode: EmailTLS, untrusted: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender, data, _ := fakeSMTP(t, tc.opts)
			err := sender.Send(context.Background(), testEmailMessage())
			assertSMTPAllCapacityAvailable(t, sender)
			var sendErr *EmailError
			if !errors.As(err, &sendErr) || sendErr.Retryable != tc.retry {
				t.Fatalf("error=%v expected retry=%v", err, tc.retry)
			}
			select {
			case <-data:
				t.Fatal("unexpected delivery")
			default:
			}
		})
	}
}

func TestSMTPCancellationAndTimeout(t *testing.T) {
	for _, mode := range []EmailTLSMode{EmailTLS, EmailSTARTTLS} {
		for _, timeout := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/timeout=%v", mode, timeout), func(t *testing.T) {
				sender, _, ready := fakeSMTP(t, smtpTestServer{mode: mode, stallGreeting: true})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if timeout {
					sender.cfg.SendTimeout = 100 * time.Millisecond
				}
				result := make(chan error, 1)
				go func() { result <- sender.Send(ctx, testEmailMessage()) }()
				select {
				case <-ready:
				case <-time.After(3 * time.Second):
					t.Fatal("no connection")
				}
				want := context.DeadlineExceeded
				if !timeout {
					want = context.Canceled
					cancel()
				}
				select {
				case err := <-result:
					var sendErr *EmailError
					if !errors.Is(err, want) || !errors.As(err, &sendErr) || !sendErr.Retryable {
						t.Fatalf("err=%v want=%v", err, want)
					}
					assertSMTPAllCapacityAvailable(t, sender)
				case <-time.After(3 * time.Second):
					t.Fatal("send did not stop")
				}
			})
		}
	}
}

func TestLogEmailSenderDoesNotLogContent(t *testing.T) {
	var output bytes.Buffer
	sender, err := NewLogEmailSender("sender@example.com", slog.New(slog.NewTextHandler(&output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	msg := testEmailMessage()
	if err := sender.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), msg.MessageID) || strings.Contains(output.String(), msg.To) || strings.Contains(output.String(), msg.Subject) || strings.Contains(output.String(), msg.HTML) {
		t.Fatalf("unexpected log: %s", output.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sender.Send(ctx, msg); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSMTPWaitForSlot(t *testing.T) {
	for _, kind := range []string{"cancel", "caller-deadline", "send-timeout"} {
		t.Run(kind, func(t *testing.T) {
			cfg := testSMTPConfig()
			cfg.MaxConcurrency = 1
			if kind == "send-timeout" {
				cfg.SendTimeout = 50 * time.Millisecond
			}
			sender, err := NewSMTPEmailSender(cfg)
			if err != nil {
				t.Fatal(err)
			}
			release, err := sender.acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "caller-deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 50*time.Millisecond)
				defer stop()
			}
			result := make(chan error, 1)
			go func() { result <- sender.Send(ctx, testEmailMessage()) }()
			want := context.DeadlineExceeded
			if kind == "cancel" {
				select {
				case err := <-result:
					t.Fatalf("saturated sender returned instead of waiting: %v", err)
				case <-time.After(30 * time.Millisecond):
				}
				want = context.Canceled
				cancel()
			}
			select {
			case err := <-result:
				var emailErr *EmailError
				if !errors.Is(err, want) || !errors.As(err, &emailErr) || emailErr.Op != "acquire" || !emailErr.Retryable {
					t.Fatalf("waiting error=%v, want %v", err, want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("waiting did not observe context")
			}
			if sender.concurrency.TryAcquire(1) {
				sender.concurrency.Release(1)
				t.Fatal("canceled waiter changed the occupied slot")
			}
		})
	}
}

func TestSMTPInvalidMessageAndCanceledContextRelease(t *testing.T) {
	cfg := testSMTPConfig()
	cfg.MaxConcurrency = 1
	sender, err := NewSMTPEmailSender(cfg)
	if err != nil {
		t.Fatal(err)
	}
	msg := testEmailMessage()
	msg.To = "invalid"
	var emailErr *EmailError
	if err := sender.Send(context.Background(), msg); !errors.As(err, &emailErr) || emailErr.Op != "message" {
		t.Fatalf("expected validation error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 100 {
		if err := sender.Send(ctx, testEmailMessage()); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation: %v", err)
		}
		assertSMTPAllCapacityAvailable(t, sender)
	}
	// 校验失败及预先取消均不影响后续调用获取全部额度。
	release, err := sender.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestSMTPConnectionConcurrencyAndResume(t *testing.T) {
	// 连接停留在握手阶段，让并发额度持续被占用，观察后续 Send 是否拨号。
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	var connections []net.Conn
	results := make(chan error, 5)
	started := 0
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		<-done
		for _, conn := range connections {
			_ = conn.Close()
		}
		for {
			select {
			case conn := <-accepted:
				_ = conn.Close()
			default:
				for range started {
					select {
					case <-results:
					case <-time.After(3 * time.Second):
						t.Error("send goroutine did not stop")
					}
				}
				return
			}
		}
	})
	cfg := testSMTPConfig()
	cfg.Host, cfg.Port = "127.0.0.1", listener.Addr().(*net.TCPAddr).Port
	cfg.MaxConcurrency = 2
	sender, err := NewSMTPEmailSender(cfg)
	if err != nil {
		t.Fatal(err)
	}
	firstCtx, cancelFirst := context.WithCancel(ctx)
	defer cancelFirst()
	start := func(ctx context.Context) {
		started++
		go func() { results <- sender.Send(ctx, testEmailMessage()) }()
	}
	waitConnection := func() net.Conn {
		t.Helper()
		select {
		case conn := <-accepted:
			connections = append(connections, conn)
			return conn
		case <-time.After(3 * time.Second):
			t.Fatal("expected SMTP connection")
			return nil
		}
	}
	start(firstCtx)
	firstConn := waitConnection()
	start(ctx)
	waitConnection()
	for range 3 {
		start(ctx)
	}
	select {
	case conn := <-accepted:
		connections = append(connections, conn)
		t.Fatal("connection count exceeded MaxConcurrency")
	case <-time.After(50 * time.Millisecond):
	}
	cancelFirst()
	select {
	case err := <-results:
		started--
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first send error=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first send did not cancel")
	}
	// 消费已发送的 ClientHello，随后必须读到 EOF，证明归还额度前连接已关闭。
	_ = firstConn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.Copy(io.Discard, firstConn); err != nil {
		t.Fatalf("canceled connection was not closed: %v", err)
	}
	waitConnection() // 额度释放后，等待中的调用应自动继续。
	select {
	case conn := <-accepted:
		connections = append(connections, conn)
		t.Fatal("too many waiters resumed")
	case <-time.After(50 * time.Millisecond):
	}
}
