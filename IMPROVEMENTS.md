# 梧桐墙 Campus-Market — 本次完善说明

> 环境限制：改动在无网络的云端沙盒完成，Go 依赖与 npm 均被出网策略拦截，**无法编译/跑测试**。
> 所有 Go 改动经 `gofmt` 语法校验并解析通过；前端/SQL 经人工审读。请在本机运行完整验证（见文末）。

本目录用 git 记录两次提交：`baseline`（从 yatools/Campus-Market@main 逐文件字节级重建的原始源码）
与 `improvements`（本次修复 + 观察台去码功能）。用 `git diff baseline improvements` 查看全部改动。

缺失的生成/自带大文件（未随抓取重建，构建前请从你的真实 clone 补齐或重新生成）见 `RECONSTRUCTION_NOTES.md`。

---

## 一、已修复的确定性缺陷

### Critical
1. **密码重置验证码可暴破接管账号** — `backend/internal/httpapi/auth.go` `resetPassword`
   新增按邮箱（8 次/时）+ 按 IP（60 次/时）限流，计数用独立事务提交（失败也计数）。
2. **登录限流因事务回滚失效** — `auth.go` `login`
   限流计数从请求事务内移出，改用新增的 `s.rateLimit`（独立提交），失败登录不再被回滚清零。
   `register` 同步改用该模式。

### High
3. **树洞匿名被 `reply_to_user_id` 击穿** — `backend/internal/httpapi/content.go` `commentPayloadQ`
   评论响应不再下发被回复者真实 user_id（改为 null），并同时修复隐藏评论墓碑仍返回作者/附件的问题。
4. **审核裁决可复活已删除/过期内容** — `backend/internal/httpapi/me_admin.go` `adminDecideModeration`
   裁决前判断实体状态：`deleted`/`expired` 只记录审核结果，不再改回 `published`。
5. **附件清理与 attach 竞态毁纠纷证据** — `backend/internal/worker/worker.go` `cleanup`
   待清理 SELECT 加 `FOR UPDATE SKIP LOCKED`；先带 `status='pending'` 守卫删行、删成功才删 S3 对象。
6. **车队 `voice_link` 存储型 XSS** — `teams.go`（`validVoiceLink` 校验 http(s)）+ `frontend/src/views/TeamsView.vue`（`:href` 前加 `^https?://` 守卫、补 `noreferrer`）。

### Medium
7. **SSE 被 90s WriteTimeout 掐断** — `governance.go` `notificationStream`：流开始时 `SetWriteDeadline(time.Time{})`。
8. **悬赏可双重支付** — `modules.go` `acceptAnswer`：`bounty_settled` 已结时只发 20 基础 XP。
9. **`/me/content?status=` 用不存在的列名 500** — `me_admin.go` `myContent`：`status` → `publication_status`。
10. **公示 `public_mask` 泄露匿名 alias** — `me_admin.go` `adminCreatePenalty`：改用 SecretKey 派生的稳定 6 位哈希码，不再取 alias 尾部（删除失效的 `lastRunes`）。
11. **`adminDecideFeedback` reward_xp 无校验** — 限定 `0..100`。
12. **metrics label 未转义可致 /metrics 中毒** — `metrics.go` 转义 label 值；`server.go` 未匹配路由归并为 `unmatched`（同时消除基数爆炸）。
13. **SMTP 头 CRLF 注入** — `worker.go` `sendEmail`：header 值剥离 CR/LF。
14. **`truncate` 按字节截断中文致落库失败循环** — `worker.go`：改为 UTF-8 边界安全截断。
15. **前端无全局 401 处理** — `frontend/src/api.ts` + `stores/auth.ts`：会话中途 401 时清理登录态并弹登录框（登出态不触发）。
16. **纠纷证据输入框全交易共用致证据串号** — `frontend/src/views/ExploreView.vue`：证据按交易 id 隔离（`evidenceFiles` map + 逐行输入）。
17. **取消预订/评分 prompt 仍提交** — `ExploreView.vue` `requestReservation`/`reviewTransaction`：`=== null` 判空中止 + try/catch + 评分校验。
18. **交易确认按钮忽略已确认状态** — `frontend/src/market.ts`：已确认方隐藏「我已完成面交」。

