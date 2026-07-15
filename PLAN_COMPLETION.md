# Go 后端重构完成清单

| 项目 | 状态 | 实现 |
|---|---|---|
| Vue 3 + TypeScript + Vite 前端保持不变 | 完成 | `frontend/` |
| Go 1.26 单二进制 API/worker/CLI | 完成 | `backend/cmd/wutong/`、`backend/internal/` |
| 134 个 OpenAPI 操作及 SSE/health | 完成 | `backend/internal/httpapi/`、路由契约测试 |
| PostgreSQL 16、pgx、sqlc、Goose | 完成 | `backend/internal/database/`、`backend/internal/dbgen/` |
| Alembic 0005 原地接管 | 完成 | 表结构指纹检查并保留 `alembic_version` |
| SQLite 0005 全量导入 | 完成 | `wutong import-sqlite`、逐表计数/sequence/文件校验 |
| Python Argon2id 密码兼容 | 完成 | 固定 Python 哈希兼容测试 |
| Cookie 会话、轮换、CSRF 与安全头 | 完成 | Go 中间件与认证服务 |
| 树洞、车队、校园栏目、通信治理、管理后台 | 完成 | Go 路由及 PostgreSQL 事务 |
| 邮件、提醒、清理、生命周期和备份 worker | 完成 | `backend/internal/worker/` |
| OpenAPI/oapi-codegen/sqlc 生成链路 | 完成 | 固化 3.1 基线与 3.0 生成视图 |
| Docker、Compose、CI、部署文档 | 完成 | Go 多阶段镜像及 PostgreSQL 集成 CI |

前端源码、路由和 API 调用方式未因后端语言迁移而改变。历史原型只保留在 `legacy/`，不参与构建和部署。

## 本地验收记录（2026-07-15）

- Go 编译、`go vet`、空库迁移、Alembic 0005 接管、PostgreSQL API/worker 集成测试通过。
- 当前 `campus.db` 的 51 张表、124 行数据完成 dry-run 与真实导入，逐表计数、sequence 和外键检查通过。
- 图片上传实测 PNG→WebP 原图/缩略图、仅缩小规则和 MIME 不匹配拒绝通过。
- 前端 6 个 Vitest、类型检查、生产构建和 30 个 Chromium/WebKit/移动端 Playwright 用例通过。
- 目标 Ubuntu 的真实 SMTP、DNS/TLS、备份恢复和 100 并发 P95 仍属于上线环境验收项。
