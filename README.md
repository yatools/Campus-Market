# 梧桐墙

梧桐墙是面向单校部署的校园社区。当前工程架构：Vue 3 + TypeScript 前端、Go API/Worker、PostgreSQL 16、S3 兼容对象存储和 Nginx HTTPS 入口。`/api/v1` 是唯一业务 API 前缀，`backend/api/openapi.yaml` 是唯一接口契约。

## 前端演示
### 移动端
<img width="509" height="1134" alt="image" src="https://github.com/user-attachments/assets/c30b440b-6077-490c-8f19-942047f383bf" />
### 桌面端
<img width="2510" height="1223" alt="image" src="https://github.com/user-attachments/assets/5dee3c27-8c46-45aa-ba36-d55d896618d4" />
<img width="1284" height="976" alt="image" src="https://github.com/user-attachments/assets/f29b9400-d327-46ee-80e2-2951aec4b70b" />
<img width="1276" height="835" alt="image" src="https://github.com/user-attachments/assets/efe8f166-652a-4d2e-9df9-5ebcd90ad6f8" />
<img width="1269" height="886" alt="image" src="https://github.com/user-attachments/assets/d64d62ed-c630-4ff8-98ea-277f6ad9f86c" />
<img width="1284" height="889" alt="image" src="https://github.com/user-attachments/assets/b747e5bc-9629-4646-ba34-ee0316292fca" />
<img width="1288" height="864" alt="image" src="https://github.com/user-attachments/assets/f0914b3f-8ed2-4a7f-b1b6-b5980224fe52" />
<img width="1284" height="866" alt="image" src="https://github.com/user-attachments/assets/987b25de-af3a-4c68-ae9f-a15f926a3e0d" />
<img width="1279" height="869" alt="image" src="https://github.com/user-attachments/assets/4d50a28f-eb9f-4dd8-b347-d8cbd8c92f3a" />
文明观察台按理说按之前的原型要有个信用分大于多少且勾选【吃瓜不扩散协议】是能去码查看的，不过此处觉得有点问题还没做好，以后再说吧
<img width="1283" height="875" alt="image" src="https://github.com/user-attachments/assets/4251dac3-2509-4d7b-92c4-cf3d34aeeb13" />
<img width="1300" height="862" alt="image" src="https://github.com/user-attachments/assets/b0e6216a-0010-40cf-a6dc-028431f09b9a" />
<img width="1271" height="883" alt="image" src="https://github.com/user-attachments/assets/a79a0d50-ac6c-43e9-885a-811a7a20910b" />
<img width="1281" height="877" alt="image" src="https://github.com/user-attachments/assets/f3ee2d56-b78a-490e-8591-1cc4c53109c1" />
<img width="1281" height="898" alt="image" src="https://github.com/user-attachments/assets/39e9b042-85a0-4d40-9a72-d7aa268d9d54" />
<img width="1274" height="1032" alt="image" src="https://github.com/user-attachments/assets/166f2db8-b5a0-4628-a173-81e2d04ba3c4" />
<img width="1626" height="397" alt="image" src="https://github.com/user-attachments/assets/17fd1155-b6da-4324-949e-f0cc0b62d4be" />
<img width="1271" height="883" alt="image" src="https://github.com/user-attachments/assets/ea1f0e52-3ddb-4f0f-8bd3-fb9d5c110b4a" />
<img width="1284" height="889" alt="image" src="https://github.com/user-attachments/assets/626ea73d-9bf8-4f6c-9413-d88d5153ba55" />


## 主要能力

- 校园邮箱验证码注册、Argon2id 密码、服务端会话、CSRF、可信代理与原子限流。
- 动态、树洞、问答悬赏、手册、课程、活动、失物、观察台、车队、私信、通知和治理后台。
- 二手市场完整交易流程：申请、卖家接受、24 小时预留、双方确认、纠纷裁决和双盲评价。
- 商品价格使用整数分；交易状态与审核状态独立；成交数只统计真实完成的交易。
- public/private/backup 三类 S3 bucket。纠纷证据为私有对象，只能通过鉴权后的短期签名地址访问。
- Worker heartbeat、邮件积压、依赖健康、对象存储探测、备份保留与安全恢复。

## 本地开发

推荐使用 Docker Compose 启动 PostgreSQL、MinIO、API、Worker 和 Web：

```powershell
Copy-Item .env.example .env
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build
```

开发环境应把 `.env` 中的示例生产值改为本地值，至少设置数据库密码、`SECRET_KEY`、健康检查令牌和 MinIO 凭据。MinIO API 默认监听 `9000`，管理控制台默认监听 `9001`。

也可以只用 Compose 启动 PostgreSQL/MinIO，再分别运行进程：

