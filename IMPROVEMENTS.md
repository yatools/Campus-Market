# 架构、安全边界与迁移状态

本文记录当前代码的工程边界和仍在进行的迁移，不复述审计过程或历史缺陷。实际行为以测试、OpenAPI 契约和当前实现为准。

## 1. 系统边界

### Web

- Vue 3 路由页面只负责挂载页面 feature。
- Explore、Admin、Me 和 Teams 的请求、状态编排、数据转换与业务动作位于 `frontend/src/features`。
- 迁移 feature 通过 `frontend/src/generated/sdk` 访问 HTTP API，不手写 URL 或响应断言。
- 外部数据在边界处按生成 DTO 或 `unknown` 处理，经解析后再进入页面 ViewModel。

### API

- `backend/internal/httpapi` 负责路由、认证上下文、请求解析、输入校验和 HTTP 错误映射。
- `backend/internal/market` 负责市场交易、纠纷和评价状态。
- `backend/internal/team` 负责车队成员、场次、请假、签到、转让、移除和互评状态。
- `backend/internal/governance` 负责账号停用、管理员账号更新、处罚、申诉和内容审核状态。
- 已迁移纵向切片的 service 控制事务；对应 SQL 只存在于 repository。
- 尚未迁移的内容、消息、校园服务、通知和部分账号操作仍由 legacy HTTP handler 直接编排。

### Worker

- Worker 处理邮件 Outbox、内容与附件清理、交易超时、车队提醒、heartbeat 和备份。
- Worker 与 API 通过数据库协调，不共享进程内状态。
- 备份任务、邮件任务和租约状态可从数据库恢复；对象存储操作遵循提交后清理边界。

## 2. HTTP 契约

`backend/api/openapi.yaml` 是迁移范围内唯一 HTTP DTO 来源。

每个操作必须有唯一 `operationId`。标记为 `x-contract-status: typed` 的操作还要求：

- 请求参数和 JSON 请求体使用明确 schema。
- 所有成功 JSON 响应使用明确 schema。
- `400`、`401`、`403`、`404`、`409`、`422` 和 `500` 使用统一 `ErrorResponse`。
- 成功响应不得以无字段对象或 `additionalProperties: true` 代替真实结构。

前端 SDK 配置位于 `frontend/openapi-ts.config.ts`，使用：

- `@hey-api/openapi-ts`
- `@hey-api/typescript`
- `@hey-api/client-fetch`
- `@hey-api/sdk`

`frontend/scripts/check-contracts.mjs` 检查 operationId、迁移标记和响应 schema；CI 还会执行破坏性变更检查和生成文件 diff。

## 3. 遗留 API 状态

`frontend/src/api.ts` 中的泛型 `api<T>()` 是遗留入口，不代表契约驱动调用。

允许的调用位置和数量记录在 `frontend/scripts/legacy-api-allowlist.json`。`npm run check:legacy-api` 禁止：

- 在 allowlist 外新增遗留调用。
- 在已有文件中增加遗留调用数量。
- 在已迁移的 Explore、Admin、Me 和 Teams feature 中回退到遗留入口。

当前仍使用遗留入口的主要页面包括首页、Dashboard、消息和搜索。后续迁移应先补齐对应 OpenAPI schema，再改用生成 SDK，并同步减少 allowlist。

## 4. TypeScript 类型规则

手写生产代码和测试统一启用：

```text
@typescript-eslint/no-explicit-any: error
```

允许例外：

- `frontend/src/generated`
- 第三方声明

`unknown` 只能出现在外部输入、浏览器存储或不可控 JSON 边界，并必须通过类型守卫或解析函数收窄。页面内部模型使用明确 ViewModel 和判别联合；例如 Explore 信息流的 `meta` 按内容类型建模。

## 5. 后端纵向切片规则

已迁移的后端域遵守以下约束：

- application service 负责权限、所有权、状态迁移、冲突和多写事务。
- repository 只暴露用例需要的操作，不提供通用 Repository、Manager 或 Helper。
- repository 负责 SQL 和数据库错误归一化。
- HTTP handler 不直接调用 `Begin`、`Query`、`QueryRow` 或 `Exec`。
- 跨域依赖通过用例所需的最小数据结构表达。
- 领域错误在 HTTP adapter 中映射到现有错误响应格式。

静态边界测试：

- `backend/internal/httpapi/market_boundaries_test.go`
- `backend/internal/httpapi/team_boundaries_test.go`
- `backend/internal/httpapi/governance_boundaries_test.go`

Repository 集成测试使用 `TEST_DATABASE_URL` 创建临时数据库并执行真实 Goose 迁移。

## 6. 安全边界

### 认证和会话

- 密码使用 Argon2id。
- 会话 token 只以摘要形式存储。
- 修改密码、停用账号和需要失效权限的管理员操作会撤销会话。
- 非安全 HTTP 方法要求 CSRF token。
- 验证码、登录、私信和敏感治理操作使用数据库支持的原子限流。

### 代理和网络

- 仅信任 `TRUSTED_PROXY_CIDRS` 中代理提供的客户端 IP。
- Host、CORS、Cookie Secure 和 Public Origin 在生产配置中交叉校验。
- `/metrics` 只用于内部监控网络。

### 内容和对象存储

- 富文本在输出前清洗。
- public、private 和 backup bucket 分离。
- 纠纷证据使用 private bucket 和短期签名 URL。
- 图片类型、尺寸、解码结果和声明 MIME 需要一致。
- 观察台原文需要信用门槛、协议确认、逐次审计和限流。

### 治理与交易

- 市场交易通过行锁和 service 状态机处理并发确认、取消与纠纷。
- 车队容量、请假、签到奖励和所有权迁移在事务内完成。
- 管理员不能通过账号更新接口限制自己的管理员账号。
- 审核决定不会把已删除或过期内容重新发布。

## 7. 数据与恢复

- Goose 管理数据库版本。
- CI 验证 `up → down → up` 和查询计划基线。
- 备份包包含 PostgreSQL custom dump、关键表计数、对象清单和校验和。
- 恢复先进入临时数据库，通过迁移版本、表计数、外键和对象清单校验后才切换。
- 切换失败自动恢复原数据库；切换成功后原库默认保留为 rollback 数据库。
- 对象本体不包含在数据库备份中，生产 bucket 仍需版本控制与跨故障域复制。

## 8. CI 验收

每个 Pull Request 必须通过：

- Compose 配置、POSIX shell 语法、ShellCheck 和部署脚本测试。
- Go 格式、vet、race、覆盖率、构建、迁移、staticcheck 和 govulncheck。
- OpenAPI 破坏性检查及 Go/TypeScript 生成物无漂移。
- 前端契约检查、legacy allowlist、ESLint、严格 typecheck、Vitest 和 Playwright。
- npm 高危依赖审计。
- PostgreSQL/MinIO 集成测试和恢复演练。
- Trivy 文件系统与 API、Worker、Web 镜像扫描。

`main` 的发布 job 直接加载安全扫描产出的镜像，不重新构建；发布物使用 commit SHA 标签，并产出 SBOM 与镜像 digest。

## 9. 后续迁移

后续工作应保持小步、串行和兼容：

1. 为尚未迁移的 operation 补齐 typed OpenAPI schema。
2. 将首页、Dashboard、消息和搜索迁移到生成 SDK，并减少 legacy allowlist。
3. 把内容、消息、校园服务、通知和剩余账号写操作迁入对应纵向切片。
4. 为新 service 增加真实数据库、并发冲突和事务回滚测试。

迁移不得改变现有路由、JSON 字段、鉴权语义或数据库 schema；产品行为修正应使用独立变更和独立测试。
