"""
梧桐墙 扩展模块 v2
问答 / 生存手册 / 课程评价 / 文明观察台 / 治理公示 / 失物招领 / 校园活动 / 私信 / 公告 / 举报
由 main.py 末尾 `import extras; extras.register(app)` 挂载。
"""
import re
import secrets
from datetime import datetime, timedelta
from typing import Optional

from fastapi import APIRouter, Depends, Header, HTTPException
from fastapi.responses import FileResponse
from pydantic import BaseModel, Field

from main import (BASE_DIR, ABUSE_WORDS, db, now, hash_pw,
                  current_user, add_credit, notify)

router = APIRouter()
INCITE_WORDS = ["求扩散", "避雷", "人肉", "全校转发"]      # 观察台煽动性表达
ATTACK_WORDS = ABUSE_WORDS + ["垃圾老师", "废物", "脑子有问题"]  # 课评人身攻击
MIN_REVIEWS_TO_SHOW = 5                                     # 课评小样本保护

SCHEMA2 = """
CREATE TABLE IF NOT EXISTS questions(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL,
  tags TEXT DEFAULT '', category TEXT DEFAULT '其他', bounty INTEGER DEFAULT 0,
  accepted_answer_id INTEGER, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS answers(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  question_id INTEGER NOT NULL, user_id INTEGER NOT NULL,
  body TEXT NOT NULL, likes INTEGER DEFAULT 0, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS hb_articles(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL, category TEXT NOT NULL,
  title TEXT NOT NULL, body TEXT NOT NULL,
  favs INTEGER DEFAULT 0, featured INTEGER DEFAULT 0, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS course_reviews(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL, course TEXT NOT NULL, teacher TEXT NOT NULL,
  semester TEXT NOT NULL, rating INTEGER NOT NULL,
  tags TEXT DEFAULT '', body TEXT NOT NULL, correction TEXT DEFAULT '',
  created_at TEXT NOT NULL,
  UNIQUE(user_id, course, semester)
);
CREATE TABLE IF NOT EXISTS observe_posts(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL,
  raw_body TEXT DEFAULT '',
  status TEXT NOT NULL DEFAULT '审核中',
  response TEXT DEFAULT '', admin_note TEXT DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS penalties(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  masked TEXT NOT NULL, vtype TEXT NOT NULL, result TEXT NOT NULL,
  rule TEXT NOT NULL, appeal TEXT NOT NULL DEFAULT '可申诉', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS lost_items(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL, kind TEXT NOT NULL, item TEXT NOT NULL,
  place TEXT NOT NULL, happened_at TEXT NOT NULL, contact TEXT DEFAULT '站内私信',
  status TEXT NOT NULL DEFAULT '进行中', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS activities(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL, category TEXT NOT NULL,
  title TEXT NOT NULL, body TEXT DEFAULT '',
  joins INTEGER DEFAULT 0, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS dms(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  from_id INTEGER NOT NULL, to_id INTEGER NOT NULL,
  body TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS blocks(
  user_id INTEGER NOT NULL, blocked_id INTEGER NOT NULL,
  UNIQUE(user_id, blocked_id)
);
CREATE TABLE IF NOT EXISTS announcements(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL, body TEXT NOT NULL,
  level TEXT NOT NULL DEFAULT '普通', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ann_reads(
  ann_id INTEGER NOT NULL, user_id INTEGER NOT NULL, UNIQUE(ann_id, user_id)
);
CREATE TABLE IF NOT EXISTS hb_favs(
  user_id INTEGER NOT NULL, article_id INTEGER NOT NULL, UNIQUE(user_id, article_id)
);
"""


def _mask(nickname: str, uid: int) -> str:
    return f"用户 {nickname[0]}****{uid % 10}"


def add_penalty(conn, uid: int, nickname: str, vtype: str, result: str, rule: str):
    conn.execute("INSERT INTO penalties(masked,vtype,result,rule,created_at) VALUES(?,?,?,?,?)",
                 (_mask(nickname, uid), vtype, result, rule, now()))


def opt_user(authorization: str = Header(default="")):
    try:
        return current_user(authorization)
    except HTTPException:
        return None


def require_admin(user):
    if user["identity"] != "管理员":
        raise HTTPException(403, "需要管理员权限")


def add_exp(conn, uid: int, delta: int):
    conn.execute("UPDATE users SET exp=exp+? WHERE id=?", (delta, uid))


