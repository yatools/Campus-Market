from __future__ import annotations

import io

from fastapi.testclient import TestClient
from PIL import Image

from app.database import SessionLocal
from app.main import app
from app.models import User
from app.security import hash_password

from .conftest import csrf, register


def make_admin() -> TestClient:
    with SessionLocal() as db:
        db.add(
            User(
                email="admin@test.edu.cn",
                password_hash=hash_password("AdminPassword123"),
                nickname="管理员",
                alias="梧桐#admin",
                campus_identity="staff",
                role="admin",
                credit=100,
            )
        )
        db.commit()
    client = TestClient(app)
    response = client.post(
        "/api/v1/auth/login",
        json={"email": "admin@test.edu.cn", "password": "AdminPassword123"},
    )
    assert response.status_code == 200, response.text
    return client


def test_knowledge_market_and_course_workflows(client: TestClient) -> None:
    asker = register(client, "knowledge@test.edu.cn", "提问者")
    with SessionLocal() as db:
        db.get(User, asker["id"]).xp = 100
        db.commit()
    question = client.post(
        "/api/v1/questions",
        headers=csrf(client),
        json={"title": "图书馆周末几点关门？", "body": "需要本学期准确时间", "bounty_xp": 30},
    ).json()
    answer_client = TestClient(app)
    register(answer_client, "answerer@test.edu.cn", "答主")
    answer = answer_client.post(
        f"/api/v1/questions/{question['id']}/answers",
        headers=csrf(answer_client),
        json={"body": "周末闭馆时间以图书馆官网当日公告为准。"},
    ).json()
    accepted = client.post(f"/api/v1/answers/{answer['id']}/accept", headers=csrf(client))
    assert accepted.json()["awarded_xp"] == 50
    assert answer_client.get("/api/v1/me").json()["xp"] == 50

    article = client.post(
        "/api/v1/handbook",
        headers=csrf(client),
        json={
            "category": "新生入学指南",
            "title": "新生办理校园卡完整流程",
            "body": "准备录取材料和个人证件，前往服务大厅按现场指引办理。",
            "draft": True,
        },
    ).json()
    assert article["status"] == "draft"
    assert (
        client.post(f"/api/v1/handbook/{article['id']}/publish", headers=csrf(client)).json()["status"] == "published"
    )
    assert (
        answer_client.put(f"/api/v1/entities/{article['id']}/favorite", headers=csrf(answer_client)).status_code == 200
    )

    listing = client.post(
        "/api/v1/listings",
        headers=csrf(client),
        json={
            "category": "书籍",
            "title": "线性代数教材",
            "description": "正版教材，有少量笔记，只在校内线下面交。",
            "price": 20,
            "condition": "八成新",
            "location": "图书馆门口",
        },
    ).json()
    assert listing["trade_mode"] == "offline_only"
    assert (
        client.patch(
            f"/api/v1/listings/{listing['id']}",
            headers=csrf(client),
            json={"price": 18, "attachment_ids": []},
        ).json()["price"]
        == 18
    )

    admin = make_admin()
    course = admin.post(
        "/api/v1/courses",
        headers=csrf(admin),
        json={"name": "线性代数", "teacher": "李老师"},
    ).json()
    offering = admin.post(
        "/api/v1/course-offerings",
        headers=csrf(admin),
        json={"course_id": course["id"], "semester": "2026秋", "section": "1班"},
    ).json()
    review = client.post(
        "/api/v1/course-reviews",
        headers=csrf(client),
        json={"offering_id": offering["id"], "rating": 5, "tags": ["板书清晰"], "body": "课程结构清楚，作业反馈及时。"},
    )
    assert review.status_code == 201
    assert (
        admin.post(
            f"/api/v1/course-reviews/{review.json()['id']}/correction",
            headers=csrf(admin),
            json={"text": "课程考核方式以教务系统为准。"},
        ).status_code
        == 200
    )
    answer_client.close()
    admin.close()


