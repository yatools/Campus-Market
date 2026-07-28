# 梧桐墙

梧桐墙是面向单校部署的校园社区系统。仓库包含 Vue 3 Web 前端、Go API 与后台 Worker、PostgreSQL 数据库、S3 兼容对象存储，以及用于 HTTPS 入口的 Nginx 配置。

## 前端演示

### 移动端

<img width="509" height="1134" alt="梧桐墙移动端界面" src="https://github.com/user-attachments/assets/9ae94436-9fd0-45bd-ae5c-0dc53179fa4b" />

### 桌面端

<img width="2494" height="1225" alt="梧桐墙桌面端界面" src="https://github.com/user-attachments/assets/6a4773df-632a-4faf-92dc-00b5f35458ca" />
<img width="2489" height="1196" alt="梧桐墙桌面端界面" src="https://github.com/user-attachments/assets/6513ba7a-6d1c-4864-bbe7-4fcafddded53" />
<img width="1276" height="835" alt="梧桐墙桌面端界面" src="https://github.com/user-attachments/assets/09c95177-80ec-455d-8b24-783bf4c67acc" />
<img width="1269" height="886" alt="梧桐墙桌面端界面" src="https://github.com/user-attachments/assets/579d2d72-ea95-4ed3-bae6-a1c99d46e4ad" />
<img width="1288" height="864" alt="梧桐墙桌面端界面" src="https://github.com/user-attachments/assets/03a61014-981f-45d7-9f25-9551c024d318" />
<img width="1347" height="933" alt="梧桐墙桌面端界面" src="https://github.com/user-attachments/assets/8b8293a2-a51f-49ce-bc77-0bb7eddfa22d" />
<img width="1274" height="1032" alt="梧桐墙桌面端界面" src="https://github.com/user-attachments/assets/fcb34640-d8f6-48b3-8de0-58b6d5d7f31f" />
<img width="1626" height="397" alt="梧桐墙桌面端界面" src="https://github.com/user-attachments/assets/9d8e40ea-3275-411a-acbf-ce6ea1d2c628" />

## 主要能力

- 校园邮箱注册、Argon2id 密码、服务端会话、CSRF 防护和请求限流。
- 动态、树洞、问答、手册、课程、活动、失物、观察台、车队、私信和通知。
- 二手市场的预约、接受、双方确认、纠纷处理和双盲评价流程。
- 内容举报、审核、处罚、申诉、公告、反馈和账号管理。
- PostgreSQL 备份、对象清单校验、恢复演练、健康检查和 Prometheus 指标。

## 架构

运行时由以下组件组成：

- `frontend`：Vue 3、TypeScript、Pinia、Vue Router 和 Vite。
- `backend/cmd/wutong serve`：Chi HTTP API、认证、SSE 和健康检查。
- `backend/cmd/wutong worker`：邮件 Outbox、定时清理、交易超时、备份和 heartbeat。
- PostgreSQL 16：业务数据、会话、任务状态和审计记录。
- S3 兼容存储：公开附件、私有纠纷证据和备份对象。
- Nginx：Web 静态资源、API 反向代理和 TLS 终止。

后端正在按纵向切片迁移。市场交易、车队成员与场次、治理与账号状态操作已经使用 application service 和窄职责 PostgreSQL repository；这些 service 控制多写事务，HTTP handler 只处理认证上下文、请求校验和错误映射。其他模块仍保留在 legacy HTTP 层，迁移状态见 [IMPROVEMENTS.md](IMPROVEMENTS.md)。

## HTTP 契约与前端类型

`backend/api/openapi.yaml` 是 HTTP DTO 的契约来源。所有操作必须有唯一 `operationId`；标记为 `x-contract-status: typed` 的迁移操作还必须声明具体请求、成功响应和统一错误响应。

前端通过 `@hey-api/openapi-ts`、`@hey-api/client-fetch` 和 SDK 插件生成按 `operationId` 命名的调用函数。Explore、Admin、Me 和 Teams feature 使用生成 SDK；尚未迁移的页面通过受 allowlist 约束的 `api()` 遗留入口访问。

重新生成并检查：

```powershell
cd frontend
npm ci
npm run check:contracts
npm run check:legacy-api
npm run generate:api
git diff --exit-code -- src/generated
```

手写 TypeScript 源码和测试启用 `@typescript-eslint/no-explicit-any: error`。生成目录不参与该规则。

## 本地开发

要求：

- Docker 与 Docker Compose
- 或 Go 1.26、Node.js 22、PostgreSQL 16 和 S3 兼容存储

复制开发配置并启动完整环境：

```powershell
Copy-Item .env.example .env
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build
```

示例配置中的生产占位值必须替换。至少应设置数据库密码、`SECRET_KEY`、健康检查令牌和对象存储凭据。

分别运行服务：

