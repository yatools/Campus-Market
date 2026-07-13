from __future__ import annotations

from datetime import timedelta

from fastapi.testclient import TestClient

from app.database import SessionLocal
from app.models import ContentEntity, ObservePost, utcnow

from .conftest import csrf, register


def new_client() -> TestClient:
    from app.main import app

    return TestClient(app)


def test_non_member_cannot_check_in(client: TestClient) -> None:
    register(client, "owner@test.edu.cn", "车头")
    team = client.post(
        "/api/v1/teams",
        headers=csrf(client),
        json={
            "game": "LOL",
            "mode": "测试车队",
            "capacity": 5,
            "starts_at": (utcnow() + timedelta(minutes=20)).isoformat(),
        },
    ).json()
    outsider = new_client()
    register(outsider, "outsider@test.edu.cn", "路人")
    response = outsider.post(
        f"/api/v1/teams/{team['id']}/runs/{team['next_run']['id']}/check-in",
        headers=csrf(outsider),
    )
    outsider.close()
    assert response.status_code == 403
    assert response.json()["code"] == "RUN_MEMBER_REQUIRED"


def test_member_can_rejoin_same_run_after_leaving(client: TestClient) -> None:
    register(client, "rejoin-owner@test.edu.cn", "车头")
    team = client.post(
        "/api/v1/teams",
        headers=csrf(client),
        json={
            "game": "CS2",
            "mode": "重新上车测试",
            "capacity": 5,
            "starts_at": (utcnow() + timedelta(minutes=20)).isoformat(),
        },
    ).json()
    member = new_client()
    register(member, "rejoin-member@test.edu.cn", "队员")
    assert member.post(f"/api/v1/teams/{team['id']}/join", headers=csrf(member)).status_code == 200
    assert member.post(f"/api/v1/teams/{team['id']}/leave", headers=csrf(member)).status_code == 200
    assert member.post(f"/api/v1/teams/{team['id']}/join", headers=csrf(member)).status_code == 200
    checkin = member.post(
        f"/api/v1/teams/{team['id']}/runs/{team['next_run']['id']}/check-in",
        headers=csrf(member),
    )
    member.close()
    assert checkin.status_code == 200
    assert checkin.json()["credit_delta"] == 1


def test_activity_join_is_idempotent(client: TestClient) -> None:
    register(client, "activity@test.edu.cn")
    activity = client.post(
        "/api/v1/activities",
        headers=csrf(client),
        json={
            "category": "运动",
            "title": "周末一起打羽毛球",
            "location": "体育馆",
            "starts_at": (utcnow() + timedelta(days=1)).isoformat(),
            "capacity": 10,
        },
    ).json()
    client.put(f"/api/v1/activities/{activity['id']}/membership", headers=csrf(client))
    response = client.put(f"/api/v1/activities/{activity['id']}/membership", headers=csrf(client))
    assert response.status_code == 200
    assert response.json()["member_count"] == 1


def test_only_designated_respondent_can_respond(client: TestClient) -> None:
    owner = register(client, "observe-owner@test.edu.cn")
    with SessionLocal() as db:
        entity = ContentEntity(owner_id=owner["id"], type="observe", status="published", search_visible=False)
        db.add(entity)
        db.flush()
        db.add(ObservePost(entity_id=entity.id, title="测试观察帖", body_masked="已打码内容", body_raw="原始内容"))
        db.commit()
        observe_id = entity.id
    response = client.post(
        f"/api/v1/observe-posts/{observe_id}/response",
        headers=csrf(client),
        json={"body": "任意用户不能覆盖回应"},
    )
    assert response.status_code == 403
    assert response.json()["code"] == "RESPONDENT_REQUIRED"
