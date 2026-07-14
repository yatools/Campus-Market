from __future__ import annotations

from dataclasses import dataclass

from sqlalchemy.orm import Session

from .errors import APIError
from .models import CreditRule, User

MAX_CREDIT = 1000


@dataclass(frozen=True)
class CreditRuleDefault:
    label: str
    kind: str
    value: int
    description: str


CREDIT_RULE_DEFAULTS: dict[str, CreditRuleDefault] = {
    "baseline.initial_credit": CreditRuleDefault("新用户初始信用", "baseline", 800, "仅影响新注册用户"),
    "threshold.anonymous_post": CreditRuleDefault("完全匿名发帖", "threshold", 600, "树洞完全匿名发帖门槛"),
    "threshold.team_create": CreditRuleDefault("创建游戏车队", "threshold", 600, "发布开车门槛"),
    "threshold.course_review": CreditRuleDefault("评价课程", "threshold", 600, "提交课程评价门槛"),
    "threshold.listing_publish": CreditRuleDefault("发布交易帖", "threshold", 700, "二手集市发布门槛"),
    "threshold.contact_publish": CreditRuleDefault("发布联系方式", "threshold", 700, "公开联系方式门槛"),
    "threshold.observe_publish": CreditRuleDefault("观察台发帖", "threshold", 750, "校园文明观察台发帖门槛"),
    "threshold.high_credit": CreditRuleDefault("高信用用户", "threshold", 800, "高信用身份标签门槛"),
    "threshold.dm_unlimited": CreditRuleDefault("私信不限量", "threshold", 850, "解除新用户私信频率限制"),
    "reward.team_check_in": CreditRuleDefault("车队准时签到", "reward", 2, "每场车队首次有效签到奖励"),
    "reward.lost_claim": CreditRuleDefault("失物成功认领", "reward", 5, "失主确认认领完成奖励"),
    "reward.feedback_accepted": CreditRuleDefault("反馈被采纳", "reward", 5, "管理员采纳有效反馈奖励"),
    "penalty.team_late_leave": CreditRuleDefault("临近发车退出", "penalty", -20, "发车前半小时内未请假退出扣分"),
}


def ensure_credit_rules(db: Session) -> None:
    existing = {row.key for row in db.query(CreditRule.key).all()}
    for key, default in CREDIT_RULE_DEFAULTS.items():
        if key in existing:
            continue
        db.add(
            CreditRule(
                key=key,
                label=default.label,
                kind=default.kind,
                value=default.value,
                description=default.description,
            )
        )
    db.flush()


def credit_value(db: Session, key: str) -> int:
    default = CREDIT_RULE_DEFAULTS.get(key)
    if not default:
        raise KeyError(key)
    row = db.get(CreditRule, key)
    return row.value if row else default.value


def require_credit(db: Session, user: User, key: str, action: str) -> None:
    threshold = credit_value(db, key)
    if user.credit < threshold:
        raise APIError(403, "CREDIT_REQUIRED", f"{action}需要信用分不低于 {threshold}")


def apply_credit_rule(
    db: Session,
    user: User,
    key: str,
    *,
    actor_id: int | None = None,
    target_type: str = "user",
    target_id: int | str | None = None,
) -> int:
    delta = credit_value(db, key)
    before = user.credit
    user.credit = max(0, min(MAX_CREDIT, user.credit + delta))
    applied = user.credit - before
    if applied:
        from .services import audit

        audit(
            db,
            actor_id if actor_id is not None else user.id,
            f"credit.{key}",
            target_type,
            target_id if target_id is not None else user.id,
            before={"credit": before},
            after={"credit": user.credit, "delta": applied, "rule": key},
        )
    return applied


def credit_rules_payload(db: Session) -> dict:
    ensure_credit_rules(db)
    rows = db.query(CreditRule).order_by(CreditRule.kind, CreditRule.key).all()
    return {
        "max_score": MAX_CREDIT,
        "initial_score": credit_value(db, "baseline.initial_credit"),
        "values": {row.key: row.value for row in rows},
        "rules": [
            {
                "key": row.key,
                "label": row.label,
                "kind": row.kind,
                "value": row.value,
                "description": row.description,
                "updated_at": row.updated_at,
            }
            for row in rows
        ],
    }
