-- +goose Up
-- 观察台「去码查看」需要留痕的《吃瓜不扩散协议》同意记录。
-- 信用门槛沿用 credit_rules（threshold.observe_unmask），由 ensureCreditRulesSQL 播种，
-- 因此此处只需存储每个用户的协议签署版本与时间。
CREATE TABLE observe_unmask_agreements (
	user_id BIGINT NOT NULL,
	agreed_version VARCHAR(30) NOT NULL,
	agreed_at TIMESTAMP WITH TIME ZONE NOT NULL,
	PRIMARY KEY (user_id),
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS observe_unmask_agreements CASCADE;
