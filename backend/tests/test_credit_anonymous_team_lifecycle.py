from __future__ import annotations

from datetime import timedelta

from fastapi.testclient import TestClient

from app.database import SessionLocal
from app.main import app
from app.models import ContentEntity, Team, TeamRun, ThreadAnonymousIdentity, User, utcnow
from app.security import hash_password

from .conftest import csrf, register


def login_admin() -> TestClient:
    with SessionLocal() as db:
        db.add(
            User(
                email="rules-admin@test.edu.cn",
                password_hash=hash_password("AdminPassword123"),
                nickname="规则管理员",
                alias="规则管理员马甲",
                campus_identity="staff",
                role="admin",
                credit=1000,
            )
        )
        db.commit()
    client = TestClient(app)
    response = client.post(
        "/api/v1/auth/login",
        json={"email": "rules-admin@test.edu.cn", "password": "AdminPassword123"},
    )
    assert response.status_code == 200
    return client


def test_credit_rules_are_editable_and_profile_alias_is_validated(client: TestClient) -> None:
    user = register(client, "credit-user@test.edu.cn", "信用同学")
    assert user["credit"] == 800
    rules = client.get("/api/v1/credit-rules").json()
    assert rules["max_score"] == 1000
    assert rules["values"]["threshold.team_create"] == 600

    profile = client.patch(
        "/api/v1/me/profile",
        headers=csrf(client),
        json={"alias": "月下小狐狸"},
    )
    assert profile.status_code == 200
    assert profile.json()["alias"] == "月下小狐狸"

    admin = login_admin()
    changed = admin.patch(
        "/api/v1/admin/credit-rules",
        headers=csrf(admin),
        json={"rules": [{"key": "threshold.team_create", "value": 900}]},
    )
    assert changed.status_code == 200, changed.text
    assert changed.json()["values"]["threshold.team_create"] == 900
    invalid = admin.patch(
        "/api/v1/admin/credit-rules",
        headers=csrf(admin),
        json={"rules": [{"key": "penalty.team_late_leave", "value": 5}]},
    )
    assert invalid.status_code == 400
    admin.close()


def test_anonymous_pool_is_stable_within_a_thread_and_admin_changes_are_not_retroactive(
    client: TestClient,
) -> None:
    admin = login_admin()
    pool = admin.put(
        "/api/v1/admin/settings/anonymous_nickname_pool",
        headers=csrf(admin),
        json={"value": "丹阳子\n小狐狸\n丹阳子\n\n"},
    )
    assert pool.status_code == 200, pool.text
    assert pool.json()["value"] == "丹阳子\n小狐狸"

    first = register(client, "anon-first@test.edu.cn", "匿名甲")
    post = client.post(
        "/api/v1/posts",
        headers=csrf(client),
        json={"body": "匿名昵称稳定性测试", "identity_mode": "anonymous", "visibility": "forever"},
    )
    assert post.status_code == 201, post.text
    author = post.json()["author"]
    assert author in {"丹阳子", "小狐狸"}
    reply = client.post(
        f"/api/v1/entities/{post.json()['id']}/comments",
        headers=csrf(client),
        json={"body": "同一个人的匿名回复", "identity_mode": "anonymous"},
    )
    assert reply.status_code == 201, reply.text
    assert reply.json()["author"] == author

    other = TestClient(app)
    register(other, "anon-other@test.edu.cn", "匿名乙")
    other_reply = other.post(
        f"/api/v1/entities/{post.json()['id']}/comments",
        headers=csrf(other),
        json={"body": "另一个人的匿名回复", "identity_mode": "anonymous"},
    )
    assert other_reply.status_code == 201, other_reply.text
    assert other_reply.json()["author"] != author

    admin.put(
        "/api/v1/admin/settings/anonymous_nickname_pool",
        headers=csrf(admin),
        json={"value": "欧阳牛马\n橘子汽水"},
    )
    assert client.get(f"/api/v1/posts/{post.json()['id']}").json()["author"] == author
    with SessionLocal() as db:
        identities = db.query(ThreadAnonymousIdentity).filter_by(thread_id=post.json()["id"]).all()
        assert len(identities) == 2
        assert first["id"] in {row.user_id for row in identities}
    other.close()
    admin.close()


def test_team_expiry_is_enforced_without_worker_and_history_remains_available(client: TestClient) -> None:
    register(client, "expiry-owner@test.edu.cn", "过期车头")
    game = client.get("/api/v1/team-games").json()["items"][0]
    past = client.post(
        "/api/v1/teams",
        headers=csrf(client),
        json={
            "game_id": game["id"],
            "mode": "过去时间",
            "capacity": 3,
            "starts_at": (utcnow() - timedelta(minutes=1)).isoformat(),
        },
    )
    assert past.status_code == 400

    created = client.post(
        "/api/v1/teams",
        headers=csrf(client),
        json={
            "game_id": game["id"],
            "mode": "到期测试",
            "capacity": 3,
            "starts_at": (utcnow() + timedelta(hours=1)).isoformat(),
            "post_departure_retention_minutes": 60,
        },
    )
    assert created.status_code == 201, created.text
    team_id = created.json()["id"]
    assert created.json()["post_departure_retention_minutes"] == 60
    with SessionLocal() as db:
        run = db.query(TeamRun).filter_by(team_id=team_id).one()
        run.starts_at = utcnow() - timedelta(hours=2)
        run.expires_at = utcnow() - timedelta(hours=1)
        db.commit()

    listing = client.get("/api/v1/teams?page_size=50")
    assert listing.status_code == 200
    assert team_id not in {item["id"] for item in listing.json()["items"]}
    with SessionLocal() as db:
        assert db.get(Team, team_id).status == "archived"
        assert db.get(ContentEntity, team_id).status == "expired"
    history = client.get(f"/api/v1/teams/{team_id}")
    assert history.status_code == 200
    assert history.json()["content_status"] == "expired"
