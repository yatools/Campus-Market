package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const MaxCredit = 1000

type Querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type CreditRuleDefault struct {
	Label, Kind string
	Value       int
	Description string
}

var CreditDefaults = map[string]CreditRuleDefault{
	"baseline.initial_credit":   {"新用户初始信用", "baseline", 800, "仅影响新注册用户"},
	"threshold.anonymous_post":  {"完全匿名发帖", "threshold", 600, "树洞完全匿名发帖门槛"},
	"threshold.team_create":     {"创建游戏车队", "threshold", 600, "发布开车门槛"},
	"threshold.course_review":   {"评价课程", "threshold", 600, "提交课程评价门槛"},
	"threshold.listing_publish": {"发布交易帖", "threshold", 700, "二手集市发布门槛"},
	"threshold.contact_publish": {"发布联系方式", "threshold", 700, "公开联系方式门槛"},
	"threshold.observe_publish": {"观察台发帖", "threshold", 750, "校园文明观察台发帖门槛"},
	"threshold.observe_unmask":  {"观察台去码查看", "threshold", 800, "满足信用分并签署吃瓜不扩散协议后可查看观察帖原文"},
	"threshold.high_credit":     {"高信用用户", "threshold", 800, "高信用身份标签门槛"},
	"threshold.dm_unlimited":    {"私信不限量", "threshold", 850, "解除新用户私信频率限制"},
	"reward.team_check_in":      {"车队准时签到", "reward", 2, "每场车队首次有效签到奖励"},
	"reward.lost_claim":         {"失物成功认领", "reward", 5, "失主确认认领完成奖励"},
	"reward.feedback_accepted":  {"反馈被采纳", "reward", 5, "管理员采纳有效反馈奖励"},
	"penalty.team_late_leave":   {"临近发车退出", "penalty", -20, "发车前半小时内未请假退出扣分"},
}

func NormalizeEmail(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func CreditValue(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, key string) (int, error) {
	def, ok := CreditDefaults[key]
	if !ok {
		return 0, fmt.Errorf("未知信用规则 %s", key)
	}
	var value int
	err := q.QueryRow(ctx, "SELECT value FROM credit_rules WHERE key=$1", key).Scan(&value)
	if err == pgx.ErrNoRows {
		return def.Value, nil
	}
	return value, err
}

func EnqueueEmail(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, email, subject, body string) error {
	_, err := q.Exec(ctx, `INSERT INTO email_outbox(to_email,subject,body,status,attempts,next_attempt_at,last_error,created_at)
		VALUES($1,$2,$3,'pending',0,now(),'',now())`, email, subject, body)
	return err
}

func Notify(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, userID int64, title, body, link, kind string) error {
	_, err := q.Exec(ctx, `INSERT INTO notifications(user_id,type,title,body,link,created_at)
		VALUES($1,$2,$3,$4,$5,now())`, userID, kind, title, body, link)
	return err
}

func Audit(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, actorID *int64, action, targetType, targetID, reason string, before, after any, requestID string) error {
	encode := func(value any) string {
		if value == nil {
			return ""
		}
		data, _ := json.Marshal(value)
		return string(data)
	}
	_, err := q.Exec(ctx, `INSERT INTO audit_logs(actor_id,action,target_type,target_id,reason,before_json,after_json,request_id,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,now())`, actorID, action, targetType, targetID, reason, encode(before), encode(after), requestID)
	return err
}

func CheckRateLimit(ctx context.Context, tx pgx.Tx, action, subject string, limit, minutes int) error {
	var count int
	err := tx.QueryRow(ctx, `INSERT INTO rate_limit_counters(action,subject,window_start,count,expires_at)
		VALUES($1,$2,to_timestamp(floor(extract(epoch FROM now())/($3*60))*($3*60)),1,now()+($3*interval '1 minute')*2)
		ON CONFLICT(action,subject,window_start) DO UPDATE SET count=rate_limit_counters.count+1,expires_at=EXCLUDED.expires_at
		RETURNING count`, action, subject, minutes).Scan(&count)
	if err != nil {
		return err
	}
	if count > limit {
		return ErrRateLimited
	}
	return nil
}

var ErrRateLimited = fmt.Errorf("rate limited")

func UTC(value time.Time) time.Time { return value.UTC() }
