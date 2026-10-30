# 通用邮件发送

实现在 `internal/pkg/email.go`。`main.go` 已在数据库迁移前加载并校验邮件配置，创建一个共享 Sender；初始化不会连接 SMTP 或发送邮件。该实例已注入评论审核通知 Task，处理 `comment.submitted` 事件，不创建 MailSrv。

## 启动配置

使用现有环境变量配置加载流程，不自动读取本地 `.env` 文件。Docker Compose 已转发下列变量，可通过 shell 环境或 Compose 的 `.env` 提供。

`Config.SMTP` 直接使用 `pkg.SMTPEmailConfig`，通过 `envPrefix:"CMS_SMTP_"` 解析子字段，初始化时直接传给 `NewSMTPEmailSender(cfg.SMTP)`。发件人变量统一为 `CMS_SMTP_FROM`（替代此前的 `CMS_EMAIL_FROM`）。

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| CMS_EMAIL_DRIVER | log | log 或 smtp；PROD 必须显式选择 smtp |
| CMS_SMTP_FROM | 空 | SMTP 必填；log 留空时使用 CMS <noreply@localhost> |
| CMS_SMTP_HOST | 空 | SMTP 服务器，不包含端口 |
| CMS_SMTP_PORT | 0 | 自动按 TLS 模式选择 465 或 587 |
| CMS_SMTP_TLS_MODE | tls | tls 或 starttls |
| CMS_SMTP_USERNAME | 空 | SMTP 登录用户名 |
| CMS_SMTP_PASSWORD | 空 | SMTP 登录密码，与用户名同时设置 |
| CMS_SMTP_MAX_CONCURRENCY | 3 | 同一实例的发送并发上限 |
| CMS_SMTP_CONNECT_TIMEOUT | 10s | 连接超时 |
| CMS_SMTP_SEND_TIMEOUT | 20s | 包含等待额度的总超时 |

DEV、TEST 默认使用日志发送器。选择 smtp 后配置不合法会阻止启动，不回退到 log。PROD 未选择 smtp 也会阻止启动。

配置示例（密码通过部署环境单独注入）：

```dotenv
CMS_EMAIL_DRIVER=smtp
CMS_ADMIN_EMAIL=372525247@qq.com
CMS_SMTP_FROM=CMS <sender@example.com>
CMS_SMTP_HOST=smtp.example.com
CMS_SMTP_PORT=465
CMS_SMTP_TLS_MODE=tls
CMS_SMTP_USERNAME=sender@example.com
CMS_SMTP_MAX_CONCURRENCY=3
CMS_SMTP_CONNECT_TIMEOUT=10s
CMS_SMTP_SEND_TIMEOUT=20s
```

## 创建发送器

```go
sender, err := pkg.NewSMTPEmailSender(pkg.SMTPEmailConfig{
    Host:           "smtp.example.com",
    Port:           465,
    TLSMode:        pkg.EmailTLS,
    Username:       "sender@example.com",
    Password:       smtpPassword, // 由启动配置或环境变量提供，不写入代码或 Outbox。
    From:           "CMS <sender@example.com>",
    MaxConcurrency: 3,
    ConnectTimeout: 5 * time.Second,
    SendTimeout:    20 * time.Second,
})
if err != nil {
    return err
}
```

- `EmailTLS` 表示连接后立即 TLS 握手；`EmailSTARTTLS` 表示先建立 SMTP 会话，再强制升级 TLS。没有明文回退。
- TLS 模式必须显式填写。端口为 0 时分别使用 465、587。实际可配置其他端口。
- 连接超时默认为 10 秒，整次发送超时默认为 20 秒，包含等待并发额度的时间；调用方 Context 截止时间更早时优先使用它。取消会结束等待或关闭连接，中断握手和 SMTP 读写。
- 使用系统信任的证书根和主机名校验，最低 TLS 1.2。
- 用户名、密码必须同时填写或同时为空。填写时使用 TLS 内的 AUTH PLAIN；同时为空适用于无需认证的受信任 SMTP 中继。
- `MaxConcurrency` 必须大于 0。Sender 与密码哈希器一样使用 `semaphore.Weighted`，每次发送通过 `Acquire(ctx, 1)` 获取额度；额度耗尽时阻塞等待，由 Context 控制超时或取消，不返回 Busy 错误。额度覆盖邮件构造和整个连接生命周期，连接关闭后通过 `Release(1)` 释放。
- 每个 SMTP 账号应创建一个 Sender 并共享给所有邮件 Task。不同实例、不同进程的额度独立；该限制不控制单位时间发送频率。

开发环境可显式使用 `pkg.NewLogEmailSender(from, logger)`。logger 为 nil 时使用 `slog.Default()`。该实现验证邮件并记录 MessageID，不输出收件人、主题、正文或密码，不执行网络发送；生产代码不应自动回退到此实现。

## 发送邮件

```go
err := sender.Send(ctx, pkg.EmailMessage{
    MessageID: "notification-123@example.com",
    To:        "reader@example.com",
    Subject:   "内容更新通知",
    Text:      "你关注的内容已经更新。",
    HTML:      "<p>你关注的内容已经更新。</p>",
})
```

一封邮件只允许一个收件人，支持带显示名称的地址。信封邮箱地址限制为 ASCII；中文域名应由配置方转换为 ASCII 域名，中文显示名称和主题正常编码。主题不能为空且不能包含控制字符；Text、HTML 至少填写一项。两者均填写时构造 `multipart/alternative`，正文使用 UTF-8 和 quoted-printable 编码。

MessageID 格式为 `id@domain`，不包含尖括号；当前允许字母、数字及 `._+-@`。留空时自动生成。后台重试需要调用方在事件中保存稳定标识，每次重建邮件时沿用。稳定 Message-ID 便于追踪，不提供服务端去重保证。

调用方负责生成业务内容。HTML 中包含用户输入时，应通过 `html/template` 等方式转义；发送器将 HTML 当作已构造完成的内容处理。

## 错误与 Outbox 接入

```go
var emailErr *pkg.EmailError
if errors.As(err, &emailErr) && emailErr.Retryable {
    // 交由任务执行框架延迟重试，不在 Sender 内循环发送。
}
```

所有发送错误通过 EmailError 包装，保留原始错误，可使用 `errors.Is` 检查取消、截止时间，或通过 `errors.As` 取得 SMTP 响应。

- SMTP 4xx、网络连接错误、EOF、取消和超时标记为可重试。
- SMTP 5xx、证书校验、无 STARTTLS、配置和邮件格式错误不标记为可重试。
- Send 返回 nil 表示 SMTP 接受了 DATA，不表示最终送达。DATA 被接受后，QUIT 失败不再返回投递失败。
- DATA 结果不明确、进程退出或 Outbox 成功回写失败时，重试可能重复投递。SMTP 无法提供严格一次投递保证。

业务 srv 在事务内写入 Outbox，再由对应 task 生成邮件、调用 Sender。Outbox 已识别 EmailError.Retryable，支持指数退避、过期租约回收、在途数量限制和基于租约的任务截止时间。评论通知的触发规则、收件人与重试边界见 [评论文档](comments.md#管理员审核邮件)。

## 验证

`go test ./internal/pkg` 使用本地回环地址的假 SMTP 服务器验证 TLS、STARTTLS、AUTH、邮件内容、错误分类与取消超时，不连接真实邮箱服务器。运行环境需要允许测试进程访问本机回环端口。
