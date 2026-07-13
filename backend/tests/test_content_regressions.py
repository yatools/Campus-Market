from __future__ import annotations

from fastapi.testclient import TestClient

from app.database import SessionLocal
from app.main import app
from app.models import User

from .conftest import csrf, register


def create_post(client: TestClient) -> int:
    response = client.post(
        "/api/v1/posts",
        headers=csrf(client),
        json={"body": "用于测试回帖完整性的公开帖子", "identity_mode": "nickname"},
    )
    assert response.status_code == 201, response.text
    return response.json()["id"]


def test_comment_target_must_exist(client: TestClient) -> None:
    register(client, "comments@test.edu.cn")
    response = client.post(
        "/api/v1/entities/999999/comments",
        headers=csrf(client),
        json={"body": "不能成为孤儿回帖", "identity_mode": "nickname"},
    )
    assert response.status_code == 404
    assert response.json()["code"] == "CONTENT_NOT_FOUND"


def test_two_level_comments_keep_parent_relation(client: TestClient) -> None:
    register(client, "thread@test.edu.cn")
    post_id = create_post(client)
    root = client.post(
        f"/api/v1/entities/{post_id}/comments",
        headers=csrf(client),
        json={"body": "主评论", "identity_mode": "nickname"},
    ).json()
    child = client.post(
        f"/api/v1/entities/{post_id}/comments",
        headers=csrf(client),
        json={"body": "二级回复", "parent_id": root["id"], "identity_mode": "nickname"},
    )
    assert child.status_code == 201
    result = client.get(f"/api/v1/entities/{post_id}/comments").json()
    assert result["total"] == 1
    assert result["items"][0]["replies"][0]["body"] == "二级回复"


def test_reaction_is_idempotent(client: TestClient) -> None:
    register(client, "likes@test.edu.cn")
    post_id = create_post(client)
    first = client.put(f"/api/v1/entities/{post_id}/reactions/like", headers=csrf(client))
    second = client.put(f"/api/v1/entities/{post_id}/reactions/like", headers=csrf(client))
    assert first.json()["count"] == 1
    assert second.json()["count"] == 1


def test_nested_reply_targets_the_actual_child_author(client: TestClient) -> None:
    owner = register(client, "post-owner@test.edu.cn", "楼主")
    post_id = create_post(client)
    root_client = TestClient(app)
    root_user = register(root_client, "root-author@test.edu.cn", "一楼")
    root = root_client.post(
        f"/api/v1/entities/{post_id}/comments",
        headers=csrf(root_client),
        json={"body": "主评论", "identity_mode": "nickname"},
    ).json()
    child_client = TestClient(app)
    child_user = register(child_client, "child-author@test.edu.cn", "二楼")
    child = child_client.post(
        f"/api/v1/entities/{post_id}/comments",
        headers=csrf(child_client),
        json={"body": "回复一楼", "parent_id": root["id"], "identity_mode": "nickname"},
    ).json()
    nested = client.post(
        f"/api/v1/entities/{post_id}/comments",
        headers=csrf(client),
        json={"body": "楼主回复二楼", "parent_id": child["id"], "identity_mode": "nickname"},
    )
    root_client.close()
    child_client.close()
    assert nested.status_code == 201
    assert nested.json()["parent_id"] == root["id"]
    assert nested.json()["reply_to_user_id"] == child_user["id"]
    assert nested.json()["reply_to_user_id"] not in {owner["id"], root_user["id"]}


def test_unsettled_question_bounty_is_refunded_when_deleted(client: TestClient) -> None:
    user = register(client, "bounty@test.edu.cn", "提问者")
    with SessionLocal() as db:
        row = db.get(User, user["id"])
        row.xp = 100
        db.commit()
    question = client.post(
        "/api/v1/questions",
        headers=csrf(client),
        json={"title": "如何测试悬赏退款？", "body": "删除未采纳问题后应退回经验", "bounty_xp": 40},
    ).json()
    assert client.get("/api/v1/me").json()["xp"] == 60
    assert client.delete(f"/api/v1/entities/{question['id']}", headers=csrf(client)).status_code == 200
    assert client.get("/api/v1/me").json()["xp"] == 100
