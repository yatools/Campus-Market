from __future__ import annotations

import io
from datetime import timedelta

from fastapi.testclient import TestClient
from PIL import Image
from sqlalchemy import func, select

from app.database import SessionLocal
from app.main import app
from app.models import (
    AnnouncementRead,
    ContentRevision,
    EmailOutbox,
    ModerationCase,
    SessionRecord,
    Setting,
    TeamRun,
    User,
    utcnow,
)
from app.security import hash_password

from .conftest import csrf, register


def make_admin() -> TestClient:
    with SessionLocal() as db:
        db.add(
            User(
                email="admin@test.edu.cn",
                password_hash=hash_password("AdminPassword123"),
                nickname="管理员",
                alias="梧桐#admin-gap",
                campus_identity="staff",
                role="admin",
                credit=1000,
            )
        )
        db.commit()
    admin = TestClient(app)
    response = admin.post(
        "/api/v1/auth/login",
        json={"email": "admin@test.edu.cn", "password": "AdminPassword123"},
    )
    assert response.status_code == 200
    return admin


def test_session_rotates_and_old_token_stops_working(client: TestClient) -> None:
    user = register(client, "rotate@test.edu.cn")
    old_token = client.cookies.get("wutong_session")
    with SessionLocal() as db:
        session = db.scalar(select(SessionRecord).where(SessionRecord.user_id == user["id"]))
        session.last_seen_at = utcnow() - timedelta(hours=25)
        db.commit()

    assert client.get("/api/v1/me").status_code == 200
    new_token = client.cookies.get("wutong_session")
    assert new_token and new_token != old_token

    stale = TestClient(app)
    stale.cookies.set("wutong_session", old_token or "")
    assert stale.get("/api/v1/me").status_code == 401
    stale.close()


def test_restricted_account_cannot_publish_but_can_remove_existing_content(client: TestClient) -> None:
    user = register(client, "restricted@test.edu.cn")
    post = client.post(
        "/api/v1/posts",
        headers=csrf(client),
        json={"body": "限权前发布的内容", "identity_mode": "nickname"},
    ).json()
    with SessionLocal() as db:
        db.get(User, user["id"]).status = "restricted"
        db.commit()

    denied = client.post(
        "/api/v1/posts",
        headers=csrf(client),
        json={"body": "限权后不能继续发布", "identity_mode": "nickname"},
    )
    assert denied.status_code == 403
    assert denied.json()["code"] == "ACCOUNT_RESTRICTED"
    assert client.delete(f"/api/v1/entities/{post['id']}", headers=csrf(client)).status_code == 200


def test_post_edit_history_and_configured_risk_rule(client: TestClient) -> None:
    register(client, "revision@test.edu.cn")
    post = client.post(
        "/api/v1/posts",
        headers=csrf(client),
        json={"title": "原始标题", "body": "原始正文", "identity_mode": "nickname"},
    ).json()
    edited = client.patch(
        f"/api/v1/posts/{post['id']}",
        headers=csrf(client),
        json={"title": "更新标题", "body": "更新正文"},
    )
    assert edited.status_code == 200
    history = client.get(f"/api/v1/entities/{post['id']}/revisions").json()
    assert history["total"] == 1
    assert history["items"][0]["title"] == "原始标题"
    with SessionLocal() as db:
        assert db.scalar(select(func.count(ContentRevision.id))) == 1
        db.add(Setting(key="risk_words", value='{"review":["自定义风险"]}'))
        db.commit()
    pending = client.post(
        "/api/v1/posts",
        headers=csrf(client),
        json={"body": "这里包含自定义风险词", "identity_mode": "nickname"},
    )
    assert pending.status_code == 201
    assert pending.json()["status"] == "pending"
    with SessionLocal() as db:
        assert db.scalar(select(func.count(ModerationCase.id)).where(ModerationCase.status == "pending")) == 1


def test_search_has_real_pagination_and_excludes_anonymous_posts(client: TestClient) -> None:
    register(client, "search@test.edu.cn")
    for index in range(3):
        assert (
            client.post(
                "/api/v1/posts",
                headers=csrf(client),
                json={"body": f"梧桐分页关键词 {index}", "identity_mode": "nickname"},
            ).status_code
            == 201
        )
    client.post(
        "/api/v1/posts",
        headers=csrf(client),
        json={"body": "梧桐分页关键词 匿名", "identity_mode": "anonymous"},
    )
    first = client.get("/api/v1/search", params={"q": "分页关键词", "page": 1, "page_size": 2}).json()
    second = client.get("/api/v1/search", params={"q": "分页关键词", "page": 2, "page_size": 2}).json()
    assert first["total"] == 3
    assert len(first["items"]) == 2
    assert len(second["items"]) == 1


def test_question_owner_cannot_accept_own_answer(client: TestClient) -> None:
    register(client, "self-answer@test.edu.cn")
    question = client.post(
        "/api/v1/questions",
        headers=csrf(client),
        json={"title": "能否采纳自己的回答？", "body": "不应通过自问自答获取经验"},
    ).json()
    answer = client.post(
        f"/api/v1/questions/{question['id']}/answers",
        headers=csrf(client),
        json={"body": "这是提问者自己的回答"},
    ).json()
    response = client.post(f"/api/v1/answers/{answer['id']}/accept", headers=csrf(client))
    assert response.status_code == 400
    assert response.json()["code"] == "SELF_ACCEPT_NOT_ALLOWED"