def seed2(conn):
    if conn.execute("SELECT COUNT(*) c FROM questions").fetchone()["c"]:
        return
    # 管理员账号
    if not conn.execute("SELECT 1 FROM users WHERE identity='管理员'").fetchone():
        salt = secrets.token_hex(8)
        conn.execute(
            "INSERT INTO users(email,pw_hash,pw_salt,nickname,anon_alias,identity,credit,created_at)"
            " VALUES(?,?,?,?,?,?,?,?)",
            ("admin@campus.edu.cn", hash_pw("admin1234", salt), salt,
             "管理员小梧", "梧桐#0001", "管理员", 100, now()))
    # 问答
    conn.execute("INSERT INTO questions(user_id,title,body,tags,category,bounty,created_at)"
                 " VALUES(2,'北区哪里可以打彩色胶装？','急，明早交开题报告','打印,加急','行政事务',50,?)", (now(),))
    conn.execute("INSERT INTO answers(question_id,user_id,body,likes,created_at)"
                 " VALUES(1,3,'北区二食堂一楼西侧「文汇打印」，彩打 1 元/页，胶装 6 元，开到晚上 11 点。',214,?)", (now(),))
    conn.execute("UPDATE questions SET accepted_answer_id=1 WHERE id=1")
    conn.execute("INSERT INTO questions(user_id,title,body,tags,category,bounty,created_at)"
                 " VALUES(1,'转专业面试都会问什么？','有没有近两年成功的学长学姐分享一下','转专业','学院·计算机',100,?)", (now(),))
    # 生存手册
    hb = [(3, "奖学金/竞赛/保研/考研", "保研 rank 计算全流程（2026 修订版）", "绩点、加分项、名额分配逐条讲清……", 892, 1),
          (3, "打印/维修/快递", "校内打印全攻略", "各打印点价格对比与高峰期避坑……", 310, 0),
          (4, "新生入学指南", "新生报到动线图与材料清单", "从校门到宿舍最短路径……", 2400, 1)]
    for uid, cat, t, b, f, ft in hb:
        conn.execute("INSERT INTO hb_articles(user_id,category,title,body,favs,featured,created_at)"
                     " VALUES(?,?,?,?,?,?,?)", (uid, cat, t, b, f, ft, now()))
    # 课评：线代 5 条（显示分数）、大物 2 条（隐藏）
    for i, (uid, r, tg, b) in enumerate([
            (1, 5, "给分好,板书清晰", "讲得清楚，作业量适中"), (2, 5, "给分好", "期末不难"),
            (3, 4, "点名多", "内容扎实但点名频繁"), (4, 5, "板书清晰", "推导完整"),
            (2, 4, "给分好", "适合打基础")]):
        conn.execute("INSERT OR IGNORE INTO course_reviews(user_id,course,teacher,semester,rating,tags,body,created_at)"
                     " VALUES(?,?,?,?,?,?,?,?)",
                     (uid, "线性代数", "李老师", f"2025春-{i}" if i > 3 else "2025春", r, tg, b, now()))
    conn.execute("INSERT OR IGNORE INTO course_reviews(user_id,course,teacher,semester,rating,tags,body,created_at)"
                 " VALUES(1,'大学物理II','周老师','2025春',3,'','作业偏多',?)", (now(),))
    conn.execute("INSERT OR IGNORE INTO course_reviews(user_id,course,teacher,semester,rating,tags,body,created_at)"
                 " VALUES(2,'大学物理II','周老师','2025春',4,'','讲义不错',?)", (now(),))
    # 观察台
    conn.execute("INSERT INTO observe_posts(user_id,title,body,status,created_at)"
                 " VALUES(2,'晚自习教室外放短视频约 40 分钟','教三 ▓▓▓ 教室，劝阻后仍继续，已附打码视频。','审核中',?)", (now(),))
    conn.execute("INSERT INTO observe_posts(user_id,title,body,status,response,admin_note,created_at)"
                 " VALUES(1,'某社团收费后活动多次取消未退款','事件描述已打码展示。','已公示',"
                 "'因场地审批延期，已于 7/3 全额退款并公示凭证。','纠纷已解决，依据《社区规范》4.2 条结案。',?)", (now(),))
    # 治理公示
    for m, v, r2, rl in [
            ("用户 A****7", "交易区发布违禁品（电子烟）", "删帖 · 信用 −10 · 禁发交易帖 30 天", "规范 3.1 禁售清单"),
            ("用户 K****2", "观察台帖泄露他人学号", "删帖 · 信用 −15 · 观察台禁言 90 天", "规范 5.2 隐私保护"),
            ("用户 W****9", "车队爽约 3 次未请假", "信用 −9 · 创建车队权限冻结 14 天", "规范 6.4 车队信用")]:
        conn.execute("INSERT INTO penalties(masked,vtype,result,rule,created_at) VALUES(?,?,?,?,?)",
                     (m, v, r2, rl, now()))
    # 失物招领
    for uid, k, it, pl, st in [(1, "捡到", "🎧 白色降噪耳机", "图书馆三楼东侧自习区", "进行中"),
                               (2, "丢失", "🪪 校园卡（尾号0917）", "二食堂或篮球场", "进行中"),
                               (3, "捡到", "🔑 宿舍钥匙（小熊挂件）", "操场看台", "已认领")]:
        conn.execute("INSERT INTO lost_items(user_id,kind,item,place,happened_at,status,created_at)"
                     " VALUES(?,?,?,?,?,?,?)", (uid, k, it, pl, now()[:10], st, now()))
    # 校园活动
    for uid, c, t, j in [(4, "社团招新", "校辩论队秋季纳新：9/10 明德楼宣讲", 45),
                         (2, "运动搭子", "找 6:40 操场晨跑搭子，互相监督", 4),
                         (1, "拼车/拼单", "周五 17:00 校门口→高铁站拼车，差 2 人", 2)]:
        conn.execute("INSERT INTO activities(user_id,category,title,joins,created_at)"
                     " VALUES(?,?,?,?,?)", (uid, c, t, j, now()))
    # 公告
    conn.execute("INSERT INTO announcements(title,body,level,created_at)"
                 " VALUES('《社区规范 v2.3》6/28 生效','交易区新增中介担保费率说明（2%）；观察台申诉期延长至 7 天；车队爽约扣分细化。','强提醒',?)", (now(),))
    conn.execute("INSERT INTO announcements(title,body,level,created_at)"
                 " VALUES('7/12 停服维护','凌晨 2:00–4:00 数据库升级，期间无法访问。','强提醒',?)", (now(),))
    conn.execute("INSERT INTO announcements(title,body,level,created_at)"
                 " VALUES('新增游戏标签合并','「Valorant / 瓦 / 无畏契约」已合并为统一标签。','普通',?)", (now(),))


with db() as _c:
    _c.executescript(SCHEMA2)
    seed2(_c)


# ================================================================ 问答
class QuestionIn(BaseModel):
    title: str = Field(min_length=2, max_length=80)
    body: str = ""
    tags: str = ""
    category: str = "其他"
    bounty: int = Field(ge=0, le=200, default=0)


class AnswerIn(BaseModel):
    body: str = Field(min_length=1, max_length=2000)


@router.get("/api/qa", tags=["问答"])
def list_qa():
    with db() as conn:
        qs = conn.execute(
            "SELECT q.*, u.nickname, (SELECT COUNT(*) FROM answers a WHERE a.question_id=q.id) n_answers"
            " FROM questions q JOIN users u ON u.id=q.user_id ORDER BY q.id DESC").fetchall()
        out = []
        for q in qs:
            best = None
            if q["accepted_answer_id"]:
                b = conn.execute("SELECT a.body, a.likes, u.nickname FROM answers a JOIN users u ON u.id=a.user_id"
                                 " WHERE a.id=?", (q["accepted_answer_id"],)).fetchone()
                if b:
                    best = {"body": b["body"], "likes": b["likes"], "author": b["nickname"]}
            out.append({"id": q["id"], "title": q["title"], "body": q["body"], "tags": q["tags"],
                        "category": q["category"], "bounty": q["bounty"], "asker": q["nickname"],
                        "n_answers": q["n_answers"], "best": best, "created_at": q["created_at"]})
    return out


@router.post("/api/qa", tags=["问答"])
def create_question(data: QuestionIn, user=Depends(current_user)):
    with db() as conn:
        cur = conn.execute("INSERT INTO questions(user_id,title,body,tags,category,bounty,created_at)"
                           " VALUES(?,?,?,?,?,?,?)",
                           (user["id"], data.title, data.body, data.tags, data.category, data.bounty, now()))
    return {"id": cur.lastrowid}


@router.post("/api/qa/{qid}/answer", tags=["问答"])
def create_answer(qid: int, data: AnswerIn, user=Depends(current_user)):
    with db() as conn:
        q = conn.execute("SELECT * FROM questions WHERE id=?", (qid,)).fetchone()
        if not q:
            raise HTTPException(404, "问题不存在")
        cur = conn.execute("INSERT INTO answers(question_id,user_id,body,created_at) VALUES(?,?,?,?)",
                           (qid, user["id"], data.body, now()))
        notify(conn, q["user_id"], f"你的问题「{q['title']}」有了新回答")
    return {"id": cur.lastrowid}


@router.post("/api/qa/answer/{aid}/accept", tags=["问答"])
def accept_answer(aid: int, user=Depends(current_user)):
    with db() as conn:
        a = conn.execute("SELECT a.*, q.user_id asker, q.bounty, q.title, q.accepted_answer_id FROM answers a"
                         " JOIN questions q ON q.id=a.question_id WHERE a.id=?", (aid,)).fetchone()
        if not a:
            raise HTTPException(404, "回答不存在")
        if a["asker"] != user["id"]:
            raise HTTPException(403, "只有提问者可以采纳")
        if a["accepted_answer_id"]:
            raise HTTPException(400, "该问题已采纳过答案")
        conn.execute("UPDATE questions SET accepted_answer_id=? WHERE id=?", (aid, a["question_id"]))
        gain = 20 + a["bounty"]
        add_exp(conn, a["user_id"], gain)
        notify(conn, a["user_id"], f"你的回答被采纳：「{a['title']}」，经验 +{gain}")
    return {"ok": True, "exp_to_answerer": gain}