```powershell
cd backend
go run ./cmd/wutong migrate up
go run ./cmd/wutong serve --addr 127.0.0.1:8000

# 另一个终端
go run ./cmd/wutong worker

# 另一个终端
cd ..\frontend
npm ci
npm run dev
```

前端挂载前会读取 `/app-config.json`，因此 API 前缀和 CSRF Cookie 名以服务端实际配置为准。

## 配置

复制 `.env.example` 后按环境修改。配置解析是严格的：非法布尔值、非法整数、越界连接池或时长、错误 Cookie 名、无效 CIDR、不一致的 HTTPS/Cookie 设置以及缺失的 S3/SMTP 生产配置都会阻止进程启动，并指出对应配置键。

生产前先执行：

```bash
docker compose run --rm api /app/wutong verify-config
```

反向代理必须覆盖客户端提交的转发头：

```nginx
proxy_set_header X-Forwarded-For $remote_addr;
proxy_set_header X-Real-IP $remote_addr;
```

API 仅当连接源地址属于 `TRUSTED_PROXY_CIDRS` 时才接受格式合法的 `X-Real-IP`。

## 数据库和代码生成

Goose 基线位于 `backend/internal/database/migrations/00001_baseline.sql`。全新环境执行：

```powershell
cd backend
go run ./cmd/wutong migrate up
go run ./cmd/wutong migrate status
```

验证迁移可逆性：

```powershell
go run ./cmd/wutong migrate up
go run ./cmd/wutong migrate down
go run ./cmd/wutong migrate up
```

更新 `backend/api/openapi.yaml` 或 SQL 后重新生成并检查无差异：

```powershell
cd backend
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.7.2 --config oapi-codegen.yaml api/openapi.yaml

cd ..\frontend
npm run generate:api
```

## 测试

```powershell
cd backend
gofmt -w cmd internal api
go vet ./...
go test -race -coverprofile=coverage.out ./...
go build ./cmd/wutong

cd ..\frontend
npm run lint
npm run typecheck
npm run test:coverage
npm run build
npx playwright test
```

需要 PostgreSQL 的并发、交易和迁移集成测试使用 `TEST_DATABASE_URL`；MinIO 集成测试使用 `TEST_S3_ENDPOINT`。CI 会启动隔离的 PostgreSQL 和 MinIO，并执行 Goose `up → down → up`、生成物一致性、依赖审计和镜像漏洞扫描；独立恢复演练还会验证临时数据库切换成功，以及就绪检查失败时自动换回原数据库。

## 健康检查

- `GET /health/live`：只表示 API 进程存活。
- `GET /health/ready`：检查数据库、Goose 当前版本和对象存储，失败返回 503。
- `GET /health/dependencies`：使用 `Authorization: Bearer <HEALTH_CHECK_TOKEN>`，返回依赖明细。
- `GET /api/v1/admin/system-health`：管理员查看 Worker heartbeat、邮件积压/失败、最近备份、磁盘和 S3 状态。

## 部署

生产对象存储必须位于独立故障域；Web/API/Worker 不挂载 uploads 或 backup volume。构建和启动示例：

```bash
cp .env.example .env
chmod 600 .env
export TLS_EMAIL=admin@example.edu.cn
sh deploy/bootstrap-tls.sh
sh deploy/deploy.sh
```

创建首位管理员：

```bash
docker compose exec api /app/wutong create-admin --email admin@example.edu.cn --nickname 站点管理员
```

检查部署：

```bash
docker compose ps
curl -fsS https://你的域名/health/ready
```

Staging 使用独立 Compose project、数据库和对象命名空间：

```bash
cp .env.staging.example .env.staging
chmod 600 .env.staging
sh deploy/staging.sh
```

## 备份与恢复

Worker 将 PostgreSQL custom dump、表计数、对象清单和校验和写入独立 backup bucket，并按 7 个每日、4 个每周、12 个月度恢复点保留。生产 bucket 还应启用版本控制和跨故障域复制。

恢复命令：

```bash
ENV_FILE=.env.staging \
COMPOSE_PROJECT_NAME=wutong-staging \
COMPOSE_FILE=docker-compose.yml:docker-compose.staging.yml \
sh deploy/restore.sh /secure/path/wutong-backup-YYYYMMDD-HHMMSS.zip
```

恢复脚本会拒绝不安全 ZIP 路径，校验 SHA-256 和 `pg_restore --list`，先恢复到临时数据库并验证迁移版本、关键表计数、外键与对象清单。验证成功后才短暂停写并重命名切换；切换后就绪检查失败会自动换回旧数据库。

## 运维命令

单一 `wutong` 二进制提供：

- `serve`：HTTP API、SSE、配置和健康端点。
- `worker`：邮件、清理、预留超时、heartbeat 和备份。
- `migrate up|down|status`：Goose 迁移。
- `create-admin`：创建管理员。
- `verify-config`：严格验证配置。
- `verify-storage-manifest`：从标准输入校验对象清单。

