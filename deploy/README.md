# GitHub Actions 构建与部署

沿用原 cms-backend 项目的腾讯 CCR + Cloudflare Access SSH 方案，适配 v2 的 `CMS_*` 配置、SMTP 和数据库迁移。目标为安装 Docker Engine、Compose v2.20+、bash、flock、timeout 的 Linux 主机。服务器继续使用已有 cloudflared；本项目不安装或重启服务器 Tunnel。

## 流程

- 分支 main/master push、PR：`ci.yml` 运行 Go 竞态测试、真实 PostgreSQL 集成测试、环境文件/Compose 解析测试、Go vet、部署脚本语法检查和程序构建，不部署。
- 推送 `cms-backend-v*` 标签或手动运行 **Build and Deploy CMS Backend v2**：测试通过后构建并推送 CCR，再通过 SSH 部署不可变镜像 digest。
- 部署目录按 `releases/COMMIT-RUN-ATTEMPT` 保存对应提交的 Compose、脚本和环境文件；依次拉取镜像、等待数据库、替换服务、等待 `/healthz`、更新 current，再检查公网 HTTPS。数据库迁移由 server 启动时自动执行。

镜像默认 `ccr.ccs.tencentyun.com/pigpig/cms-backend-v2`，Compose 项目名固定 `cms-backend-v2`，目录默认 `/home/ubuntu/cms-backend-v2`，端口默认 `127.0.0.1:8082`。PostgreSQL 17 使用该项目自己的命名卷，不映射数据库端口。v1 和 v2 数据结构不同，不能直接复用旧项目数据库/卷；本流程不迁移旧业务数据。

根目录 `docker-compose.yaml` 仍为本地文件，不参与 Git 或生产部署。生产使用本目录的 `docker-compose.yml`。构建上下文排除了本地 Compose、环境文件和部署文件，避免本地配置进入镜像构建缓存。

## GitHub 配置

先将代码提交到实际 GitHub 仓库，并在腾讯 CCR 创建对应镜像仓库。以下两个凭据放在 **Repository Secrets**，供构建和部署共用：

| Secret | 用途 |
| --- | --- |
| CCR_USERNAME | CCR 登录用户名 |
| CCR_PASSWORD | 允许推送和拉取目标仓库的密码/令牌 |

可选 **Repository Variables**：

| Variable | 默认值 |
| --- | --- |
| CCR_NAMESPACE | pigpig |
| CCR_IMAGE_NAME | cms-backend-v2 |
| DEPLOY_PLATFORM | linux/amd64；ARM 主机可设置 linux/arm64 |

创建 **GitHub Environment：cms-backend-v2-production**。不要把 build 所需的 CCR 凭据仅放在 Environment Secrets，否则构建 job 无法取得。

Environment Secrets：

| Secret | 用途 |
| --- | --- |
| DEPLOY_SSH_PRIVATE_KEY | 部署账号 SSH 私钥，对应公钥已安装在服务器 |
| SSH_KNOWN_HOSTS | 从可信服务器终端获得的 SSH host key 行；主机名与 DEPLOY_SSH_HOST 一致 |
| CF_ACCESS_CLIENT_ID / CF_ACCESS_CLIENT_SECRET | SSH Access Service Token |
| POSTGRES_PASSWORD | 数据库密码，后续发布保持一致 |
| CMS_ADMIN_PASSWORD | 管理员初始化密码，每次启动配置仍需提供 |
| CMS_SMTP_PASSWORD | SMTP 发信密码/授权码 |

Environment Variables：

| Variable | 默认值或示例 |
| --- | --- |
| DEPLOY_SSH_HOST | 必填，例如 ssh.example.com |
| DEPLOY_SSH_USER | ubuntu |
| DEPLOY_DIR | /home/ubuntu/cms-backend-v2，账号需能创建目录 |
| CMS_TRUSTED_ORIGIN_URL | 必填，例如 https://www.example.com，只填前端 origin |
| BACKEND_ORIGIN | 必填，例如 https://api.example.com，用于部署后公网健康检查 |
| BACKEND_HOST_PORT | 8082 |
| POSTGRES_USER / POSTGRES_DB | cms / cms |
| CMS_ADMIN_NAME | admin |
| CMS_ADMIN_EMAIL | 372525247@qq.com，同时用作审核邮件收件人 |
| CMS_SMTP_HOST | 必填，例如 smtp.qiye.aliyun.com |
| CMS_SMTP_USERNAME | 必填，实际发信账号 |
| CMS_SMTP_FROM | 必填，例如 CMS &lt;sender@example.com&gt;，必须包含真实邮箱 |
| CMS_SMTP_PORT | 0，按 TLS 模式使用 465 或 587；可显式设置 |
| CMS_SMTP_TLS_MODE | tls 或 starttls，默认 tls |
| CMS_SMTP_MAX_CONCURRENCY | 3 |
| CMS_SMTP_CONNECT_TIMEOUT | 10s |
| CMS_SMTP_SEND_TIMEOUT | 20s，包含等待邮件并发额度的时间 |