@router.post("/api/qa/answer/{aid}/like", tags=["问答"])
def like_answer(aid: int):
    with db() as conn:
        conn.execute("UPDATE answers SET likes=likes+1 WHERE id=?", (aid,))
        r = conn.execute("SELECT likes FROM answers WHERE id=?", (aid,)).fetchone()
    if not r:
        raise HTTPException(404, "回答不存在")
    return {"likes": r["likes"]}


# ================================================================ 生存手册
class ArticleIn(BaseModel):
    category: str
    title: str = Field(min_length=2, max_length=60)
    body: str = Field(min_length=10)


@router.get("/api/handbook", tags=["生存手册"])
def list_handbook(category: Optional[str] = None):
    with db() as conn:
        q = ("SELECT h.*, u.nickname FROM hb_articles h JOIN users u ON u.id=h.user_id "
             + ("WHERE h.category=? " if category else "")
             + "ORDER BY h.featured DESC, h.favs DESC")
        rows = conn.execute(q, (category,) if category else ()).fetchall()
    return [{"id": r["id"], "category": r["category"], "title": r["title"], "body": r["body"][:80],
             "author": r["nickname"], "favs": r["favs"], "featured": bool(r["featured"])} for r in rows]


@router.post("/api/handbook", tags=["生存手册"])
def create_article(data: ArticleIn, user=Depends(current_user)):
    with db() as conn:
        cur = conn.execute("INSERT INTO hb_articles(user_id,category,title,body,created_at) VALUES(?,?,?,?,?)",
                           (user["id"], data.category, data.title, data.body, now()))
    return {"id": cur.lastrowid, "note": "发帖本身不加经验；被收藏 +2 / 管理员加精 +50"}


@router.post("/api/handbook/{aid}/fav", tags=["生存手册"])
def fav_article(aid: int, user=Depends(current_user)):
    with db() as conn:
        a = conn.execute("SELECT * FROM hb_articles WHERE id=?", (aid,)).fetchone()
        if not a:
            raise HTTPException(404, "文章不存在")
        try:
            conn.execute("INSERT INTO hb_favs(user_id,article_id) VALUES(?,?)", (user["id"], aid))
        except Exception:
            raise HTTPException(400, "已收藏过")
        conn.execute("UPDATE hb_articles SET favs=favs+1 WHERE id=?", (aid,))
        add_exp(conn, a["user_id"], 2)
    return {"ok": True}


@router.post("/api/handbook/{aid}/feature", tags=["生存手册"])
def feature_article(aid: int, user=Depends(current_user)):
    require_admin(user)
    with db() as conn:
        a = conn.execute("SELECT * FROM hb_articles WHERE id=?", (aid,)).fetchone()
        if not a:
            raise HTTPException(404, "文章不存在")
        conn.execute("UPDATE hb_articles SET featured=1 WHERE id=?", (aid,))
        add_exp(conn, a["user_id"], 50)
        notify(conn, a["user_id"], f"你的手册文章「{a['title']}」被管理员加精，经验 +50")
    return {"ok": True}


# ================================================================ 课程评价
class ReviewIn(BaseModel):
    course: str
    teacher: str
    semester: str = "2026春"
    rating: int = Field(ge=1, le=5)
    tags: str = ""
    body: str = Field(min_length=5)


@router.get("/api/courses", tags=["课程评价"])
def list_courses():
    with db() as conn:
        rows = conn.execute(
            "SELECT course, teacher, COUNT(*) n, AVG(rating) avg_r,"
            " GROUP_CONCAT(tags,',') all_tags FROM course_reviews GROUP BY course, teacher").fetchall()
        out = []
        for r in rows:
            tags = [t for t in (r["all_tags"] or "").split(",") if t]
            top = sorted(set(tags), key=lambda t: -tags.count(t))[:3]
            latest = conn.execute("SELECT body, rating, correction FROM course_reviews"
                                  " WHERE course=? AND teacher=? ORDER BY id DESC LIMIT 2",
                                  (r["course"], r["teacher"])).fetchall()
            out.append({"course": r["course"], "teacher": r["teacher"], "n": r["n"],
                        "score": round(r["avg_r"], 1) if r["n"] >= MIN_REVIEWS_TO_SHOW else None,
                        "hidden_reason": None if r["n"] >= MIN_REVIEWS_TO_SHOW
                        else f"评价数不足 {MIN_REVIEWS_TO_SHOW} 条，暂不显示分数",
                        "top_tags": top,
                        "latest": [dict(x) for x in latest]})
    return out


@router.post("/api/courses/review", tags=["课程评价"])
def create_review(data: ReviewIn, user=Depends(current_user)):
    if user["credit"] < 60:
        raise HTTPException(403, "评价课程需要信用 ≥ 60")
    hit = [w for w in ATTACK_WORDS if w in data.body]
    if hit:
        raise HTTPException(400, f"评价含违规表述（{hit[0]}…），请基于课程体验描述")
    with db() as conn:
        try:
            conn.execute("INSERT INTO course_reviews(user_id,course,teacher,semester,rating,tags,body,created_at)"
                         " VALUES(?,?,?,?,?,?,?,?)",
                         (user["id"], data.course, data.teacher, data.semester,
                          data.rating, data.tags, data.body, now()))
        except Exception:
            raise HTTPException(400, "同一课程同一学期只能评价一次")
    return {"ok": True}


class CorrectionIn(BaseModel):
    text: str = Field(min_length=2)


@router.post("/api/courses/review/{rid}/correction", tags=["课程评价"])
def add_correction(rid: int, data: CorrectionIn, user=Depends(current_user)):
    if user["identity"] not in ("管理员", "导员"):
        raise HTTPException(403, "更正入口面向老师/助教，由导员或管理员代提交")
    with db() as conn:
        cur = conn.execute("UPDATE course_reviews SET correction=? WHERE id=?", (data.text, rid))
        if cur.rowcount == 0:
            raise HTTPException(404, "评价不存在")
    return {"ok": True, "note": "更正以附注形式展示，原评价不删除"}


# ================================================================ 文明观察台
class ObserveIn(BaseModel):
    title: str = Field(min_length=4, max_length=60)
    body: str = Field(min_length=10)


@router.get("/api/observe", tags=["观察台"])
def list_observe(user=Depends(opt_user)):
    with db() as conn:
        rows = conn.execute("SELECT o.*, u.nickname FROM observe_posts o JOIN users u ON u.id=o.user_id"
                            " ORDER BY o.id DESC").fetchall()
    # 解码资格：信用 ≥ 阈值（管理员后台可调）且已同意「吃瓜不扩散」协议
    from main import get_threshold
    can_uncover = False
    if user:
        need = get_threshold("observe_uncover", 90)
        agreed = get_pref(user["id"], "uncover_agreed") == "1"
        can_uncover = (user["credit"] >= need and agreed) or user["identity"] == "管理员"
    out = []
    for r in rows:
        is_owner = user and user["id"] == r["user_id"]
        is_admin = user and user["identity"] == "管理员"
        if r["status"] == "审核中" and not (is_owner or is_admin):
            continue
        body = r["raw_body"] if (can_uncover and r["raw_body"]) else r["body"]
        out.append({"id": r["id"], "title": r["title"], "body": body, "status": r["status"],
                    "uncovered": bool(can_uncover and r["raw_body"] and r["raw_body"] != r["body"]),
                    "response": r["response"], "admin_note": r["admin_note"],
                    "created_at": r["created_at"], "mine": bool(is_owner)})
    return out


