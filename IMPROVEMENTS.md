# 梧桐墙 Campus-Market — 第二轮全面审计与修复

> **验证边界**：改动在无网络的云端沙盒完成，Go 模块代理与 npm 源均被出网策略拦截，
> **无法编译、无法跑测试**。所有 Go 改动经 `gofmt -e`、自建 AST 包级符号解析器、
> 以及 SQL 列名与 migrations schema 交叉校验器（1055 条语句）验证；前端经 TS/Vue 语法
> 与模板标签配对校验；shell 脚本经 `sh -n`（dash）验证。**类型正确性无法被静态工具完全
> 覆盖**，请务必在可编译环境执行文末验证步骤。

本次共修改 57 个文件（+2402 / −588）。完整的缺陷清单、成因分析与未处理项见随附的
**《梧桐墙 Campus-Market 全面审计与修复报告》**。

## 审计方法

两轮。第一轮 7 个独立审计员分领域逐行读代码并交叉核对 migrations；第二轮在修复完成后，
由 2 个独立复核员对全部改动做对抗性复查，专门找**修复本身引入的错误**——找出并修正了
11 个真实问题，其中包括一处会让搜索接口全量 500 的 SQL 转义错误（Go 反引号字符串里的
`ESCAPE '\\'` 会原样发给 PostgreSQL）、一处会让整个维护任务永久停摆的 NOT NULL 违反、
以及一处让 panic 被记成成功的 defer 顺序错误。

## 一、Critical

1. **部署回滚会把服务打到不可恢复状态** — `deploy.sh` 回滚只换镜像不回滚迁移，而就绪检查
   要求库版本与二进制严格相等，回滚后旧 API 永久 503；compose 健康检查用的是 `/health/live`，
   容器仍判为健康，流量照进。此后每次部署都会失败并再次回滚，形成死循环。
   → 就绪检查改为「版本不低于期望」；回滚时显式提示迁移未回滚。
2. **私信「发起新会话」在第一条会话之后全站不可用** — `findConversation` 在游标未读完时用
   同一 `pgx.Tx` 发起内层查询，`conn busy` → 500。现有测试只在空库上跑，走空循环分支。
   → 改写为单条自连接 SQL，同时把 O(全站会话数) 的 N+1 降到 O(1)。
3. **两条 worker 告警永远不会触发** — worker 不监听 HTTP，进程内计数器不在任何抓取目标上，
   表达式恒为空向量。worker 崩溃、备份停产都不会告警。
   → 由 API 从数据库导出 `worker_heartbeat_*`，告警改基于该指标并补 `absent()` 兜底。

## 二、High

4. **车队签到可无限自刷信用分，全站门槛整体失效** — 奖励按 (run,user) 记账而新建场次会重置，
   建场次与签到均不限流，两次请求换 2 分 → 任何账号可拉满 1000 分越过全部门槛。
   → 改为发车后 30 分钟内 + 场次/车队活跃 + 本场 ≥2 人；建场次 8 次/天、奖励 4 次/天。
5. **单方已确认的预留被超时静默作废且零救济** — `expired` 是绝对终态：不能纠纷、不能评价、
   管理员也无法裁决，商品同时释放回在售。买家钱货两空。
   → 只释放双方均未确认的预留；有单方确认的转 `disputed` 并自动建纠纷工单。
6. **审核员可裁决自己参与的纠纷，裁决终局不可撤销** → 增加当事人回避检查。
7. **长树洞帖必然写入失败** — `ix_posts_search` 是建在无上限 TEXT 上的 btree，约 900 汉字
   以上必报索引行超限，发帖与编辑直接 500。该索引本身无用（搜索走 trgm）。
   → 迁移 00005 删除，改建按 board 的窄索引。
8. **编辑任何已有回答的问题都会 500** — `questionPayload` / `offeringPayload` 的嵌套游标查询。
   → 先读完游标再做派生查询。
9. **前端单测全线红、e2e 纠纷用例必挂** — 上一轮的 401 处理漏改 4 个测试 mock；证据输入框
   按交易隔离后 e2e 仍用旧选择器。→ 补齐 mock 导出；加回按交易 id 的 `id` 并同步 e2e。
10. **管理员取消「信用分变化」仍会开出处罚单** — `Number(null)===0`，后端接受 0。
    → 判空返回 + 范围校验。同类「取消 prompt 仍提交」另修 4 处。
11. **恢复流程的表计数校验只验了第一张表** — `docker compose exec -T` 吃掉了循环的 stdin。
    这是恢复唯一的完整性闸门。CI 演练只写一行，恰好绕过。
    → `</dev/null` 隔离；演练改写 3 行并断言「已校验 3 张表」。
12. **CI 扫描的镜像与推送的镜像不是同一个** — 两个 job 各自 `docker build`，基础镜像是浮动 tag，
    被部署的 digest 从未被扫描。→ `docker save`/`load` 传递扫描通过的镜像。
13. **契约破坏性检查每个 PR 都会崩** — 用 `JSON.parse` 解析 YAML。
    → 内置 YAML 子集解析器（含行尾注释与紧凑序列，已实测），并补齐 path 层参数、`$ref`
    解引用、「新增必填 / 可选转必填」的反向检查。
14. **TLS 证书 90 天后必然全站不可达** — `renew-tls.sh` 无任何调度，且 bootstrap 用 standalone
    签发、renew 用 webroot，authenticator 不匹配；叠加一年期 HSTS 连绕过都点不了。
    → bootstrap 后切到 webroot 并打印 crontab 行；renew 增加剩余有效期检查。