```powershell
# 先启动 PostgreSQL 和 MinIO
docker compose -f docker-compose.yml -f docker-compose.dev.yml up db minio

# API
cd backend
go run ./cmd/wutong migrate up
go run ./cmd/wutong serve --addr 127.0.0.1:8000

# Worker（另一个终端）
cd backend
go run ./cmd/wutong worker

# Web（另一个终端）
cd frontend
npm ci
npm run dev
```

前端启动时读取 `/app-config.json`，以服务端配置的 API 前缀和 CSRF Cookie 名为准。

## 配置与安全边界

配置解析采用严格模式。非法布尔值、越界数值、错误 Cookie 名、无效 CIDR，以及生产环境缺失的 S3/SMTP 配置会阻止进程启动。

```bash
docker compose run --rm api /app/wutong verify-config
```

部署时需要遵守以下边界：

- 仅当连接源属于 `TRUSTED_PROXY_CIDRS` 时，API 才采信代理传入的客户端地址。
- 生产 Cookie 必须启用 Secure，并与 HTTPS 配置一致。
- 纠纷附件使用 private bucket，通过鉴权后的短期签名 URL 访问。
- 备份使用独立 backup bucket；数据库备份不替代对象存储的版本控制和跨故障域复制。
- `/metrics` 供内部监控网络抓取，不由 Nginx 对公网暴露。

反向代理应覆盖客户端提交的转发头：

```nginx
proxy_set_header X-Forwarded-For $remote_addr;
proxy_set_header X-Real-IP $remote_addr;
```

## 数据库与生成物

Goose 迁移位于 `backend/internal/database/migrations`。

```powershell
cd backend
go run ./cmd/wutong migrate up
go run ./cmd/wutong migrate status
```

更新 SQL 或 OpenAPI 后执行：

```powershell
cd backend
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.7.2 --config oapi-codegen.yaml api/openapi.yaml
git diff --exit-code -- internal/dbgen internal/openapi

cd ..\frontend
npm run generate:api
git diff --exit-code -- src/generated
```

## 测试

后端：

```powershell
cd backend
gofmt -w cmd internal api
go vet ./...
go test -race -coverprofile=coverage.out ./...
go build ./cmd/wutong
```

前端：

```powershell
cd frontend
npm run check:contracts
npm run check:legacy-api
npm run lint
npm run typecheck
npm run test:coverage
npm run build
npx playwright test
```

需要 PostgreSQL 的集成测试读取 `TEST_DATABASE_URL`；对象存储测试读取 `TEST_S3_ENDPOINT`。

GitHub Actions 还会检查：

- OpenAPI 合法性、`operationId` 唯一性、迁移响应 schema 和破坏性契约变更。
- sqlc、oapi-codegen 和前端 SDK 生成文件无漂移。
- Go race detector、覆盖率门槛、staticcheck 和 govulncheck。
- ESLint、严格 TypeScript、Vitest、Playwright 和 npm 高危依赖审计。
- Compose 配置、ShellCheck、部署脚本和完整恢复演练。
- Trivy 文件系统与镜像扫描；`main` 只发布已扫描镜像的不可变 SHA 标签。

## 健康检查与运维

- `GET /health/live`：API 进程存活。
- `GET /health/ready`：数据库迁移版本和对象存储可用。
- `GET /health/dependencies`：携带健康检查令牌后返回依赖明细。
- `GET /api/v1/admin/system-health`：管理员查看 Worker、邮件、备份、磁盘和对象存储状态。
- `GET /metrics`：HTTP、数据库连接池、邮件、Worker heartbeat 和备份指标。

单一 `wutong` 二进制提供：

- `serve`
- `worker`
- `migrate up|down|status`
- `create-admin`
- `verify-config`
- `verify-storage-manifest`

## 部署与恢复

生产部署接受已扫描的镜像 digest：

```bash
cp .env.example .env
chmod 600 .env
export TLS_EMAIL=admin@example.edu.cn
sh deploy/bootstrap-tls.sh
sh deploy/deploy.sh \
  ghcr.io/owner/repo/api@sha256:API_DIGEST \
  ghcr.io/owner/repo/worker@sha256:WORKER_DIGEST \
  ghcr.io/owner/repo/web@sha256:WEB_DIGEST
```

创建首位管理员：

```bash
docker compose exec api /app/wutong create-admin \
  --email admin@example.edu.cn \
  --nickname 站点管理员
```

恢复脚本先写入临时数据库，验证迁移、关键表计数、外键和对象清单，再短暂停写并切换数据库：

```bash
ENV_FILE=.env.staging \
COMPOSE_PROJECT_NAME=wutong-staging \
COMPOSE_FILE=docker-compose.yml:docker-compose.staging.yml \
sh deploy/restore.sh /secure/path/wutong-backup-YYYYMMDD-HHMMSS.zip
```

切换成功后，原数据库默认保留为 `<库名>_rollback_<时间戳>`。确认恢复结果后再手工删除，或显式设置 `RESTORE_DROP_OLD=1`。

## 许可

项目使用 [PolyForm Noncommercial License 1.0.0](LICENSE)。商业部署、托管、再销售或其他超出许可范围的使用，需要事先取得著作权人的书面授权。