@router.post("/api/observe", tags=["观察台"])
def create_observe(data: ObserveIn, user=Depends(current_user)):
    if user["credit"] < 75:
        raise HTTPException(403, "观察台发帖需要信用 ≥ 75")
    hit = [w for w in INCITE_WORDS if w in data.title + data.body]
    if hit:
        raise HTTPException(400, f"禁止煽动性表达（{hit[0]}），请只描述事件本身")
    body = re.sub(r"\d{6,}", "▓▓▓▓▓▓", data.body)          # 学号/手机号自动打码
    with db() as conn:
        cur = conn.execute("INSERT INTO observe_posts(user_id,title,body,raw_body,created_at) VALUES(?,?,?,?,?)",
                           (user["id"], data.title, body, data.body, now()))
    return {"id": cur.lastrowid, "status": "审核中",
            "note": "涉及具体个人/组织的帖子须经人工审核，审核通过前仅你可见"}


class RespondIn(BaseModel):
    text: str = Field(min_length=2)


@router.post("/api/observe/{oid}/respond", tags=["观察台"])
def observe_respond(oid: int, data: RespondIn, user=Depends(current_user)):
    with db() as conn:
        cur = conn.execute("UPDATE observe_posts SET response=? WHERE id=?", (data.text, oid))
        if cur.rowcount == 0:
            raise HTTPException(404, "帖子不存在")
    return {"ok": True, "note": "回应将与原帖并列展示"}


class ReviewDecision(BaseModel):
    approve: bool
    note: str = ""


@router.post("/api/observe/{oid}/review", tags=["观察台"])
def observe_review(oid: int, data: ReviewDecision, user=Depends(current_user)):
    require_admin(user)
    with db() as conn:
        st = "已公示" if data.approve else "已驳回"
        cur = conn.execute("UPDATE observe_posts SET status=?, admin_note=? WHERE id=?",
                           (st, data.note, oid))
        if cur.rowcount == 0:
            raise HTTPException(404, "帖子不存在")
    return {"ok": True, "status": st}


# ================================================================ 治理公示
@router.get("/api/gov", tags=["治理公示"])
def list_gov():
    with db() as conn:
        rows = conn.execute("SELECT * FROM penalties ORDER BY id DESC LIMIT 50").fetchall()
    return [dict(r) for r in rows]


@router.post("/api/gov/{pid}/appeal", tags=["治理公示"])
def appeal(pid: int, user=Depends(current_user)):
    with db() as conn:
        cur = conn.execute("UPDATE penalties SET appeal='申诉中' WHERE id=? AND appeal='可申诉'", (pid,))
        if cur.rowcount == 0:
            raise HTTPException(400, "该记录不可申诉或已在申诉中")
    return {"ok": True, "note": "申诉已提交，管理员将在 7 天内复核"}


# ================================================================ 失物招领
class LostIn(BaseModel):
    kind: str                     # 捡到 / 丢失
    item: str
    place: str
    happened_at: str = ""
    contact: str = "站内私信"


@router.get("/api/lost", tags=["失物招领"])
def list_lost():
    with db() as conn:
        rows = conn.execute("SELECT l.*, u.nickname FROM lost_items l JOIN users u ON u.id=l.user_id"
                            " ORDER BY l.id DESC").fetchall()
    return [dict(r) for r in rows]


@router.post("/api/lost", tags=["失物招领"])
def create_lost(data: LostIn, user=Depends(current_user)):
    if data.kind not in ("捡到", "丢失"):
        raise HTTPException(400, "kind 必须为 捡到/丢失")
    with db() as conn:
        cur = conn.execute("INSERT INTO lost_items(user_id,kind,item,place,happened_at,contact,created_at)"
                           " VALUES(?,?,?,?,?,?,?)",
                           (user["id"], data.kind, data.item, data.place,
                            data.happened_at or now()[:10], data.contact, now()))
    return {"id": cur.lastrowid}


@router.post("/api/lost/{lid}/claim", tags=["失物招领"])
def claim_lost(lid: int, user=Depends(current_user)):
    with db() as conn:
        item = conn.execute("SELECT * FROM lost_items WHERE id=?", (lid,)).fetchone()
        if not item:
            raise HTTPException(404, "条目不存在")
        if item["status"] == "已认领":
            raise HTTPException(400, "已被认领")
        conn.execute("UPDATE lost_items SET status='已认领' WHERE id=?", (lid,))
        if item["kind"] == "捡到":
            add_credit(conn, item["user_id"], 2, "拾金不昧，失物被认领")
            notify(conn, item["user_id"], f"你捡到的「{item['item']}」已被认领，信用 +2")
    return {"ok": True}


# ================================================================ 校园活动
class ActivityIn(BaseModel):
    category: str
    title: str = Field(min_length=4, max_length=60)
    body: str = ""


@router.get("/api/activity", tags=["校园活动"])
def list_activity(category: Optional[str] = None):
    with db() as conn:
        q = ("SELECT a.*, u.nickname FROM activities a JOIN users u ON u.id=a.user_id "
             + ("WHERE a.category=? " if category else "") + "ORDER BY a.id DESC")
        rows = conn.execute(q, (category,) if category else ()).fetchall()
    return [dict(r) for r in rows]


@router.post("/api/activity", tags=["校园活动"])
def create_activity(data: ActivityIn, user=Depends(current_user)):
    with db() as conn:
        cur = conn.execute("INSERT INTO activities(user_id,category,title,body,created_at) VALUES(?,?,?,?,?)",
                           (user["id"], data.category, data.title, data.body, now()))
    return {"id": cur.lastrowid}


@router.post("/api/activity/{aid}/join", tags=["校园活动"])
def join_activity(aid: int, user=Depends(current_user)):
    with db() as conn:
        a = conn.execute("SELECT * FROM activities WHERE id=?", (aid,)).fetchone()
        if not a:
            raise HTTPException(404, "活动不存在")
        conn.execute("UPDATE activities SET joins=joins+1 WHERE id=?", (aid,))
        notify(conn, a["user_id"], f"「{user['nickname']}」加入了你的活动：{a['title']}")
    return {"ok": True, "joins": a["joins"] + 1}


# ================================================================ 私信（克制原则）
class DmIn(BaseModel):
    to_nickname: str
    body: str = Field(min_length=1, max_length=500)


@router.post("/api/dm", tags=["私信"])
def send_dm(data: DmIn, user=Depends(current_user)):
    with db() as conn:
        to = conn.execute("SELECT * FROM users WHERE nickname=?", (data.to_nickname,)).fetchone()
        if not to:
            raise HTTPException(404, "收件人不存在")
        if conn.execute("SELECT 1 FROM blocks WHERE user_id=? AND blocked_id=?",
                        (to["id"], user["id"])).fetchone():
            raise HTTPException(403, "对方已拉黑你，无法发送")
        n_block = conn.execute("SELECT COUNT(*) c FROM blocks WHERE blocked_id=?",
                               (user["id"],)).fetchone()["c"]
        if n_block >= 5:
            raise HTTPException(403, "你已被多人拉黑，私信功能已限制")
        # 每日上限：注册 7 天内 5 条；普通 20 条；信用 ≥ 85 不限
        if user["credit"] < 85:
            is_new = datetime.fromisoformat(user["created_at"]) > datetime.now() - timedelta(days=7)
            limit = 5 if is_new else 20
            sent = conn.execute("SELECT COUNT(*) c FROM dms WHERE from_id=? AND created_at>=?",
                                (user["id"], now()[:10])).fetchone()["c"]
            if sent >= limit:
                raise HTTPException(429, f"已达今日私信上限（{limit} 条）")
        conn.execute("INSERT INTO dms(from_id,to_id,body,created_at) VALUES(?,?,?,?)",
                     (user["id"], to["id"], data.body, now()))
        notify(conn, to["id"], f"来自「{user['nickname']}」的私信：{data.body[:30]}")
    return {"ok": True}


