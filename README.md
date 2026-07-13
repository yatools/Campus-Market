# 梧桐墙 · 单校校园社区

梧桐墙现已从静态原型重构为可部署的单校社区：Vue 3 + TypeScript 前端、FastAPI + PostgreSQL 后端、独立后台任务、Alembic 迁移和 Nginx HTTPS 入口。

## 已实现模块

- 校邮验证码注册、Argon2id 密码、服务端会话、CSRF、会话撤销与账号注销
- 树洞、两级回帖、三种身份模式、点赞、收藏、举报、搜索和热榜
- 车队、发车场次、容量控制、请假、签到、转让、取消和队友评价
- 问答与经验悬赏、生存手册、课程目录与匿名课评
- 仅限线下面交的二手集市、校园活动、失物认领确认流程
- 观察台先审后发、指定回应方、治理公示与本人申诉
- 上下文私信、陌生人私信开关、拉黑、通知、公告与反馈
- 用户后台、审核工作队列、用户治理、审计日志和异步备份

旧版 HTML/JS 与 FastAPI/SQLite 原型分别保存在 `legacy/frontend-prototype/` 和 `legacy/backend-prototype/`，不再进入生产构建。

## 本地开发

后端默认使用本地 SQLite，仅用于开发与测试。生产环境只允许 PostgreSQL。

```powershell
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install -r backend\requirements-dev.txt
cd backend
..\.venv\Scripts\alembic.exe upgrade head
..\.venv\Scripts\uvicorn.exe app.main:app --reload --port 8000
```

另开终端启动前端：

```powershell
cd frontend
npm install
npm run dev
```

本地注册仍必须经过真实 SMTP；测试不会暴露或回传验证码。可以配置测试邮箱服务，或使用 pytest 中的受控验证码夹具。

## 测试

```powershell
cd backend
..\.venv\Scripts\python.exe -m ruff check app tests
..\.venv\Scripts\python.exe -m pytest --cov=app

cd ..\frontend
npm run typecheck
npm test
npm run build
```

Playwright 用例位于 `frontend/e2e/`，安装浏览器后运行 `npx playwright test`。100 并发基准脚本位于 `backend/tests/load/`。
CI 会使用 PostgreSQL 16 实际执行全部 Alembic 迁移、`alembic check`、后端测试、依赖审计、前端检查和 Playwright，而不是只在 SQLite 上验证。

Staging 上创建专用压测账号并从浏览器开发者工具取得会话 Cookie 与 CSRF Cookie 后，可执行读写混合验收：

```bash
BASE_URL=http://127.0.0.1:8080 \
SESSION_COOKIE='仅用于 staging 的会话值' \
CSRF_TOKEN='对应的 CSRF Cookie 值' \
k6 run backend/tests/load/mixed_workload.js
```

脚本同时维持 80 个读取与 20 个写入虚拟用户，并强制读取 P95 `<500 ms`、写入 P95 `<800 ms`、分类错误率 `<1%`。压测只允许在 Staging 执行，结束后删除压测内容和账号。

## 同机 Staging 验收

Staging 使用独立 Compose project、PostgreSQL volume、上传 volume 和备份 volume，仅监听服务器回环地址 `127.0.0.1:8080`，不会与生产 80/443 端口或数据混用：

```bash
cp .env.staging.example .env.staging
chmod 600 .env.staging
# 替换独立密钥、数据库密码、校园邮箱域和 SMTP 参数
sh deploy/staging.sh
```

从本机通过 SSH 隧道访问 `http://127.0.0.1:8080`：

```bash
ssh -L 8080:127.0.0.1:8080 your-server
```

在 Staging 完成注册、登录、发帖回帖、车队、市场、私信、审核、备份下载与恢复演练后，再初始化空生产库。Staging 配置为 `noindex`，也不得导入生产个人数据。
Staging 恢复演练使用独立 project 和覆盖文件，避免误碰生产：

```bash
ENV_FILE=.env.staging \
COMPOSE_PROJECT_NAME=wutong-staging \
COMPOSE_FILE=docker-compose.yml:docker-compose.staging.yml \
sh deploy/restore.sh /secure/path/to/staging-backup.zip
```

## Ubuntu 24.04 部署

前置条件：域名已解析到服务器、80/443 可访问、Docker Engine 与 Compose Plugin 已安装、校园邮箱域与 SMTP 参数可用。

1. 从示例创建生产配置并替换所有占位值：

   ```bash
   cp .env.example .env
   chmod 600 .env
   ```

   生产配置会拒绝示例域名、`replace-with-*` 密钥、非 PostgreSQL 数据库、非 HTTPS 公网地址以及缺失的 SMTP 参数。

2. 首次申请证书（`web` 服务尚未运行时）：

   ```bash
   export TLS_EMAIL=admin@example.edu.cn
   sh deploy/bootstrap-tls.sh
   ```

3. 构建、迁移并启动：

   ```bash
   sh deploy/deploy.sh
   ```

4. 创建首位管理员。密码不要写入命令历史；命令会安全提示输入：

   ```bash
   docker compose exec api python -m app.cli create-admin --email admin@example.edu.cn --nickname 站点管理员
   ```

5. 检查状态：

   ```bash
   docker compose ps
   curl -fsS https://你的域名/health/ready
   ```

6. 每天通过系统 cron 执行一次证书续期检查：`sh deploy/renew-tls.sh`。

数据库、上传文件和证书均使用 Docker volume；PostgreSQL 不映射公网端口。Nginx 强制 HTTPS，并配置 HSTS、CSP、上传限制与 API/IP 限流。

## 更新与回滚

更新前先从管理后台生成并下载备份，然后执行：

```bash
git pull --ff-only
sh deploy/deploy.sh
```

应用回滚使用上一稳定 Git 提交重新构建；数据库只允许在确认迁移可逆且已完成备份后执行 `alembic downgrade`。恢复完整备份：

```bash
sh deploy/restore.sh /secure/path/wutong-backup-YYYYMMDD-HHMMSS.zip
```

恢复会重建生产数据库，应在维护窗口执行并先再次保留当前数据。
恢复脚本会先验证 ZIP 可读性和包内 `SHA256SUMS`，校验失败时不会停止服务或改动数据库。

## 生产检查清单

- `.env` 未提交，`SECRET_KEY`、数据库密码和 SMTP 密码均为独立随机值
- 正式库为空库初始化，未导入任何演示账号或演示帖子
- 校邮验证码可收取，密码重置与车队提醒邮件已实测
- 管理员、审核员和普通用户权限分别完成验收
- 生成备份、下载到异地设备并完成一次恢复演练
- 配置外部可用性监测并定期检查容器日志、邮件失败和慢请求
