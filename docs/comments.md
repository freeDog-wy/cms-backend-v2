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

审核示例：

```json
{"version":1,"status":"approved","reason":""}
```

status 支持 pending、approved、rejected。删除请求体为 `{"version":1,"reason":""}`。版本冲突返回 409；成功修改递增版本号，审计写入失败则整个事务回滚。

公开列表按 ID 升序，后台列表按 ID 倒序。next_cursor 是最后一条返回记录的 ID，分别传入 after_id 和 before_id。默认每页 20 条，上限 100。ID 通过 JSON 字符串传输。

## 验证

普通测试：`go test ./...`。

现有测试覆盖 Token 摘要校验，以及 Session 和权限中间件对无效凭据、缺失身份和数据库错误的处理。当前尚无评论数据库集成测试；重复提交并发、并发审核、公开可见性及审计失败回滚仍需通过 PostgreSQL 集成测试验证。