@router.get("/api/dm", tags=["私信"])
def inbox(user=Depends(current_user)):
    with db() as conn:
        rows = conn.execute(
            "SELECT d.*, uf.nickname f_name, ut.nickname t_name FROM dms d"
            " JOIN users uf ON uf.id=d.from_id JOIN users ut ON ut.id=d.to_id"
            " WHERE d.from_id=? OR d.to_id=? ORDER BY d.id DESC LIMIT 30",
            (user["id"], user["id"])).fetchall()
    return [{"from": r["f_name"], "to": r["t_name"], "body": r["body"], "at": r["created_at"]} for r in rows]


@router.post("/api/dm/block/{nickname}", tags=["私信"])
def block(nickname: str, user=Depends(current_user)):
    with db() as conn:
        t = conn.execute("SELECT id FROM users WHERE nickname=?", (nickname,)).fetchone()
        if not t:
            raise HTTPException(404, "用户不存在")
        conn.execute("INSERT OR IGNORE INTO blocks(user_id,blocked_id) VALUES(?,?)", (user["id"], t["id"]))
    return {"ok": True}


# ================================================================ 公告
@router.get("/api/announcements", tags=["公告"])
def list_announcements(user=Depends(opt_user)):
    with db() as conn:
        rows = conn.execute("SELECT * FROM announcements ORDER BY id DESC").fetchall()
        reads = set()
        if user:
            reads = {r["ann_id"] for r in conn.execute(
                "SELECT ann_id FROM ann_reads WHERE user_id=?", (user["id"],))}
        counts = {r["ann_id"]: r["c"] for r in conn.execute(
            "SELECT ann_id, COUNT(*) c FROM ann_reads GROUP BY ann_id")}
    return [{"id": r["id"], "title": r["title"], "body": r["body"], "level": r["level"],
             "created_at": r["created_at"], "read": r["id"] in reads,
             "read_count": counts.get(r["id"], 0)} for r in rows]


@router.post("/api/announcements/{aid}/read", tags=["公告"])
def read_announcement(aid: int, user=Depends(current_user)):
    with db() as conn:
        conn.execute("INSERT OR IGNORE INTO ann_reads(ann_id,user_id) VALUES(?,?)", (aid, user["id"]))
    return {"ok": True}


# ================================================================ 举报（含交易冻结）
class ReportIn(BaseModel):
    kind: str                     # post / listing / observe
    item_id: int
    reason: str = Field(min_length=2)


@router.post("/api/report", tags=["举报"])
def report(data: ReportIn, user=Depends(current_user)):
    with db() as conn:
        if data.kind == "post":
            conn.execute("UPDATE posts SET reports=reports+1 WHERE id=?", (data.item_id,))
            return {"ok": True, "note": "举报已记录，将计入热度惩罚"}
        if data.kind == "listing":
            cur = conn.execute("UPDATE listings SET status='冻结（纠纷处理中）' WHERE id=? AND status='在售'",
                               (data.item_id,))
            if cur.rowcount == 0:
                raise HTTPException(400, "该商品不存在或已在处理中")
            return {"ok": True, "note": "帖子已冻结，进入双方举证阶段（48h），随后管理员仲裁"}
        if data.kind == "observe":
            conn.execute("UPDATE observe_posts SET status='复审中' WHERE id=?", (data.item_id,))
            return {"ok": True, "note": "已转入复审"}
    raise HTTPException(400, "kind 必须为 post/listing/observe")


# ================================================================ 头衔
@router.get("/api/my-titles", tags=["认证"])
def my_titles(user=Depends(current_user)):
    with db() as conn:
        accepted = conn.execute(
            "SELECT COUNT(*) c FROM questions q JOIN answers a ON a.id=q.accepted_answer_id"
            " WHERE a.user_id=?", (user["id"],)).fetchone()["c"]
        featured = conn.execute("SELECT COUNT(*) c FROM hb_articles WHERE user_id=? AND featured=1",
                                (user["id"],)).fetchone()["c"]
    titles = []
    if accepted >= 10: titles.append("答疑达人")
    elif accepted >= 1: titles.append("热心答主")
    if featured >= 1: titles.append("精华作者")
    if user["exp"] >= 500: titles.append("新生导师")
    if user["exp"] >= 2000: titles.append("年度校园百科贡献者")
    return {"exp": user["exp"], "accepted": accepted, "featured": featured, "titles": titles}


# ================================================================ 挂载
def register(app):
    app.include_router(router)

    @app.get("/app-extra.js", include_in_schema=False)
    def extra_js():
        f = BASE_DIR.parent / "frontend" / "app-extra.js"
        if f.exists():
            return FileResponse(f, media_type="application/javascript")
        raise HTTPException(404)


# ================================================================ v3 增强：通知已读 / 静态资源
@router.post("/api/notifications/read-all", tags=["通知"])
def read_all_notifications(user=Depends(current_user)):
    with db() as conn:
        conn.execute("UPDATE notifications SET is_read=1 WHERE user_id=?", (user["id"],))
    return {"ok": True}


@router.get("/polish.js", include_in_schema=False)
def polish_js():
    f = BASE_DIR.parent / "frontend" / "polish.js"
    if f.exists():
        return FileResponse(f, media_type="application/javascript")
    raise HTTPException(404)


# ################################################################ v4：账号体系 / 评论 / 反馈 / 管理后台 / 用户后台
import json as _json
from main import PERMISSIONS, get_threshold, DB_PATH, ALLOWED_MAIL
from main import register as core_register, RegisterIn as CoreRegisterIn

SCHEMA4 = """
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS user_prefs(user_id INTEGER NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL, UNIQUE(user_id,key));
CREATE TABLE IF NOT EXISTS email_codes(email TEXT PRIMARY KEY, code TEXT NOT NULL, expires_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS comments(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL, item_id INTEGER NOT NULL, user_id INTEGER NOT NULL,
  body TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS feedback(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL, ftype TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT '待处理', reward TEXT DEFAULT '', admin_note TEXT DEFAULT '',
  created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS team_leaves(
  team_id INTEGER NOT NULL, user_id INTEGER NOT NULL, departure_at TEXT NOT NULL,
  UNIQUE(team_id,user_id,departure_at));
"""
DEFAULT_HB_CATS = ["新生入学指南", "选课指南", "宿舍避坑", "食堂/外卖评价", "校园地图与隐藏地点",
                   "实验室/办事流程", "奖学金/竞赛/保研/考研", "社团体验", "打印/维修/快递",
                   "校医院攻略", "毕业手续指南", "校园服务评分"]

