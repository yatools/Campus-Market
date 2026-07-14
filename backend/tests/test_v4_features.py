from __future__ import annotations

import io
from datetime import timedelta

from fastapi.testclient import TestClient
from PIL import Image

from app.database import SessionLocal
from app.main import app
from app.models import User, utcnow
from app.security import hash_password

from .conftest import csrf, register


def admin_client() -> TestClient:
    with SessionLocal() as db:
        db.add(User(email="v4-admin@test.edu.cn", password_hash=hash_password("AdminPassword123"), nickname="管理员", alias="梧桐#v4admin", campus_identity="staff", role="admin", credit=1000))
        db.commit()
    result = TestClient(app)
    assert result.post("/api/v1/auth/login", json={"email": "v4-admin@test.edu.cn", "password": "AdminPassword123"}).status_code == 200
    return result


def image_bytes() -> bytes:
    buffer = io.BytesIO()
    Image.new("RGB", (20, 20), "green").save(buffer, format="PNG")
    return buffer.getvalue()


def test_feed_reports_meaningful_comment_updates_and_comment_attachments(client: TestClient) -> None:
    register(client, "feed-v4@test.edu.cn", "动态同学")
    post = client.post(
        "/api/v1/posts", headers=csrf(client),
        json={"title": "动态测试", "body": "初始内容", "identity_mode": "nickname", "visibility": "forever"},
    ).json()
    initial = client.get("/api/v1/feed").json()
    assert initial["items"][0]["title"] == "动态测试"
    upload = client.post(
        "/api/v1/uploads/images", headers=csrf(client), files={"file": ("reply.png", image_bytes(), "image/png")},
    ).json()
    response = client.post(
        f"/api/v1/entities/{post['id']}/comments", headers=csrf(client),
        json={"body": f"带图回复\n![图片]({upload['url']})", "attachment_ids": [upload["id"]]},
    )
    assert response.status_code == 201, response.text
    assert response.json()["attachments"][0]["id"] == upload["id"]
    changes = client.get("/api/v1/feed/changes", params={"after": initial["watermark"]}).json()
    assert changes["count"] == 1


def test_v4_team_catalog_fields_submission_merge_and_calendar(client: TestClient) -> None:
    user = register(client, "team-v4@test.edu.cn", "车头")
    games = client.get("/api/v1/team-games").json()["items"]
    valo = next(game for game in games if game["name"] == "无畏契约")
    team = client.post(
        "/api/v1/teams", headers=csrf(client),
        json={
            "game_id": valo["id"], "mode": "竞技排位", "rank_requirement": "黄金~铂金", "capacity": 5,
            "starts_at": (utcnow() + timedelta(hours=2)).isoformat(), "newbie_level": "欢迎新手，带练",
            "vibe": "娱乐上分两不误", "reminder_channels": ["email", "in_app", "calendar"],
        },
    )
    assert team.status_code == 201, team.text
    payload = team.json()
    assert payload["game"] == "无畏契约"
    assert payload["vibe"] == "娱乐上分两不误"
    assert payload["owner"]["credit"] == 800
    assert payload["owner"]["verified"] is True
    assert payload["completion_rate"] is None
    assert payload["rating_tags"] == {}
    assert client.get(f"/api/v1/teams/{payload['id']}/calendar.ics").status_code == 200

    submission = client.post(
        "/api/v1/game-submissions", headers=csrf(client),
        json={"name": "VALORANT Mobile", "aliases": ["瓦手游"]},
    ).json()
    admin = admin_client()
    decision = admin.post(
        f"/api/v1/admin/game-submissions/{submission['id']}/decision", headers=csrf(admin),
        json={"action": "merge", "target_game_id": valo["id"], "admin_note": "归入无畏契约目录"},
    )
    admin.close()
    assert decision.status_code == 200, decision.text
    assert decision.json()["status"] == "merged"
    with SessionLocal() as db:
        assert db.get(User, user["id"]).id == user["id"]


def test_listing_remains_offline_only_with_v4_details(client: TestClient) -> None:
    user = register(client, "listing-v4@test.edu.cn", "卖家")
    with SessionLocal() as db:
        db.get(User, user["id"]).credit = 800
        db.commit()
    listing = client.post(
        "/api/v1/listings", headers=csrf(client),
        json={
            "category": "数码", "title": "九成新机械键盘", "description": "右下角有轻微使用痕迹",
            "price": 199, "condition": "九成新", "negotiable": True, "purchased_at": "2025-09-01",
            "location": "东门快递站面交",
        },
    )
    assert listing.status_code == 201, listing.text
    assert listing.json()["trade_mode"] == "offline_only"
    assert listing.json()["negotiable"] is True
    assert listing.json()["purchased_at"] == "2025-09-01"
    assert listing.json()["seller"]["verified"] is True
    assert listing.json()["seller"]["completed_sales"] == 0


def test_real_campus_service_ratings_enforce_cooldown_and_allow_response(client: TestClient) -> None:
    register(client, "service-rater@test.edu.cn", "评分同学")
    admin = admin_client()
    service = admin.post(
        "/api/v1/admin/campus-services",
        headers=csrf(admin),
        json={"name": "文汇打印", "category": "打印/维修/快递"},
    )
    assert service.status_code == 201, service.text
    service_id = service.json()["id"]

    missing_reason = client.post(
        f"/api/v1/campus-services/{service_id}/ratings",
        headers=csrf(client),
        json={"rating": 2, "body": "太慢"},
    )
    assert missing_reason.status_code == 400
    rating = client.post(
        f"/api/v1/campus-services/{service_id}/ratings",
        headers=csrf(client),
        json={"rating": 2, "body": "晚间排队超过四十分钟，取件提示也不清楚。"},
    )
    assert rating.status_code == 201, rating.text
    cooldown = client.post(
        f"/api/v1/campus-services/{service_id}/ratings",
        headers=csrf(client),
        json={"rating": 5, "body": "补充评价"},
    )
    assert cooldown.status_code == 409
    response = admin.post(
        f"/api/v1/campus-service-ratings/{rating.json()['id']}/response",
        headers=csrf(admin),
        json={"body": "已增加晚间值班人员，并优化取件提示。"},
    )
    assert response.status_code == 200, response.text
    assert response.json()["response"].startswith("已增加")
    detail = client.get(f"/api/v1/campus-services/{service_id}").json()
    assert detail["score"] == 2.0
    assert detail["rating_count"] == 1
    assert detail["ratings"][0]["response"]
    admin.close()