## 三、Medium（择要）

**观察台去码**：门槛默认值等于新用户初始信用（800/800）且判定是 `<`，新账号签个协议就能读原文
→ 提到 900 并补数据迁移（`ensureCreditRulesSQL` 是 DO NOTHING，只改代码对存量库无效）；
审计日志改 fail-closed；端点 GET→POST（带副作用的 GET 免 CSRF，可跨站伪造他人去码记录）；
补按用户限流（此前可脚本遍历全站原文）；协议签署改为版本感知；限权用户不再能去码。

**打码覆盖面**：此前只遮 6–18 位数字，姓名/邮箱/微信 QQ 字母 ID/带分隔符手机号全部漏网——
而打码视图是**未登录公众可见**的，泄漏发生在门槛之前。→ 重写为多级遮蔽并验证误报。

**Worker**：提交前删 S3 对象（回滚后附件行复活而对象已灭失 → 纠纷证据永久丢失）；备份任务
无租约（崩溃后永久卡 running，而心跳一片绿）；panic 杀整个进程；三处游标缺 `rows.Err()`。

**认证**：Argon2id 在事务内计算（约 20 并发登录占满连接池）；不存在账号跳过校验形成数量级
时序差异；`requestCode` 可枚举邮箱、`change_email` 无需登录（邮件轰炸中继）；验证码猜错不
计数不作废；`truncate` 按字节截断致长中文 UA 客户端**完全无法登录**；会话 Cookie 在提交前下发。

**其余**：附件永远无法解绑（删图功能实质不存在）；4 个 update handler 把编辑**后**内容写进版本
历史（改了就洗白）；`publishHandbook` 命中风控词不建工单（草稿永久锁死）；过期树洞帖仍从信息流
泄露全文；accept 加锁顺序成环；accept 不复核发布/审核状态；卖家可单方改价且无痕；取消车队漏
重置 `team_run_members`；`removeTeamMember` 可向任意用户推送定制通知；metrics method 标签无界；
`/metrics` 持锁写响应；`getPost` 每次浏览取行级排他锁；评论 N+1 与回复不分页；31 处
`writeJSON` 后 `return rows.Err()` 导致响应体拼接两个 JSON。

**部署**：Zip Slip 绕不过符号链接；`trap` 绑 INT/TERM 却不退出（Ctrl-C 删完目录继续跑）；切换后
30 秒内删原库（误恢复无法挽回，现默认保留）；生产库不可达时脚本第一步就退出；两次 rename 非
原子；并发部署共享 candidate.env；readiness 不重试 connection refused；nginx keepalive 因缺
`Connection ""` 从未生效；OCSP 因缺 resolver 静默失效；`noindex` 加在首页而树洞照常被收录；
80 端口 `$host` 跳转构成开放重定向；开发 MinIO 弱口令监听 0.0.0.0；缺 `.dockerignore`。

## 四、必须由你完成

1. **契约同步**（`backend/api/openapi.yaml` 未随仓库重建，我无法核对）：
   `GET /observe-posts/{observeID}/reveal` **已改为 POST**；新增 `POST /me/observe-unmask-agreement`；
   observe 响应的 `can_unmask`；`/me` 的 `observe_unmask_agreed` / `observe_unmask_threshold`。
   不同步会导致运行期契约校验与 `routes_test.go` 的契约路由测试失败。
2. **重新生成生成物**：我按 sqlc 约定手工补了 `ObserveUnmaskAgreement`（00004 新增、上一轮未
   同步，CI 的 `git diff --exit-code` 必然失败）与 `BackupJob.LeaseUntil`（00005 新增）。
   请执行 `sqlc generate` 确认 diff 为空。
3. **基础镜像按 digest 固定**：4 个 Dockerfile 基础镜像仍是浮动 tag。沙盒无法解析 digest
   （写错会直接让构建失败），需要你在有网络的环境补上。

## 五、验证步骤

```bash
cd backend
go generate ./...
git diff --exit-code -- internal/dbgen internal/openapi
gofmt -l cmd internal api        # 应无输出
go vet ./...
go build ./cmd/wutong
go test -race ./...              # 集成测试需 TEST_DATABASE_URL / TEST_S3_ENDPOINT
go run ./cmd/wutong migrate up && go run ./cmd/wutong migrate down && go run ./cmd/wutong migrate up

cd ../frontend
npm ci
npm run generate:api             # 契约更新后
npm run lint && npm run typecheck && npm run test:coverage && npm run build
npx playwright test

cd ..
sh deploy/test-deploy.sh
sh deploy/test-restore.sh        # 需要 docker
```

## 六、已定位但本次未处理

搜索最小长度仍为 2（2 字符中文用不上 trgm 索引，六表全表扫描；提到 3 会伤中文体验，正解是
`pg_bigm`/全文索引，属产品决策）；纠纷仍只有发起方能举证（功能缺失，需新增端点与契约）；
成交数与评分无女巫成本；`email_outbox` / `audit_logs` / `worker_heartbeats` / `moderation_cases`
无清理策略；`TRUSTED_PROXY_CIDRS` 不校验前缀宽度；`resolveClientIP` 只读 `X-Real-IP`（换只设
`X-Forwarded-For` 的入口会让全体用户共用一个限流桶）；对象存储不在备份包内（README 已补充说明）。