with db() as _c4:
    _c4.executescript(SCHEMA4)
    try:
        _c4.execute("ALTER TABLE observe_posts ADD COLUMN raw_body TEXT DEFAULT ''")
    except Exception:
        pass
    if not _c4.execute("SELECT 1 FROM settings WHERE key='hb_categories'").fetchone():
        _c4.execute("INSERT INTO settings VALUES('hb_categories',?)", (_json.dumps(DEFAULT_HB_CATS, ensure_ascii=False),))
        _c4.execute("INSERT OR IGNORE INTO settings VALUES('observe_uncover_credit','90')")
        _c4.execute("INSERT OR IGNORE INTO settings VALUES('auto_clean','1')")


def get_pref(uid: int, key: str) -> str:
    with db() as conn:
        r = conn.execute("SELECT value FROM user_prefs WHERE user_id=? AND key=?", (uid, key)).fetchone()
    return r["value"] if r else ""


def set_setting(conn, key: str, value: str):
    conn.execute("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
                 (key, value))


# ---------------------------------------------------------------- 邮箱验证码注册
class SendCodeIn(BaseModel):
    email: str


@router.post("/api/auth/send-code", tags=["认证"])
def send_code(data: SendCodeIn):
    email = data.email.strip()
    if not ALLOWED_MAIL.search(email):
        raise HTTPException(400, "仅支持 QQ 邮箱 / Gmail / edu.cn 校园邮箱")
    code = f"{secrets.randbelow(1000000):06d}"
    exp = (datetime.now() + timedelta(minutes=10)).isoformat(timespec="seconds")
    with db() as conn:
        conn.execute("INSERT INTO email_codes(email,code,expires_at) VALUES(?,?,?)"
                     " ON CONFLICT(email) DO UPDATE SET code=excluded.code, expires_at=excluded.expires_at",
                     (email, code, exp))
        conn.execute("INSERT INTO email_log(to_email,subject,body,created_at) VALUES(?,?,?,?)",
                     (email, "【梧桐墙】注册验证码", f"你的验证码是 {code}，10 分钟内有效。", now()))
    # demo_code 仅供本地演示（模拟邮箱未接入 SMTP 时前端可自动填入）；生产环境务必删除该字段
    return {"ok": True, "note": "验证码已发送至邮箱（演示模式写入 email_log）", "demo_code": code}


class Register2In(BaseModel):
    email: str
    code: str
    password: str = Field(min_length=6)
    nickname: str = Field(min_length=1, max_length=20)
    agreed: bool = False


@router.post("/api/auth/register2", tags=["认证"])
def register2(data: Register2In):
    with db() as conn:
        r = conn.execute("SELECT * FROM email_codes WHERE email=?", (data.email.strip(),)).fetchone()
    if not r or r["code"] != data.code.strip():
        raise HTTPException(400, "验证码错误")
    if r["expires_at"] < now():
        raise HTTPException(400, "验证码已过期，请重新发送")
    return core_register(CoreRegisterIn(email=data.email, password=data.password,
                                        nickname=data.nickname, agreed=data.agreed))


# ---------------------------------------------------------------- 全站评论（回帖）
class CommentIn(BaseModel):
    kind: str            # post / listing / hb / activity / observe / lost
    item_id: int
    body: str = Field(min_length=1, max_length=1000)


COMMENT_KINDS = {"post", "listing", "hb", "activity", "observe", "lost"}


@router.get("/api/comments", tags=["评论"])
def list_comments(kind: str, item_id: int):
    with db() as conn:
        rows = conn.execute("SELECT c.*, u.nickname FROM comments c JOIN users u ON u.id=c.user_id"
                            " WHERE c.kind=? AND c.item_id=? ORDER BY c.id", (kind, item_id)).fetchall()
    return [{"id": r["id"], "author": r["nickname"], "body": r["body"], "at": r["created_at"]} for r in rows]


@router.post("/api/comments", tags=["评论"])
def create_comment(data: CommentIn, user=Depends(current_user)):
    if data.kind not in COMMENT_KINDS:
        raise HTTPException(400, "不支持的评论对象")
    hit = [w for w in ABUSE_WORDS if w in data.body]
    if hit:
        raise HTTPException(400, f"评论含辱骂词（{hit[0]}…）")
    with db() as conn:
        if data.kind == "post":
            p = conn.execute("SELECT allow_comments FROM posts WHERE id=?", (data.item_id,)).fetchone()
            if not p:
                raise HTTPException(404, "帖子不存在")
            if not p["allow_comments"]:
                raise HTTPException(403, "楼主设置了不接收评论")
            conn.execute("UPDATE posts SET replies=replies+1 WHERE id=?", (data.item_id,))
        cur = conn.execute("INSERT INTO comments(kind,item_id,user_id,body,created_at) VALUES(?,?,?,?,?)",
                           (data.kind, data.item_id, user["id"], data.body, now()))
    return {"id": cur.lastrowid}


# ---------------------------------------------------------------- Bug / 建议反馈
class FeedbackIn(BaseModel):
    ftype: str = "建议"       # bug / 建议 / 畅想
    title: str = Field(min_length=2, max_length=60)
    body: str = Field(min_length=5)


@router.get("/api/feedback", tags=["反馈"])
def list_feedback(user=Depends(opt_user)):
    with db() as conn:
        rows = conn.execute("SELECT f.*, u.nickname FROM feedback f JOIN users u ON u.id=f.user_id"
                            " ORDER BY f.id DESC LIMIT 50").fetchall()
    out = []
    for r in rows:
        mine = user and user["id"] == r["user_id"]
        if r["status"] == "待处理" and not (mine or (user and user["identity"] == "管理员")):
            continue  # 待处理的仅本人与管理员可见，避免刷屏
        out.append({"id": r["id"], "ftype": r["ftype"], "title": r["title"], "body": r["body"],
                    "status": r["status"], "reward": r["reward"], "admin_note": r["admin_note"],
                    "author": r["nickname"], "mine": bool(mine)})
    return out


@router.post("/api/feedback", tags=["反馈"])
def create_feedback(data: FeedbackIn, user=Depends(current_user)):
    with db() as conn:
        cur = conn.execute("INSERT INTO feedback(user_id,ftype,title,body,created_at) VALUES(?,?,?,?,?)",
                           (user["id"], data.ftype, data.title, data.body, now()))
    return {"id": cur.lastrowid, "note": "感谢反馈！管理员采纳后会有经验/信用奖励"}


class FeedbackDecision(BaseModel):
    status: str               # 已采纳 / 已拒绝 / 处理中
    reward_exp: int = 0
    reward_credit: int = 0
    note: str = ""


@router.post("/api/admin/feedback/{fid}", tags=["管理后台"])
def decide_feedback(fid: int, data: FeedbackDecision, user=Depends(current_user)):
    require_admin(user)
    with db() as conn:
        f = conn.execute("SELECT * FROM feedback WHERE id=?", (fid,)).fetchone()
        if not f:
            raise HTTPException(404, "反馈不存在")
        reward = []
        if data.status == "已采纳":
            if data.reward_exp:
                add_exp(conn, f["user_id"], data.reward_exp)
                reward.append(f"经验 +{data.reward_exp}")
            if data.reward_credit:
                add_credit(conn, f["user_id"], data.reward_credit, f"反馈被采纳：{f['title']}")
                reward.append(f"信用 +{data.reward_credit}")
        conn.execute("UPDATE feedback SET status=?, reward=?, admin_note=? WHERE id=?",
                     (data.status, " ".join(reward), data.note, fid))
        notify(conn, f["user_id"], f"你的反馈「{f['title']}」{data.status}。{' '.join(reward)} {data.note}")
    return {"ok": True}