def test_activity_context_cannot_bypass_stranger_message_privacy(client: TestClient) -> None:
    organizer = register(client, "organizer@test.edu.cn", "发起人")
    activity = client.post(
        "/api/v1/activities",
        headers=csrf(client),
        json={
            "category": "学习",
            "title": "活动上下文权限测试",
            "location": "图书馆",
            "starts_at": (utcnow() + timedelta(days=1)).isoformat(),
        },
    ).json()
    sender = TestClient(app)
    register(sender, "activity-sender@test.edu.cn", "参与者")
    sender.put(f"/api/v1/activities/{activity['id']}/membership", headers=csrf(sender))
    stranger = TestClient(app)
    stranger_user = register(stranger, "activity-stranger@test.edu.cn", "无关用户")
    stranger.patch("/api/v1/me/privacy", headers=csrf(stranger), json={"dm_stranger_off": True})

    denied = sender.post(
        "/api/v1/conversations",
        headers=csrf(sender),
        json={
            "recipient_id": stranger_user["id"],
            "context_type": "activity",
            "context_id": activity["id"],
            "first_message": "伪造活动上下文",
        },
    )
    assert denied.status_code == 403
    allowed = sender.post(
        "/api/v1/conversations",
        headers=csrf(sender),
        json={
            "recipient_id": organizer["id"],
            "context_type": "activity",
            "context_id": activity["id"],
            "first_message": "联系活动发起人",
        },
    )
    assert allowed.status_code == 201
    sender.close()
    stranger.close()


def test_team_run_management_and_targeted_announcement_rules(client: TestClient) -> None:
    register(client, "team-runs@test.edu.cn", "车头")
    team = client.post(
        "/api/v1/teams",
        headers=csrf(client),
        json={
            "game": "Valorant",
            "mode": "场次管理",
            "starts_at": (utcnow() + timedelta(days=1)).isoformat(),
        },
    ).json()
    created = client.post(
        f"/api/v1/teams/{team['id']}/runs",
        headers=csrf(client),
        json={"starts_at": (utcnow() + timedelta(days=2)).isoformat()},
    )
    assert created.status_code == 201
    run_id = created.json()["id"]
    assert created.json()["member_count"] == 1
    assert client.get(f"/api/v1/teams/{team['id']}/runs").json()["total"] == 2
    cancelled = client.patch(
        f"/api/v1/teams/{team['id']}/runs/{run_id}",
        headers=csrf(client),
        json={"status": "cancelled"},
    )
    assert cancelled.json()["status"] == "cancelled"
    with SessionLocal() as db:
        assert db.get(TeamRun, run_id).status == "cancelled"

    admin = make_admin()
    announcement = admin.post(
        "/api/v1/admin/announcements",
        headers=csrf(admin),
        json={"title": "仅教职工强提醒", "body": "测试目标人群与邮件队列", "audience": "staff", "level": "strong"},
    )
    assert announcement.status_code == 201
    announcement_id = announcement.json()["id"]
    assert client.put(f"/api/v1/announcements/{announcement_id}/read", headers=csrf(client)).status_code == 404
    with SessionLocal() as db:
        assert db.scalar(select(func.count(AnnouncementRead.id))) == 0
        assert db.scalar(select(func.count(EmailOutbox.id)).where(EmailOutbox.status == "pending")) == 1
    admin.close()


def test_upload_rejects_mime_mismatch(client: TestClient) -> None:
    register(client, "mime@test.edu.cn")
    image = Image.new("RGB", (16, 16), color=(20, 30, 40))
    data = io.BytesIO()
    image.save(data, "JPEG")
    response = client.post(
        "/api/v1/uploads/images",
        headers=csrf(client),
        files={"file": ("fake.png", data.getvalue(), "image/png")},
    )
    assert response.status_code == 400
    assert response.json()["code"] == "IMAGE_MIME_MISMATCH"


def test_owned_module_content_can_be_edited_only_in_valid_states(client: TestClient) -> None:
    register(client, "module-edit@test.edu.cn")
    question = client.post(
        "/api/v1/questions",
        headers=csrf(client),
        json={"title": "原始问题标题", "body": "原始问题说明"},
    ).json()
    assert (
        client.patch(
            f"/api/v1/questions/{question['id']}",
            headers=csrf(client),
            json={"title": "更新后的问题标题"},
        ).json()["title"]
        == "更新后的问题标题"
    )
    article = client.post(
        "/api/v1/handbook",
        headers=csrf(client),
        json={"category": "办事", "title": "原始手册标题", "body": "这是一段长度足够的原始手册正文，用于验证编辑流程。", "draft": True},
    ).json()
    assert (
        client.patch(
            f"/api/v1/handbook/{article['id']}",
            headers=csrf(client),
            json={"body": "这是一段长度足够的更新手册正文，并且仍然保持草稿状态。"},
        ).status_code
        == 200
    )
    activity = client.post(
        "/api/v1/activities",
        headers=csrf(client),
        json={
            "category": "学习",
            "title": "原始活动标题",
            "location": "一号楼",
            "starts_at": (utcnow() + timedelta(days=2)).isoformat(),
            "capacity": 10,
        },
    ).json()
    assert (
        client.patch(
            f"/api/v1/activities/{activity['id']}",
            headers=csrf(client),
            json={"location": "二号楼", "capacity": 8},
        ).json()["location"]
        == "二号楼"
    )
    lost = client.post(
        "/api/v1/lost-items",
        headers=csrf(client),
        json={"kind": "lost", "item_name": "原始物品名", "location": "操场"},
    ).json()
    assert (
        client.patch(
            f"/api/v1/lost-items/{lost['id']}",
            headers=csrf(client),
            json={"item_name": "更新后的物品名"},
        ).json()["item_name"]
        == "更新后的物品名"
    )
