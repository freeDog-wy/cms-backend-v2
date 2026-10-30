# 评论接口

后端提供 `/api/v1` 接口，响应遵循 `{code,message,data}`。匿名提交无需账号，管理员接口经过 Session 认证和 `comment:moderate` 权限中间件。

## 启动配置

- `CMS_TRUSTED_ORIGIN_URL`：浏览器访问静态站的来源，例如 `https://example.com`，供现有 CSRF 来源检查使用。
- 后端从 `${CMS_TRUSTED_ORIGIN_URL}/comment-manifest.json` 读取静态站文章清单。
- 其余数据库和管理员账号配置沿用现有配置。

启动时补充 admin 角色、comment:moderate 权限，以及配置的管理员账号与角色的绑定。已有角色的禁用状态不会被重置。

文章清单在启动时加载，此后每 5 分钟刷新。失败时保留进程内的最近有效快照；超过 1 小时未刷新成功后拒绝新评论，但仍允许读取快照中的文章评论。进程重启后需重新加载；初次加载失败时评论接口返回 503，其他业务继续运行。可从管理接口手动刷新。文章关闭评论后仍可读取已有评论。

## 路由

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | /api/v1/articles/:key/comments | 顶层评论；after_id、limit |
| POST | /api/v1/articles/:key/comments | 匿名提交 |
| GET | /api/v1/comments/:id/replies | 回复；after_id、limit |
| GET | /api/v1/admin/comments | 后台列表；status、article_key、before_id、limit |
| PATCH | /api/v1/admin/comments/:id/moderation | 审核 |
| DELETE | /api/v1/admin/comments/:id | 软删除 |
| POST | /api/v1/admin/articles/:key/comments | 管理员评论或回复，直接通过 |
| GET | /api/v1/admin/comment-manifest | 清单加载状态 |
| POST | /api/v1/admin/comment-manifest/refresh | 刷新清单 |
| GET | /api/v1/auth/me | 当前用户 ID、权限列表 |
| POST | /api/v1/auth/logout | 删除当前 Session 并清除 Cookie |

登录使用 `POST /api/v1/auth/login`，Cookie 名称为 `session_token`。所有 `/admin` 评论路由均先认证，再验证权限。静态站已使用新接口、统一响应和来源检查，不再请求旧版 CSRF Token 接口。

匿名提交示例：

```json
{
  "guest_name": "访客",
  "content": "评论内容",
  "parent_id": null,
  "request_id": "e5154cbc-4359-4ff0-97b1-1cffba9f5605"
}
```

回复时 parent_id 使用顶层评论 ID 的字符串。昵称最多 40 个字符，正文最多 2000 个字符。请求体上限 16 KiB。匿名提交所有文章共用 IP 限流，每 IP 每分钟 5 次，最多保留 10000 个 IP 状态；重启清空，多个实例独立计数。沿用现有代理信任配置，部署时需确保 ClientIP 对应真实访客。

同一次提交重试必须复用 request_id；内容或目标发生变化时生成新 UUID。同标识同内容返回原 ID，不同内容返回 409。成功返回 202 和 `data.id`，评论仍须审核后公开。

## 管理员审核邮件

匿名顶层评论及回复首次插入时，在同一事务内写入 `comment.submitted` Outbox 事件。事件写入失败会回滚评论；相同 request_id 的重试不产生额外通知。管理员直接发布的评论不产生审核通知，接入前已有的待审核评论不会自动补发。

`task.CommentReviewTask` 接收共享 EmailSender，读取评论当前状态，只对仍为 pending、未删除的访客评论发送纯文本邮件。已审核、已删除或不存在的评论直接完成任务。邮件包含评论编号、文章 UUID、昵称、提交时间、正文，以及回复的父评论编号；当前不包含文章标题或后台页面链接。数据库读取与邮件投递之间仍可能发生审核，状态以后台为准。

收件人来自启动配置 `CMS_ADMIN_EMAIL`，默认 `admin@example.com`，不查询管理员用户表中的邮箱。本项目部署时需设置 `CMS_ADMIN_EMAIL=372525247@qq.com`。修改此配置不会自动更新已有管理员账号的邮箱。实际发信还需配置 `CMS_EMAIL_DRIVER=smtp`、`CMS_SMTP_FROM`、`CMS_SMTP_HOST` 及 SMTP 凭据，详见 [邮件配置](email.md)。DEV、TEST 默认 log 驱动只记录消息标识，不发送邮件。

Outbox 每 10 秒扫描一次，每个进程最多保留 32 条在途任务。当前 main 配置租约为 40 秒；任务 Context 在租约到期前 5 秒截止，覆盖排队、数据库查询、等待邮件额度和发送。结果回写另给最多 3 秒。调整 SMTP SendTimeout 时应同时调整租约，预留数据库查询及结果回写时间。

临时邮件错误、取消、超时及未分类的执行错误允许重试；永久邮件错误、非法 Payload 和未注册事件直接失败。重试使用从 10 秒开始的指数退避并加入最多 20% 抖动，总延迟上限 15 分钟；最多尝试 10 次。每轮扫描先回收过期租约，延迟 30 秒再重试。SMTP 已接受但结果回写失败等情况仍可能造成重复邮件，稳定 Message-ID 只用于追踪。

审核示例：

```json
{"version":1,"status":"approved","reason":""}
```

status 支持 pending、approved、rejected。删除请求体为 `{"version":1,"reason":""}`。版本冲突返回 409；成功修改递增版本号，审计写入失败则整个事务回滚。

公开列表按 ID 升序，后台列表按 ID 倒序。next_cursor 是最后一条返回记录的 ID，分别传入 after_id 和 before_id。默认每页 20 条，上限 100。ID 通过 JSON 字符串传输。

## 验证

普通测试：`go test ./...`。

设置 `TEST_DATABASE_URL` 为专用测试 PostgreSQL 的连接地址，再执行 `go test ./...` 可额外运行事务与通知集成测试。测试需要创建 schema 的权限，每次使用随机隔离 schema，结束后删除；未设置时跳过。请勿指向生产数据库。

集成测试覆盖并发重复提交只生成一个事件、事件写入失败时评论回滚、回复通知与管理员评论不通知、真实 Outbox 到邮件记录器的调用、已审核评论跳过、重试退避与次数耗尽、未知事件、任务 panic、租约回收及旧令牌不能回写。邮件使用记录器，不投递真实邮件。并发审核、公开可见性及审计失败回滚仍需补充数据库集成测试。