# ---------------------------------------------------------------- 车队：请假 / 车头改车
@router.post("/api/teams/{team_id}/excuse", tags=["车队"])
def excuse(team_id: int, user=Depends(current_user)):
    with db() as conn:
        team = conn.execute("SELECT * FROM teams WHERE id=?", (team_id,)).fetchone()
        if not team:
            raise HTTPException(404, "车队不存在")
        if not conn.execute("SELECT 1 FROM team_members WHERE team_id=? AND user_id=?",
                            (team_id, user["id"])).fetchone():
            raise HTTPException(400, "你不在这个车队")
        conn.execute("INSERT OR IGNORE INTO team_leaves(team_id,user_id,departure_at) VALUES(?,?,?)",
                     (team_id, user["id"], team["departure_at"]))
        notify(conn, team["owner_id"], f"「{user['nickname']}」本次 {team['game']} 车请假（{team['departure_at'][11:16]}）")
    return {"ok": True, "note": "已请假：本次发车不提醒你、临时退出不扣信用"}


class TeamUpdateIn(BaseModel):
    notes: Optional[str] = None
    remind_before: Optional[int] = Field(ge=5, le=1440, default=None)
    voice_link: Optional[str] = None


@router.post("/api/teams/{team_id}/update", tags=["车队"])
def update_team(team_id: int, data: TeamUpdateIn, user=Depends(current_user)):
    with db() as conn:
        team = conn.execute("SELECT * FROM teams WHERE id=?", (team_id,)).fetchone()
        if not team:
            raise HTTPException(404, "车队不存在")
        if team["owner_id"] != user["id"] and user["identity"] != "管理员":
            raise HTTPException(403, "只有车头可以修改")
        for k in ("notes", "remind_before", "voice_link"):
            v = getattr(data, k)
            if v is not None:
                conn.execute(f"UPDATE teams SET {k}=? WHERE id=?", (v, team_id))
        members = conn.execute("SELECT user_id FROM team_members WHERE team_id=? AND user_id!=?",
                               (team_id, user["id"])).fetchall()
        for m in members:
            notify(conn, m["user_id"], f"📢 车头更新了 {team['game']} 车队信息，请留意注意事项与提醒时间")
    return {"ok": True}


# ---------------------------------------------------------------- 用户后台
class PasswordIn(BaseModel):
    old: str
    new: str = Field(min_length=6)


@router.post("/api/me/password", tags=["用户后台"])
def change_password(data: PasswordIn, user=Depends(current_user)):
    from main import hash_pw as _hp
    with db() as conn:
        u = conn.execute("SELECT * FROM users WHERE id=?", (user["id"],)).fetchone()
        if _hp(data.old, u["pw_salt"]) != u["pw_hash"]:
            raise HTTPException(400, "原密码错误")
        salt = secrets.token_hex(8)
        conn.execute("UPDATE users SET pw_hash=?, pw_salt=? WHERE id=?",
                     (_hp(data.new, salt), salt, user["id"]))
    return {"ok": True, "note": "密码已更新"}


class EmailChangeIn(BaseModel):
    new_email: str
    code: str


@router.post("/api/me/email", tags=["用户后台"])
def change_email(data: EmailChangeIn, user=Depends(current_user)):
    if not ALLOWED_MAIL.search(data.new_email.strip()):
        raise HTTPException(400, "仅支持 QQ/Gmail/edu.cn 邮箱")
    with db() as conn:
        r = conn.execute("SELECT * FROM email_codes WHERE email=?", (data.new_email.strip(),)).fetchone()
        if not r or r["code"] != data.code.strip() or r["expires_at"] < now():
            raise HTTPException(400, "验证码错误或已过期（请先向新邮箱发送验证码）")
        if conn.execute("SELECT 1 FROM users WHERE email=?", (data.new_email,)).fetchone():
            raise HTTPException(400, "该邮箱已被占用")
        conn.execute("UPDATE users SET email=? WHERE id=?", (data.new_email.strip(), user["id"]))
    return {"ok": True}


class PrefIn(BaseModel):
    key: str
    value: str


@router.post("/api/me/prefs", tags=["用户后台"])
def set_pref(data: PrefIn, user=Depends(current_user)):
    if data.key not in ("uncover_agreed", "dm_stranger_off", "hide_online"):
        raise HTTPException(400, "不支持的偏好项")
    with db() as conn:
        conn.execute("INSERT INTO user_prefs(user_id,key,value) VALUES(?,?,?)"
                     " ON CONFLICT(user_id,key) DO UPDATE SET value=excluded.value",
                     (user["id"], data.key, data.value))
    return {"ok": True}


@router.get("/api/me/prefs", tags=["用户后台"])
def my_prefs(user=Depends(current_user)):
    with db() as conn:
        rows = conn.execute("SELECT key,value FROM user_prefs WHERE user_id=?", (user["id"],)).fetchall()
    return {r["key"]: r["value"] for r in rows}


@router.get("/api/me/posts", tags=["用户后台"])
def my_posts(user=Depends(current_user)):
    out = []
    with db() as conn:
        for r in conn.execute("SELECT id,board,title,body,created_at FROM posts WHERE user_id=? ORDER BY id DESC",
                              (user["id"],)):
            out.append({"kind": "post", "id": r["id"], "title": r["title"] or r["body"][:24],
                        "board": r["board"], "at": r["created_at"]})
        for r in conn.execute("SELECT id,title,status,created_at FROM listings WHERE seller_id=? ORDER BY id DESC",
                              (user["id"],)):
            out.append({"kind": "listing", "id": r["id"], "title": r["title"], "board": "集市·" + r["status"],
                        "at": r["created_at"]})
        for r in conn.execute("SELECT id,title,created_at FROM hb_articles WHERE user_id=? ORDER BY id DESC",
                              (user["id"],)):
            out.append({"kind": "hb", "id": r["id"], "title": r["title"], "board": "手册", "at": r["created_at"]})
    return out


class DeleteMineIn(BaseModel):
    kind: str
    item_id: int


@router.post("/api/me/delete", tags=["用户后台"])
def delete_mine(data: DeleteMineIn, user=Depends(current_user)):
    table = {"post": ("posts", "user_id"), "listing": ("listings", "seller_id"), "hb": ("hb_articles", "user_id")}
    if data.kind not in table:
        raise HTTPException(400, "不支持的类型")
    t, col = table[data.kind]
    with db() as conn:
        cur = conn.execute(f"DELETE FROM {t} WHERE id=? AND {col}=?", (data.item_id, user["id"]))
        if cur.rowcount == 0:
            raise HTTPException(404, "内容不存在或不属于你")
    return {"ok": True}


@router.get("/api/permissions", tags=["认证"])
def permissions_public():
    return [{"name": n, "key": k, "need": get_threshold(k, d)} for n, k, d in PERMISSIONS] + [
        {"name": "观察台解码（吃瓜不扩散）", "key": "observe_uncover", "need": get_threshold("observe_uncover", 90)}]


