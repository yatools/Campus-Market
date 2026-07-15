# 梧桐墙 · 单校校园社区

梧桐墙是可部署的单校校园社区：Vue 3 + TypeScript 前端、Go 1.26 后端、PostgreSQL 16、独立后台任务和 Nginx HTTPS 入口。用户端继续使用 V4 公告栏视觉，Go 后端保持原 `/api/v1` 契约、Cookie 会话、CSRF 和全部业务能力。

## 功能

- 校邮验证码注册、Argon2id 密码、服务端会话、CSRF、会话轮换与注销
- 首页混合动态、树洞、两级回帖、匿名昵称、点赞、收藏、举报、搜索与热榜
- 游戏目录审核、车票式车队、场次、提醒、请假、签到、转让、评价和日历订阅
- 问答、经验悬赏、生存手册、课程评价、校园服务评分
- 仅限校内线下面交的二手集市、校园活动、失物认领
- 观察台、治理公示、申诉、私信、通知、公告、反馈和完整管理后台
- SMTP outbox、车队生命周期、数据清理、PostgreSQL/上传文件备份

历史静态原型只保存在 `legacy/`，不进入构建或部署。

## 本地开发

前置条件：Go 1.26、Node.js 22、PostgreSQL 16、`vipsthumbnail`（libvips）。

创建数据库并准备环境变量：

```powershell
Copy-Item .env.example .env
$env:ENVIRONMENT = 'development'
$env:DATABASE_URL = 'postgresql://wutong:password@127.0.0.1:5432/wutong'
$env:ALLOWED_CAMPUS_EMAIL_DOMAINS = 'test.edu.cn'
```

启动后端：

```powershell
cd backend
go run ./cmd/wutong migrate up
go run ./cmd/wutong serve --addr 127.0.0.1:8000
```

另开终端启动 worker 和前端：

```powershell
cd backend
go run ./cmd/wutong worker

cd ..\frontend
npm install
npm run dev
```

本地注册仍经过真实 SMTP；验证码不会写入响应或日志。

## 从旧 SQLite 开发库导入

目标 PostgreSQL 必须是空业务库。命令先校验 Alembic 版本 `0005_credit_anonymous_team_lifecycle`、表结构与逐表行数，再迁移全部数据并复制上传和备份文件：

```powershell
cd backend
go run ./cmd/wutong import-sqlite --sqlite campus.db --uploads-source uploads --backups-source backups --dry-run
go run ./cmd/wutong import-sqlite --sqlite campus.db --uploads-source uploads --backups-source backups --report import-report.json
```

导入保留主键、密码哈希、匿名身份、审核记录和关联，并重置 PostgreSQL sequence。`--dry-run` 不创建表或写入目标库；重复导入或目标非空会被拒绝。

## 测试与生成代码

```powershell
cd backend
gofmt -w cmd internal api tools.go
go vet ./...
go test -race ./...
go build ./cmd/wutong
go run github.com/sqlc-dev/sqlc/cmd/sqlc generate
go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config oapi-codegen.yaml api/openapi-3.0.json

cd ..\frontend
npm run typecheck
npm test
npm run build
npx playwright test
```

`api/openapi.json` 是最终 FastAPI 版本导出的不可变兼容基线；`api/openapi-3.0.json` 仅用于 oapi-codegen。Go 路由测试会检查基线中的 134 个操作全部存在。

## Docker 与部署

生产只支持 PostgreSQL。首次启动由 `migrate` 服务运行 Goose；已有数据库若处于 Alembic 0005，会先校验 51 张表再登记 Go 基线，不会重复建表或删除 `alembic_version`。

```bash
cp .env.example .env
chmod 600 .env
export TLS_EMAIL=admin@example.edu.cn
sh deploy/bootstrap-tls.sh
sh deploy/deploy.sh
```

创建管理员：

```bash
docker compose exec api /app/wutong create-admin --email admin@example.edu.cn --nickname 站点管理员
```

检查状态：

```bash
docker compose ps
curl -fsS https://你的域名/health/ready
```

Staging 使用独立 Compose project、PostgreSQL volume、上传和备份 volume，只监听 `127.0.0.1:8080`：

```bash
cp .env.staging.example .env.staging
chmod 600 .env.staging
sh deploy/staging.sh
```

通过 SSH 隧道访问 Staging：

```bash
ssh -L 8080:127.0.0.1:8080 your-server
```

Staging 恢复演练必须显式使用独立环境与 Compose project，避免接触生产卷：

```bash
ENV_FILE=.env.staging \
COMPOSE_PROJECT_NAME=wutong-staging \
COMPOSE_FILE=docker-compose.yml:docker-compose.staging.yml \
sh deploy/restore.sh /secure/path/to/staging-backup.zip
```

更新前先从管理后台下载备份，再执行 `git pull --ff-only && sh deploy/deploy.sh`。完整恢复继续使用：

```bash
sh deploy/restore.sh /secure/path/wutong-backup-YYYYMMDD-HHMMSS.zip
```

恢复脚本会校验 ZIP 与 `SHA256SUMS`，重建 PostgreSQL、恢复 uploads，再运行 Go migrations。

每天由系统 cron 执行 `sh deploy/renew-tls.sh` 检查证书续期。首次 Go 切换不修改业务表，应用回滚可切回上一个 Python 镜像；后续 Goose 迁移只能在已备份且确认兼容时回滚。

## Staging 压测

创建专用压测账号，并从浏览器开发者工具取得 Staging 会话 Cookie 和 CSRF Cookie：

```bash
BASE_URL=http://127.0.0.1:8080 \
SESSION_COOKIE='仅用于 staging 的会话值' \
CSRF_TOKEN='对应的 CSRF Cookie 值' \
k6 run backend/tests/load/mixed_workload.js
```

脚本维持 80 个读取与 20 个写入虚拟用户，要求读取 P95 `<500 ms`、写入 P95 `<800 ms`、分类错误率 `<1%`。不得在生产执行压测。

## 运维命令

单一 `wutong` 二进制提供：

- `serve`：HTTP API、SSE、上传文件服务
- `worker`：邮件、提醒、清理、生命周期和备份
- `migrate up|status`：数据库迁移和状态
- `create-admin`：创建首位管理员
- `verify-config`：验证生产配置
- `import-sqlite`：旧 SQLite 全量导入

生产配置会拒绝示例域名、占位密钥、非 HTTPS 来源、缺失 SMTP 和非 PostgreSQL 数据库。

正式开放注册前还需在目标服务器实测真实校园 SMTP、DNS/TLS、备份恢复和上述负载阈值；这些外部环境验收不能由本地自动测试替代。
