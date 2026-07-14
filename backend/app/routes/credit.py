from __future__ import annotations

from fastapi import APIRouter, Depends
from pydantic import BaseModel, Field, field_validator
from sqlalchemy.orm import Session

from ..credit import CREDIT_RULE_DEFAULTS, MAX_CREDIT, credit_rules_payload, ensure_credit_rules
from ..database import get_db
from ..deps import admin_user
from ..errors import APIError
from ..models import CreditRule, User
from ..services import audit

router = APIRouter(tags=["信用规则"])
admin_router = APIRouter(prefix="/admin", tags=["管理后台"])


class CreditRuleChange(BaseModel):
    key: str = Field(min_length=1, max_length=80)
    value: int = Field(ge=-MAX_CREDIT, le=MAX_CREDIT)


class CreditRulesUpdate(BaseModel):
    rules: list[CreditRuleChange] = Field(min_length=1, max_length=50)

    @field_validator("rules")
    @classmethod
    def unique_keys(cls, values: list[CreditRuleChange]) -> list[CreditRuleChange]:
        if len({item.key for item in values}) != len(values):
            raise ValueError("信用规则键不能重复")
        return values


@router.get("/credit-rules")
def public_credit_rules(db: Session = Depends(get_db)) -> dict:
    return credit_rules_payload(db)


@admin_router.get("/credit-rules")
def admin_credit_rules(_: User = Depends(admin_user), db: Session = Depends(get_db)) -> dict:
    return credit_rules_payload(db)


@admin_router.patch("/credit-rules")
def update_credit_rules(
    data: CreditRulesUpdate,
    admin: User = Depends(admin_user),
    db: Session = Depends(get_db),
) -> dict:
    ensure_credit_rules(db)
    before: dict[str, int] = {}
    after: dict[str, int] = {}
    for item in data.rules:
        default = CREDIT_RULE_DEFAULTS.get(item.key)
        if not default:
            raise APIError(400, "CREDIT_RULE_UNKNOWN", f"不支持的信用规则：{item.key}")
        if default.kind in {"baseline", "threshold", "reward"} and item.value < 0:
            raise APIError(400, "CREDIT_RULE_SIGN", f"{default.label}不能设置为负数")
        if default.kind == "penalty" and item.value > 0:
            raise APIError(400, "CREDIT_RULE_SIGN", f"{default.label}不能设置为正数")
        row = db.get(CreditRule, item.key)
        if not row:
            raise APIError(500, "CREDIT_RULE_MISSING", "信用规则初始化失败")
        before[item.key] = row.value
        row.value = item.value
        row.updated_by = admin.id
        after[item.key] = item.value
    audit(db, admin.id, "credit_rules.update", "credit_rules", "global", before=before, after=after)
    return credit_rules_payload(db)