# ---------------------------------------------------------------- 手册分类（管理员可改）
@router.get("/api/handbook-categories", tags=["生存手册"])
def hb_categories():
    with db() as conn:
        r = conn.execute("SELECT value FROM settings WHERE key='hb_categories'").fetchone()
    return _json.loads(r["value"]) if r else DEFAULT_HB_CATS


# ---------------------------------------------------------------- 管理后台
@router.get("/api/admin/overview", tags=["管理后台"])
def admin_overview(user=Depends(current_user)):
    require_admin(user)
    stats = {}
    with db() as conn:
        for name, sql in [("用户", "users"), ("树洞帖", "posts"), ("车队", "teams"), ("商品", "listings"),
                          ("问题", "questions"), ("手册", "hb_articles"), ("课评", "course_reviews"),
                          ("观察帖", "observe_posts"), ("反馈", "feedback"), ("评论", "comments")]:
            stats[name] = conn.execute(f"SELECT COUNT(*) c FROM {sql}").fetchone()["c"]
        stats["待审观察帖"] = conn.execute("SELECT COUNT(*) c FROM observe_posts WHERE status='审核中'").fetchone()["c"]
        stats["待处理反馈"] = conn.execute("SELECT COUNT(*) c FROM feedback WHERE status='待处理'").fetchone()["c"]
    return stats


@router.get("/api/admin/users", tags=["管理后台"])
def admin_users(q: str = "", user=Depends(current_user)):
    require_admin(user)
    with db() as conn:
        rows = conn.execute("SELECT id,email,nickname,identity,credit,exp,created_at FROM users"
                            " WHERE nickname LIKE ? OR email LIKE ? ORDER BY id DESC LIMIT 50",
                            (f"%{q}%", f"%{q}%")).fetchall()
    return [dict(r) for r in rows]


class AdminUserIn(BaseModel):
    credit: Optional[int] = Field(ge=0, le=100, default=None)
    identity: Optional[str] = None


@router.post("/api/admin/users/{uid}", tags=["管理后台"])
def admin_update_user(uid: int, data: AdminUserIn, user=Depends(current_user)):
    require_admin(user)
    with db() as conn:
        u = conn.execute("SELECT * FROM users WHERE id=?", (uid,)).fetchone()
        if not u:
            raise HTTPException(404, "用户不存在")
        if data.credit is not None:
            delta = data.credit - u["credit"]
            conn.execute("UPDATE users SET credit=? WHERE id=?", (data.credit, uid))
            conn.execute("INSERT INTO credit_log(user_id,delta,reason,created_at) VALUES(?,?,?,?)",
                         (uid, delta, f"管理员调整（{user['nickname']}）", now()))
        if data.identity:
            conn.execute("UPDATE users SET identity=? WHERE id=?", (data.identity, uid))
    return {"ok": True}


class AdminDeleteIn(BaseModel):
    kind: str
    item_id: int
    reason: str = ""


ADMIN_TABLES = {"post": "posts", "listing": "listings", "team": "teams", "question": "questions",
                "hb": "hb_articles", "activity": "activities", "lost": "lost_items",
                "observe": "observe_posts", "comment": "comments", "review": "course_reviews"}


@router.post("/api/admin/delete", tags=["管理后台"])
def admin_delete(data: AdminDeleteIn, user=Depends(current_user)):
    require_admin(user)
    t = ADMIN_TABLES.get(data.kind)
    if not t:
        raise HTTPException(400, "不支持的类型")
    with db() as conn:
        cur = conn.execute(f"DELETE FROM {t} WHERE id=?", (data.item_id,))
        if cur.rowcount == 0:
            raise HTTPException(404, "内容不存在")
        if data.reason:
            conn.execute("INSERT INTO penalties(masked,vtype,result,rule,created_at) VALUES(?,?,?,?,?)",
                         ("（管理删除）", data.kind + " 内容删除", data.reason, "管理员操作", now()))
    return {"ok": True}


class AnnounceIn(BaseModel):
    title: str
    body: str
    level: str = "普通"


@router.post("/api/admin/announce", tags=["管理后台"])
def admin_announce(data: AnnounceIn, user=Depends(current_user)):
    require_admin(user)
    with db() as conn:
        conn.execute("INSERT INTO announcements(title,body,level,created_at) VALUES(?,?,?,?)",
                     (data.title, data.body, data.level, now()))
    return {"ok": True}


@router.get("/api/admin/settings", tags=["管理后台"])
def admin_get_settings(user=Depends(current_user)):
    require_admin(user)
    with db() as conn:
        rows = conn.execute("SELECT * FROM settings").fetchall()
    return {r["key"]: r["value"] for r in rows}


class SettingIn(BaseModel):
    key: str
    value: str


SETTING_KEYS_OK = ("hb_categories", "observe_uncover_credit", "auto_clean",
                   "perm_anon_post", "perm_trade", "perm_observe", "perm_contact",
                   "perm_create_team", "perm_course_review", "perm_observe_uncover",
                   "site_notice")


@router.post("/api/admin/settings", tags=["管理后台"])
def admin_set_setting(data: SettingIn, user=Depends(current_user)):
    require_admin(user)
    if data.key not in SETTING_KEYS_OK:
        raise HTTPException(400, f"不支持的设置项，允许：{','.join(SETTING_KEYS_OK)}")
    with db() as conn:
        set_setting(conn, data.key, data.value)
    return {"ok": True}


@router.get("/api/admin/backup", tags=["管理后台"])
def admin_backup(user=Depends(current_user)):
    require_admin(user)
    if not DB_PATH.exists():
        raise HTTPException(404, "数据库文件不存在")
    return FileResponse(DB_PATH, filename=f"wutong-backup-{now()[:10]}.db",
                        media_type="application/octet-stream")


@router.post("/api/admin/clean", tags=["管理后台"])
def admin_clean(user=Depends(current_user)):
    require_admin(user)
    with db() as conn:
        n1 = conn.execute("DELETE FROM posts WHERE expires_at IS NOT NULL AND expires_at<?", (now(),)).rowcount
        n2 = conn.execute("DELETE FROM notifications WHERE is_read=1 AND created_at<?",
                          ((datetime.now() - timedelta(days=30)).isoformat(timespec="seconds"),)).rowcount
        n3 = conn.execute("DELETE FROM email_codes WHERE expires_at<?", (now(),)).rowcount
    return {"ok": True, "deleted": {"过期匿名帖": n1, "30天前已读通知": n2, "过期验证码": n3}}


# ---------------------------------------------------------------- 自动清理线程（每小时）
import threading as _th
import time as _time


def _auto_clean_loop():
    while True:
        try:
            with db() as conn:
                r = conn.execute("SELECT value FROM settings WHERE key='auto_clean'").fetchone()
                if r and r["value"] == "1":
                    conn.execute("DELETE FROM posts WHERE expires_at IS NOT NULL AND expires_at<?", (now(),))
                    conn.execute("DELETE FROM email_codes WHERE expires_at<?", (now(),))
        except Exception as e:
            print("[auto-clean]", e)
        _time.sleep(3600)


_th.Thread(target=_auto_clean_loop, daemon=True).start()


# ---------------------------------------------------------------- 静态资源 v4
@router.get("/app-v4.js", include_in_schema=False)
def v4_js():
    f = BASE_DIR.parent / "frontend" / "app-v4.js"
    if f.exists():
        return FileResponse(f, media_type="application/javascript")
    raise HTTPException(404)
