from __future__ import annotations

from datetime import timedelta

from fastapi import APIRouter, Depends, Query
from pydantic import BaseModel, Field, field_validator
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from ..database import get_db
from ..deps import admin_user, current_user, optional_user, participating_user
from ..errors import APIError
from ..models import CampusService, CampusServiceRating, User, utcnow
from ..services import audit, notify

router = APIRouter(tags=["校园服务评分"])
admin_router = APIRouter(prefix="/admin", tags=["管理后台"])


class CampusServiceCreate(BaseModel):
    name: str = Field(min_length=2, max_length=160)
    category: str = Field(default="校园服务", min_length=1, max_length=60)
    manager_user_id: int | None = None


class CampusServiceUpdate(BaseModel):
    name: str | None = Field(default=None, min_length=2, max_length=160)
    category: str | None = Field(default=None, min_length=1, max_length=60)
    manager_user_id: int | None = None
    active: bool | None = None


class CampusServiceRatingCreate(BaseModel):
    rating: int = Field(ge=1, le=5)
    body: str = Field(default="", max_length=2000)

    @field_validator("body")
    @classmethod
    def clean_body(cls, value: str) -> str:
        return value.strip()


class CampusServiceResponseCreate(BaseModel):
    body: str = Field(min_length=2, max_length=2000)


def rating_payload(db: Session, row: CampusServiceRating) -> dict:
    user = db.get(User, row.user_id)
    responder = db.get(User, row.responder_id) if row.responder_id else None
    return {
        "id": row.id,
        "rating": row.rating,
        "body": row.body,
        "author": user.nickname if user else "已注销用户",
        "response": row.response,
        "responder": responder.nickname if responder else "",
        "created_at": row.created_at,
        "responded_at": row.responded_at,
    }


def service_payload(db: Session, row: CampusService, viewer: User | None, include_ratings: bool = False) -> dict:
    count, average = db.execute(
        select(func.count(CampusServiceRating.id), func.avg(CampusServiceRating.rating)).where(
            CampusServiceRating.service_id == row.id
        )
    ).one()
    latest_mine = None
    if viewer:
        latest_mine = db.scalar(
            select(CampusServiceRating)
            .where(CampusServiceRating.service_id == row.id, CampusServiceRating.user_id == viewer.id)
            .order_by(CampusServiceRating.created_at.desc())
        )
    payload = {
        "id": row.id,
        "name": row.name,
        "category": row.category,
        "score": round(float(average), 1) if average is not None else None,
        "rating_count": int(count or 0),
        "managed_by_me": bool(viewer and (row.manager_user_id == viewer.id or viewer.role in {"moderator", "admin"})),
        "next_rating_at": latest_mine.created_at + timedelta(days=30) if latest_mine else None,
    }
    if include_ratings:
        ratings = db.scalars(
            select(CampusServiceRating)
            .where(CampusServiceRating.service_id == row.id)
            .order_by(CampusServiceRating.created_at.desc())
            .limit(50)
        ).all()
        payload["ratings"] = [rating_payload(db, item) for item in ratings]
    return payload


@router.get("/campus-services")
def list_campus_services(
    category: str = Query("", max_length=60),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    filters = [CampusService.active.is_(True)]
    if category:
        filters.append(CampusService.category == category)
    rows = db.scalars(select(CampusService).where(*filters).order_by(CampusService.name)).all()
    return {"items": [service_payload(db, row, viewer) for row in rows], "total": len(rows)}


@router.get("/campus-services/{service_id}")
def get_campus_service(
    service_id: int,
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    row = db.get(CampusService, service_id)
    if not row or not row.active:
        raise APIError(404, "CAMPUS_SERVICE_NOT_FOUND", "校园服务不存在")
    return service_payload(db, row, viewer, include_ratings=True)


@router.post("/campus-services/{service_id}/ratings", status_code=201)
def rate_campus_service(
    service_id: int,
    data: CampusServiceRatingCreate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    service = db.get(CampusService, service_id)
    if not service or not service.active:
        raise APIError(404, "CAMPUS_SERVICE_NOT_FOUND", "校园服务不存在")
    if data.rating <= 2 and len(data.body) < 10:
        raise APIError(400, "LOW_RATING_REASON_REQUIRED", "低分评价请填写至少 10 个字的具体事由")
    latest = db.scalar(
        select(CampusServiceRating)
        .where(CampusServiceRating.service_id == service_id, CampusServiceRating.user_id == user.id)
        .order_by(CampusServiceRating.created_at.desc())
    )
    if latest and latest.created_at > utcnow() - timedelta(days=30):
        raise APIError(409, "SERVICE_RATING_COOLDOWN", "同一服务 30 天内只能评价一次")
    row = CampusServiceRating(service_id=service_id, user_id=user.id, rating=data.rating, body=data.body)
    db.add(row)
    db.flush()
    audit(db, user.id, "campus_service.rate", "campus_service", service_id, after={"rating": data.rating})
    return rating_payload(db, row)


@router.post("/campus-service-ratings/{rating_id}/response")
def respond_to_rating(
    rating_id: int,
    data: CampusServiceResponseCreate,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    row = db.get(CampusServiceRating, rating_id)
    service = db.get(CampusService, row.service_id) if row else None
    if not row or not service:
        raise APIError(404, "SERVICE_RATING_NOT_FOUND", "服务评价不存在")
    if service.manager_user_id != user.id and user.role not in {"moderator", "admin"}:
        raise APIError(403, "SERVICE_MANAGER_REQUIRED", "只有服务管理者或管理员可以回应")
    if row.response:
        raise APIError(409, "SERVICE_RATING_RESPONDED", "该评价已经回应")
    row.response = data.body.strip()
    row.responder_id = user.id
    row.responded_at = utcnow()
    notify(db, row.user_id, "校园服务评价收到回应", f"“{service.name}”回应了你的评价", f"/explore/handbook?service={service.id}")
    audit(db, user.id, "campus_service.respond", "campus_service_rating", row.id)
    return rating_payload(db, row)


@admin_router.post("/campus-services", status_code=201)
def create_campus_service(
    data: CampusServiceCreate,
    admin: User = Depends(admin_user),
    db: Session = Depends(get_db),
) -> dict:
    name = data.name.strip()
    if db.scalar(select(CampusService.id).where(CampusService.name == name)):
        raise APIError(409, "CAMPUS_SERVICE_EXISTS", "该校园服务已存在")
    if data.manager_user_id and not db.get(User, data.manager_user_id):
        raise APIError(404, "USER_NOT_FOUND", "服务管理者不存在")
    row = CampusService(name=name, category=data.category.strip(), manager_user_id=data.manager_user_id)
    db.add(row)
    db.flush()
    audit(db, admin.id, "campus_service.create", "campus_service", row.id)
    return service_payload(db, row, admin)


@admin_router.patch("/campus-services/{service_id}")
def update_campus_service(
    service_id: int,
    data: CampusServiceUpdate,
    admin: User = Depends(admin_user),
    db: Session = Depends(get_db),
) -> dict:
    row = db.get(CampusService, service_id)
    if not row:
        raise APIError(404, "CAMPUS_SERVICE_NOT_FOUND", "校园服务不存在")
    values = data.model_dump(exclude_unset=True)
    if "manager_user_id" in values and values["manager_user_id"] and not db.get(User, values["manager_user_id"]):
        raise APIError(404, "USER_NOT_FOUND", "服务管理者不存在")
    for key, value in values.items():
        if isinstance(value, str):
            value = value.strip()
        setattr(row, key, value)
    audit(db, admin.id, "campus_service.update", "campus_service", row.id, after=values)
    return service_payload(db, row, admin)