生产 Compose 固定使用 `CMS_MODE=PROD`、`CMS_EMAIL_DRIVER=smtp`。不使用 v1 的 CSRF_KEY、PUBLIC_ORIGIN、BOOTSTRAP_ADMIN_ENABLED 或独立 COMMENT_MANIFEST_URL；文章清单来自 `${CMS_TRUSTED_ORIGIN_URL}/comment-manifest.json`。

配置生成器 `go run ./deploy/render-env OUTPUT_FILE` 检查 URL、邮箱、SMTP 参数、镜像 digest 和必需凭据，并编码 PostgreSQL DSN、转义 Compose dotenv 中的特殊字符。它不会打印密码或连接 SMTP。环境文件仅包含明确列出的变量，权限 600；release 目录权限 700。部署工具集中存放在 deploy 下，cmd 只保留 server 入口。

## Cloudflare 和服务器

部署前确保已有 SSH Tunnel，例如 `ssh.example.com -> ssh://localhost:22`，Access 策略使用 **Service Auth**，包含部署专用 Service Token。Actions runner 使用固定版本、校验 SHA256 的 cloudflared 客户端，SSH 使用严格 host key 校验。

为 v2 配置独立 API 域名，例如 `api-v2.example.com -> http://127.0.0.1:8082`。此处 cloudflared 在宿主机运行，目标使用回环地址，不能填 Compose 服务名 server。不要改动仍被旧服务使用的路由，直到前端切换完成。若 API 域名设置 Access 拦截，公开探活和前端代理也需要对应认证；当前公开健康检查不携带 Access Token。

部署账号需要 Docker 权限以及访问 CCR、Docker Hub、SMTP 和前端清单的网络权限。持久化数据由 Docker 命名卷管理，更新不执行 down，不删除数据卷。

`GET /healthz` 检查进程可响应及数据库连通性，最多等待数据库 2 秒，失败返回 503。健康不表示文章清单已经同步、SMTP 凭据有效或邮件已送达；清单加载失败不会阻止服务启动，需在管理员清单状态接口确认。

## 发布与失败处理

配置好后，可在 Actions 页面手动运行发布工作流，或执行：

```bash
git tag cms-backend-v2.0.0
git push origin cms-backend-v2.0.0
```

第三方 Actions 固定到已解析的完整提交 SHA。镜像按源码 SHA 打标签，部署使用 `image@sha256:...`；生产发布通过 GitHub concurrency 和服务器 flock 串行执行。cloudflared 版本与校验和沿用参考项目的 2026.9.3，更新时须一起修改。

CI/CD 和 Compose 不单独执行数据库迁移命令，镜像只包含 server 程序。server 在开始监听 HTTP 前执行幂等迁移，随后按当前应用逻辑创建管理员并补齐角色授权；已有同名账号时不会重置密码或邮箱。改变 CMS_ADMIN_NAME 可能创建另一个管理员，应保持稳定。

启动迁移失败或新 server 健康检查失败时，部署退出、不推进 current，打印日志以便排查，不自动回滚数据库；此时旧容器可能已经被替换。current 在本机健康后更新；之后公网探活失败表示 Tunnel/公开访问仍有问题，此时新服务可能已在运行。SMTP 及数据库密码保存在各 release 的受限环境文件中，历史 release 的保管和清理由服务器维护者负责。

数据库密码环境变量只控制首次初始化；修改 Secret 不会修改已有 PostgreSQL 角色密码。更新凭据需同步数据库账户。人工回退可检查 `previous-release`，确认迁移兼容，再登录镜像仓库并运行旧 release 的 deploy.sh；应用回退不回滚数据库。上线前应配置数据库备份。

## 本地验证

```bash
go test ./...
go vet ./...
bash -n deploy/deploy.sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -shellcheck= -pyflakes=
docker build -t cms-backend-v2-ci-check:local .
```

安装 Docker Compose CLI 时，Go 测试会验证实际 Compose 解析、凭据特殊字符、服务启动依赖和端口隔离；没有 CLI 时跳过该项。设置 TEST_DATABASE_URL 可运行评论与 Outbox 的隔离 schema 集成测试。上述命令不执行线上部署。

官方参考：[发布容器镜像](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)、[Compose 启动顺序](https://docs.docker.com/compose/how-tos/startup-order/)、[Cloudflare Service Tokens](https://developers.cloudflare.com/cloudflare-one/access-controls/service-credentials/service-tokens/)。
