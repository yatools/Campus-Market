-- +goose Up
-- 1) 备份任务租约。
-- 原实现只认领 status='pending' 的任务，worker 在 createBackup 期间被 OOM/重启杀掉后，
-- 任务会永远停在 'running'：后续轮次全部跳过它，而该 job 每轮都以「无待办」正常返回，
-- 于是 worker 心跳与 last_success 一切正常，备份却静默停产。
ALTER TABLE backup_jobs ADD COLUMN IF NOT EXISTS lease_until TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS ix_backup_jobs_claim ON backup_jobs (status, lease_until, created_at);

-- 2) 删除建在无长度上限 TEXT 上的 btree 索引。
-- posts.body 是 TEXT，应用层允许 10000 字符；btree 索引元组上限为 2704 字节且不允许行外
-- TOAST，因此约 900 个汉字以上的树洞帖 INSERT/UPDATE 必然报
-- "index row size N exceeds btree version 4 maximum 2704"，发帖与编辑直接 500。
-- 该索引对搜索也没有价值：LIKE '%q%' 用不上 btree 前缀索引，正文搜索走的是 00002 建的
-- ix_posts_body_trgm（gin_trgm）。这里改建一个按 board 过滤用的窄索引。
DROP INDEX IF EXISTS ix_posts_search;
CREATE INDEX IF NOT EXISTS ix_posts_board_entity ON posts (board, entity_id);

-- 3) /feed 与 /feed/changes 的支撑索引。
-- 两者分别按 updated_at 排序和按 updated_at 区间过滤，而 content_entities 上只有
-- created_at/owner_id/type/publication_status/moderation_status 五个单列索引，
-- 首页与轮询接口只能全表排序/扫描，延迟随数据量线性劣化。
CREATE INDEX IF NOT EXISTS ix_content_entities_feed ON content_entities (updated_at DESC, id DESC)
	WHERE publication_status = 'published';

-- 4) 清理冗余索引：ix_email_outbox_status（00001 所建）是 00002 的
-- ix_email_outbox_claim (status, next_attempt_at, lease_until, id) 的严格前缀；
-- ix_email_outbox_to_email 没有任何生产查询使用；ix_backup_jobs_status（00001）同样是
-- 上面新建的 ix_backup_jobs_claim 的严格前缀。三者只增加写放大。
DROP INDEX IF EXISTS ix_email_outbox_status;
DROP INDEX IF EXISTS ix_email_outbox_to_email;
DROP INDEX IF EXISTS ix_backup_jobs_status;

-- 5) 回收升级前就卡在 running 的备份任务：它们的 lease_until 为 NULL，
-- 若不处理会继续永远得不到认领。
UPDATE backup_jobs SET status='pending' WHERE status='running';

-- 6) 把「观察台去码」门槛抬到高于新用户初始信用。
-- ensureCreditRulesSQL 用的是 ON CONFLICT DO NOTHING，只补缺失的键、不改已有值，
-- 因此仅改代码默认值对存量库无效：两者都是 800 时，任何新注册账号签个协议就能看原文。
UPDATE credit_rules SET value=900, updated_at=now()
WHERE key='threshold.observe_unmask'
  AND value <= (SELECT value FROM credit_rules WHERE key='baseline.initial_credit');

-- +goose Down
-- 刻意不重建 ix_posts_search：它正是本次要删除的、会让长树洞帖写入失败的索引，
-- 回滚时重建它会在任何已有长帖的库上直接失败（btree 元组上限 2704 字节），
-- 把回滚本身也卡死。其余对象按原样还原。
CREATE INDEX IF NOT EXISTS ix_backup_jobs_status ON backup_jobs (status);
CREATE INDEX IF NOT EXISTS ix_email_outbox_to_email ON email_outbox (to_email);
CREATE INDEX IF NOT EXISTS ix_email_outbox_status ON email_outbox (status);
DROP INDEX IF EXISTS ix_content_entities_feed;
DROP INDEX IF EXISTS ix_posts_board_entity;
DROP INDEX IF EXISTS ix_backup_jobs_claim;
ALTER TABLE backup_jobs DROP COLUMN IF EXISTS lease_until;