---

## 二、观察台「去码查看」功能（README 标注未完成，本次补全）

原型要求：信用分达标 **且** 勾选《吃瓜不扩散协议》后可查看观察帖原文。实现：

- **信用规则键** `threshold.observe_unmask`（默认 **800**，可在管理后台信用规则里调整）。已同步三处默认表：
  `backend/internal/app/services.go`、`backend/internal/httpapi/content.go`、`backend/internal/httpapi/governance.go`，
  以及前端 `frontend/src/stores/auth.ts` 默认值。
- **协议留痕**：新增迁移 `backend/internal/database/migrations/00004_observe_unmask_agreements.sql`
  （表 `observe_unmask_agreements`），并把 `LatestMigrationVersion` 提升到 4。
- **同意端点** `POST /me/observe-unmask-agreement`（幂等）；`GET /me` 附带 `observe_unmask_agreed` / `observe_unmask_threshold`。
- **去码端点** `GET /observe-posts/{id}/reveal`：审核员/管理员，或（信用达标 **且** 已签协议）返回原文；**每次去码写审计日志 `observe.unmask`**（对应「不扩散」的可追溯诉求）。列表/详情默认仍打码。
- **列表/详情**新增 `can_unmask` 标志供前端展示入口；前端在观察帖详情弹窗提供「🔓 去码查看原文」按钮，首次点击弹出《吃瓜不扩散协议》确认→签署→取原文（仍经 DOMPurify 渲染）。

> 说明：默认门槛 800 与去码走独立可审计端点，是在原型描述下我选的稳妥默认，可按需调整。
> 遗留提示：`maskObserve` 目前只遮数字/手机号（不含姓名、微信/QQ 字母 ID、邮箱）。去码对高信用用户开放后，
> 建议同步增强打码或在协议中明确该暴露面。

---

## 三、契约（openapi.yaml）待补（不影响功能与现有测试）

运行期请求校验中间件对未在契约中登记的路由**放行**，且测试仅对 register/post 响应做契约校验，故上述新端点与
`can_unmask` 等新字段不会破坏现有构建与测试。但为契约完整，建议把以下补进 `backend/api/openapi.yaml` 后重新生成：
`GET /observe-posts/{observeID}/reveal`、`POST /me/observe-unmask-agreement`，以及 observe 响应的 `can_unmask` 字段、
`/me` 的 `observe_unmask_agreed`/`observe_unmask_threshold` 字段。

---

## 四、本机验证步骤

```bash
# 1) 把缺失的生成/自带文件从你的真实 clone 复制回来（见 RECONSTRUCTION_NOTES.md），或重新生成：
cd backend
go generate ./...   # 或按 README 手动跑 sqlc / oapi-codegen
# 2) 后端
gofmt -l cmd internal        # 应无输出
go vet ./...
go build ./cmd/wutong
go test -race ./...          # 需要 TEST_DATABASE_URL / TEST_S3_ENDPOINT 的集成测试见 README
go run ./cmd/wutong migrate up && go run ./cmd/wutong migrate down && go run ./cmd/wutong migrate up  # 验证 00004 可逆
# 3) 前端
cd ../frontend
npm ci
npm run generate:api         # 若已按第三节更新 openapi.yaml
npm run lint && npm run typecheck && npm run test:coverage && npm run build
```

---

## 五、审计中「设计取舍/低优先」未动的项（清单见随附审计报告）

为控制无编译环境下的回归风险，本次未改动的较大/较发散项（如 accept 并发死锁的加锁顺序重排、
restore/staging 部署脚本、nginx keepalive/OCSP、会话去重竞态、hub 监听协程在优雅关闭时占用连接等）
均已在审计报告中给出定位与建议，留待你在可编译验证的环境中处理。其中「hub 优雅关闭挂死」需要改动
`httpapi.New` 签名以传入生命周期 context，涉及测试改动，故未在本次盲改。