def test_lost_observe_feedback_and_audience_workflows(client: TestClient) -> None:
    register(client, "lost-owner@test.edu.cn", "发布者")
    claimant = TestClient(app)
    claimant_user = register(claimant, "claimant@test.edu.cn", "认领者")
    item = client.post(
        "/api/v1/lost-items",
        headers=csrf(client),
        json={"kind": "found", "item_name": "蓝色校园卡套", "description": "卡套背面有贴纸", "location": "体育馆"},
    ).json()
    claim = claimant.post(
        f"/api/v1/lost-items/{item['id']}/claims",
        headers=csrf(claimant),
        json={"message": "我能说出卡套内校园卡的尾号。"},
    ).json()
    rows = client.get(f"/api/v1/lost-items/{item['id']}/claims").json()["items"]
    assert rows[0]["claimant_id"] == claimant_user["id"]
    assert (
        client.post(
            f"/api/v1/lost-items/{item['id']}/claims/{claim['id']}/decision",
            headers=csrf(client),
            json={"approve": True},
        ).json()["status"]
        == "approved"
    )

    observe = client.post(
        "/api/v1/observe-posts",
        headers=csrf(client),
        json={"title": "公共自习区持续外放", "body": "教学楼 123456 教室有人长时间外放视频。"},
    ).json()
    assert observe["status"] == "pending"
    admin = make_admin()
    cases = admin.get("/api/v1/admin/moderation-cases").json()["items"]
    case = next(row for row in cases if row["entity_id"] == observe["id"])
    assert (
        admin.post(
            f"/api/v1/admin/moderation-cases/{case['id']}/decision",
            headers=csrf(admin),
            json={"decision": "approve", "note": "隐私已打码", "respondent_id": claimant_user["id"]},
        ).status_code
        == 200
    )
    assert (
        claimant.post(
            f"/api/v1/observe-posts/{observe['id']}/response",
            headers=csrf(claimant),
            json={"body": "已停止外放并向现场同学致歉。"},
        ).status_code
        == 200
    )

    feedback = client.post(
        "/api/v1/feedback",
        headers=csrf(client),
        json={"type": "suggestion", "title": "增加教学楼筛选", "body": "希望活动列表可以按照教学楼或校区筛选。"},
    ).json()
    before = client.get("/api/v1/me").json()["xp"]
    payload = {"status": "accepted", "admin_note": "已进入开发计划", "reward_xp": 20, "reward_credit": 1}
    admin.post(f"/api/v1/admin/feedback/{feedback['id']}/decision", headers=csrf(admin), json=payload)
    admin.post(f"/api/v1/admin/feedback/{feedback['id']}/decision", headers=csrf(admin), json=payload)
    assert client.get("/api/v1/me").json()["xp"] == before + 20

    admin.post(
        "/api/v1/admin/announcements",
        headers=csrf(admin),
        json={"title": "教职工通知", "body": "仅教职工可见", "audience": "staff", "level": "normal"},
    )
    assert not any(row["title"] == "教职工通知" for row in client.get("/api/v1/announcements").json()["items"])
    claimant.close()
    admin.close()


def test_context_messages_and_image_upload_security(client: TestClient) -> None:
    seller = register(client, "seller@test.edu.cn", "卖家")
    client.patch("/api/v1/me/privacy", headers=csrf(client), json={"dm_stranger_off": True})
    listing = client.post(
        "/api/v1/listings",
        headers=csrf(client),
        json={
            "category": "生活用品",
            "title": "宿舍台灯",
            "description": "功能正常，只在校内线下面交。",
            "price": 15,
            "condition": "九成新",
            "location": "宿舍楼下",
        },
    ).json()
    buyer = TestClient(app)
    register(buyer, "buyer@test.edu.cn", "买家")
    direct = buyer.post(
        "/api/v1/conversations",
        headers=csrf(buyer),
        json={"recipient_id": seller["id"], "context_type": "direct", "first_message": "你好"},
    )
    assert direct.status_code == 403
    contextual = buyer.post(
        "/api/v1/conversations",
        headers=csrf(buyer),
        json={
            "recipient_id": seller["id"],
            "context_type": "listing",
            "context_id": listing["id"],
            "first_message": "请问台灯还在吗？",
        },
    )
    assert contextual.status_code == 201

    image = Image.new("RGB", (32, 32), color=(40, 100, 60))
    data = io.BytesIO()
    image.save(data, "PNG")
    upload = client.post(
        "/api/v1/uploads/images",
        headers=csrf(client),
        files={"file": ("safe.png", data.getvalue(), "image/png")},
    )
    assert upload.status_code == 201
    unsafe = client.post(
        "/api/v1/uploads/images",
        headers=csrf(client),
        files={"file": ("unsafe.svg", b"<svg><script>alert(1)</script></svg>", "image/svg+xml")},
    )
    assert unsafe.status_code == 400
    assert unsafe.json()["code"] == "UNSAFE_IMAGE_TYPE"
    buyer.close()
