"""
梧桐墙 · 校园社区 后端服务
FastAPI + SQLite（标准库 sqlite3，零 ORM 依赖）

启动：
    pip install -r requirements.txt
    uvicorn main:app --reload --port 8000
打开：
    http://127.0.0.1:8000          → 前端页面
    http://127.0.0.1:8000/docs    → 交互式 API 文档
"""
import hashlib
import os
import re
import secrets
import sqlite3
import threading
import time
from contextlib import contextmanager
from datetime import datetime, timedelta
from pathlib import Path
from typing import Optional

from fastapi import Depends, FastAPI, Header, HTTPException
from fastapi.responses import FileResponse
from pydantic import BaseModel, Field

BASE_DIR = Path(__file__).resolve().parent
DB_PATH = Path(os.environ.get("WUTONG_DB", BASE_DIR / "campus.db"))  # 可用环境变量覆盖数据库位置
FRONTEND = BASE_DIR.parent / "frontend" / "index.html"

app = FastAPI(title="梧桐墙 API", version="0.1.0", description="校园社区原型后端：注册登录 / 树洞 / 游戏车队 / 二手集市 / 信用分 / 热榜 / 站内通知")

# ---------------------------------------------------------------- 常量与规则
ALLOWED_MAIL = re.compile(r"@(qq\.com|gmail\.com|[\w.-]+\.edu\.cn)$", re.I)
CARE_WORDS = ["自杀", "轻生", "不想活", "自残", "活不下去", "想死", "想跳楼", "死了算了", "撑不下去", "没有意义了", "结束这一切"]
ABUSE_WORDS = ["傻逼", "去死吧", "全家死"]  # 演示用辱骂词表，生产环境应接入完善词库
BANNED_GOODS = ["香烟", "电子烟", "白酒", "啤酒", "处方药", "管制刀", "弹簧刀", "代考", "代写", "账号出售"]
GAMES = ["LOL", "瓦", "CS2", "原神", "星铁", "王者", "雀魂", "MC", "饥荒", "DND"]
GAME_ALIAS = {"valorant": "瓦", "无畏契约": "瓦", "星穹铁道": "星铁", "minecraft": "MC"}
RATE_TAGS = ["友善", "沟通", "技术", "准时"]

PERMISSIONS = [
    ("匿名发帖", "anon_post", 60),
    ("发布交易帖", "trade", 70),
    ("观察台/纠纷区发帖", "observe", 75),
    ("发布联系方式", "contact", 70),
    ("创建游戏车队", "create_team", 60),
    ("评价课程", "course_review", 60),
]

# ---------------------------------------------------------------- 数据库
@contextmanager
def db():
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA foreign_keys=ON")
    try:
        yield conn
        conn.commit()
    finally:
        conn.close()


SCHEMA = """
CREATE TABLE IF NOT EXISTS users(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  email TEXT UNIQUE NOT NULL,
  pw_hash TEXT NOT NULL, pw_salt TEXT NOT NULL,
  nickname TEXT NOT NULL,
  anon_alias TEXT NOT NULL,             -- 固定匿名马甲
  identity TEXT NOT NULL,               -- 已认证学生/未认证访客/校友/管理员/导员
  credit INTEGER NOT NULL DEFAULT 80,
  exp INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tokens(
  token TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id), created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS posts(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id),
  board TEXT NOT NULL,                  -- treehole / qa / handbook ...
  title TEXT DEFAULT '',
  body TEXT NOT NULL,
  identity_mode TEXT NOT NULL,          -- real / alias / anon
  allow_comments INTEGER NOT NULL DEFAULT 1,
  expires_at TEXT,                      -- 可见期截止；NULL=永久
  likes INTEGER DEFAULT 0, views INTEGER DEFAULT 0, favs INTEGER DEFAULT 0,
  replies INTEGER DEFAULT 0, reports INTEGER DEFAULT 0, admin_weight REAL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS teams(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  owner_id INTEGER NOT NULL REFERENCES users(id),
  game TEXT NOT NULL, mode TEXT NOT NULL, rank_req TEXT NOT NULL,
  capacity INTEGER NOT NULL,
  schedule_type TEXT NOT NULL,          -- tonight / friday / weekly / custom
  departure_at TEXT NOT NULL,           -- 下一次发车时间 ISO
  voice TEXT NOT NULL, voice_link TEXT DEFAULT '',
  newbie TEXT NOT NULL, vibe TEXT NOT NULL,
  notes TEXT DEFAULT '',                -- 车头注意事项（上车后推送）
  remind_before INTEGER NOT NULL DEFAULT 30,  -- 发车前提醒分钟数（车头可改）
  remind_channels TEXT NOT NULL DEFAULT '邮件,站内',
  remind_sent INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS team_members(
  team_id INTEGER NOT NULL REFERENCES teams(id),
  user_id INTEGER NOT NULL REFERENCES users(id),
  remind_channels TEXT NOT NULL DEFAULT '邮件,站内',
  owner_notified INTEGER NOT NULL DEFAULT 0,  -- 上车 3 分钟后提醒车头
  joined_at TEXT NOT NULL,
  UNIQUE(team_id, user_id)
);
CREATE TABLE IF NOT EXISTS ratings(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  team_id INTEGER NOT NULL REFERENCES teams(id),
  rater_id INTEGER NOT NULL REFERENCES users(id),
  target_id INTEGER NOT NULL REFERENCES users(id),
  tag TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(team_id, rater_id, target_id, tag)
);
CREATE TABLE IF NOT EXISTS listings(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  seller_id INTEGER NOT NULL REFERENCES users(id),
  title TEXT NOT NULL, price REAL NOT NULL,
  condition TEXT NOT NULL, bought_at TEXT NOT NULL,
  flaws TEXT NOT NULL, bargain TEXT NOT NULL,
  place TEXT NOT NULL, escrow INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT '在售',
  views INTEGER DEFAULT 0, favs INTEGER DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS notifications(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id),
  content TEXT NOT NULL, is_read INTEGER DEFAULT 0, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS email_log(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  to_email TEXT NOT NULL, subject TEXT NOT NULL, body TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS credit_log(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id),
  delta INTEGER NOT NULL, reason TEXT NOT NULL, created_at TEXT NOT NULL
);
"""


def now() -> str:
    return datetime.now().isoformat(timespec="seconds")


def hash_pw(pw: str, salt: str) -> str:
    return hashlib.pbkdf2_hmac("sha256", pw.encode(), salt.encode(), 100_000).hex()


def next_departure(schedule_type: str, custom: Optional[str] = None) -> str:
    """时间机制：今晚8点 / 周五晚 / 长期固定队（每周） / 自定义"""
    n = datetime.now()
    if schedule_type == "tonight":
        t = n.replace(hour=20, minute=0, second=0, microsecond=0)
        if t < n:
            t += timedelta(days=1)
    elif schedule_type == "friday":
        t = n.replace(hour=19, minute=0, second=0, microsecond=0)
        t += timedelta(days=(4 - n.weekday()) % 7 or (7 if t < n else 0))
    elif schedule_type == "weekly":
        t = n.replace(hour=21, minute=30, second=0, microsecond=0)
        if t < n:
            t += timedelta(days=1)
    else:  # custom
        t = datetime.fromisoformat(custom) if custom else n + timedelta(hours=2)
    return t.isoformat(timespec="seconds")


def add_credit(conn, user_id: int, delta: int, reason: str):
    conn.execute("UPDATE users SET credit=MAX(0,MIN(100,credit+?)) WHERE id=?", (delta, user_id))
    conn.execute("INSERT INTO credit_log(user_id,delta,reason,created_at) VALUES(?,?,?,?)",
                 (user_id, delta, reason, now()))


def notify(conn, user_id: int, content: str, email: Optional[str] = None, subject: str = "梧桐墙通知"):
    conn.execute("INSERT INTO notifications(user_id,content,created_at) VALUES(?,?,?)",
                 (user_id, content, now()))
    if email:  # 模拟发信：写入 email_log；接真实邮箱时替换为 smtplib 发送
        conn.execute("INSERT INTO email_log(to_email,subject,body,created_at) VALUES(?,?,?,?)",
                     (email, subject, content, now()))


def normalize_game(name: str) -> str:
    """自提交游戏合并：Valorant / 瓦 / 无畏契约 → 瓦"""
    key = name.strip().lower()
    return GAME_ALIAS.get(key, name.strip())


def display_name(row) -> str:
    mode = row["identity_mode"]
    if mode == "anon":
        return "匿名同学"
    if mode == "alias":
        return row["anon_alias"]
    return row["nickname"]


# ---------------------------------------------------------------- 种子数据
def seed(conn):
    if conn.execute("SELECT COUNT(*) c FROM users").fetchone()["c"]:
        return
    demo_users = [
        ("laok@stu.demo.edu.cn", "车头_老K", "梧桐#1024", "已认证学生", 95),
        ("mixian@qq.com", "米线不要香菜", "梧桐#2048", "已认证学生", 84),
        ("books@stu.demo.edu.cn", "学长的旧书摊", "梧桐#4096", "已认证学生", 92),
        ("dm@gmail.com", "戴斯忒尼", "梧桐#8192", "校友", 88),
    ]
    for email, nick, alias, ident, credit in demo_users:
        salt = secrets.token_hex(8)
        conn.execute(
            "INSERT INTO users(email,pw_hash,pw_salt,nickname,anon_alias,identity,credit,created_at)"
            " VALUES(?,?,?,?,?,?,?,?)",
            (email, hash_pw("demo1234", salt), salt, nick, alias, ident, credit, now()))
    conn.execute(
        "INSERT INTO posts(user_id,board,body,identity_mode,allow_comments,expires_at,likes,views,replies,favs,created_at)"
        " VALUES(1,'treehole','在图书馆连坐了六小时，起来的时候腿是麻的，心是空的，但作业写完了。就想说一句：坚持住，各位。','anon',1,?,128,3200,23,45,?)",
        ((datetime.now() + timedelta(days=7)).isoformat(timespec="seconds"), now()))
    conn.execute(
        "INSERT INTO posts(user_id,board,body,identity_mode,allow_comments,likes,views,replies,favs,created_at)"
        " VALUES(2,'treehole','三食堂二楼新开的麻辣香锅，阿姨手不抖，是这个学期最大的惊喜。有想拼饭的可以评论区集合。','alias',1,156,2100,42,60,?)",
        (now(),))
    teams = [
        (1, "瓦", "竞技排位", "黄金~铂金", 5, "tonight", "KOOK", "欢迎新手", "娱乐上分两不误", "邮件,站内,QQ机器人"),
        (4, "雀魂", "友人房四麻", "不限", 4, "weekly", "QQ群语音", "欢迎新手", "欢乐局，输了不许摔牌", "邮件,QQ机器人"),
        (4, "DND", "长团·每周一次", "无需经验", 6, "friday", "KOOK", "欢迎新手（DM带教学）", "沉浸剧情向", "邮件,日历"),
    ]
    for owner, game, mode, rank, cap, sched, voice, nb, vibe, ch in teams:
        conn.execute(
            "INSERT INTO teams(owner_id,game,mode,rank_req,capacity,schedule_type,departure_at,voice,newbie,vibe,remind_channels,created_at)"
            " VALUES(?,?,?,?,?,?,?,?,?,?,?,?)",
            (owner, game, mode, rank, cap, sched, next_departure(sched), voice, nb, vibe, ch, now()))
        conn.execute("INSERT INTO team_members(team_id,user_id,remind_channels,joined_at) VALUES"
                     "((SELECT MAX(id) FROM teams),?, '邮件,站内', ?)", (owner, now()))
    conn.execute("INSERT INTO team_members(team_id,user_id,remind_channels,joined_at) VALUES(1,2,'邮件',?)", (now(),))
    conn.execute(
        "INSERT INTO listings(seller_id,title,price,condition,bought_at,flaws,bargain,place,escrow,views,favs,created_at)"
        " VALUES(3,'iPad 9（64G WiFi 版）',1450,'95 新，屏幕完美','2024-03','右下角轻微磕碰（见图3）','可小刀','东门快递站面交',1,1900,31,?)",
        (now(),))


with db() as _c:
    _c.executescript(SCHEMA)
    seed(_c)


# ---------------------------------------------------------------- 鉴权
def current_user(authorization: str = Header(default="")):
    if not authorization.startswith("Bearer "):
        raise HTTPException(401, "请先登录")
    with db() as conn:
        row = conn.execute(
            "SELECT u.* FROM tokens t JOIN users u ON u.id=t.user_id WHERE t.token=?",
            (authorization[7:],)).fetchone()
    if not row:
        raise HTTPException(401, "登录已过期，请重新登录")
    return dict(row)


def get_threshold(key: str, default: int) -> int:
    """权限阈值：管理员可在 settings 表中调整（perm_<key>），无记录时用默认值"""
    try:
        with db() as conn:
            r = conn.execute("SELECT value FROM settings WHERE key=?", ("perm_" + key,)).fetchone()
            if r:
                return int(r["value"])
    except Exception:
        pass
    return default


def require_credit(user, key: str):
    need = get_threshold(key, next(n for _, k, n in PERMISSIONS if k == key))
    if user["credit"] < need:
        raise HTTPException(403, f"信用分不足：该操作需要 ≥ {need}，你当前 {user['credit']}")


# ---------------------------------------------------------------- Schemas
class RegisterIn(BaseModel):
    email: str
    password: str = Field(min_length=6)
    nickname: str = Field(min_length=1, max_length=20)
    agreed: bool = False


class LoginIn(BaseModel):
    email: str
    password: str


class PostIn(BaseModel):
    board: str = "treehole"
    title: str = ""
    body: str = Field(min_length=1, max_length=2000)
    identity_mode: str = "anon"          # real / alias / anon
    visibility: str = "forever"          # 24h / 7d / forever
    allow_comments: bool = True


class TeamIn(BaseModel):
    game: str
    mode: str
    rank_req: str = "不限"
    capacity: int = Field(ge=2, le=99, default=5)
    schedule_type: str = "tonight"       # tonight / friday / weekly / custom
    custom_time: Optional[str] = None
    voice: str = "KOOK"
    voice_link: str = ""
    newbie: str = "欢迎新手"
    vibe: str = ""
    notes: str = ""                      # 注意事项（上车即推送给车友）
    remind_before: int = Field(ge=5, le=1440, default=30)
    remind_channels: str = "邮件,站内"


class JoinIn(BaseModel):
    remind_channels: str = "邮件,站内"


class RateIn(BaseModel):
    target_id: int
    tags: list[str]


class ListingIn(BaseModel):
    title: str
    price: float = Field(ge=0)
    condition: str
    bought_at: str = ""
    flaws: str = "无瑕疵"
    bargain: str = "可小刀"
    place: str = "东门快递站面交"
    escrow: bool = True


# ---------------------------------------------------------------- 认证接口
@app.post("/api/auth/register", tags=["认证"])
def register(data: RegisterIn):
    if not data.agreed:
        raise HTTPException(400, "请先阅读并勾选《用户协议》与《社区规范》")
    if not ALLOWED_MAIL.search(data.email.strip()):
        raise HTTPException(400, "仅支持 QQ 邮箱 / Gmail / edu.cn 校园邮箱注册")
    identity = "已认证学生" if data.email.strip().lower().endswith(".edu.cn") else "未认证访客"
    salt = secrets.token_hex(8)
    alias = f"梧桐#{secrets.randbelow(9000) + 1000}"
    with db() as conn:
        if conn.execute("SELECT 1 FROM users WHERE email=?", (data.email,)).fetchone():
            raise HTTPException(400, "该邮箱已注册")
        cur = conn.execute(
            "INSERT INTO users(email,pw_hash,pw_salt,nickname,anon_alias,identity,credit,created_at)"
            " VALUES(?,?,?,?,?,?,?,?)",
            (data.email.strip(), hash_pw(data.password, salt), salt, data.nickname, alias, identity, 80, now()))
        token = secrets.token_hex(24)
        conn.execute("INSERT INTO tokens VALUES(?,?,?)", (token, cur.lastrowid, now()))
        notify(conn, cur.lastrowid, f"欢迎加入梧桐墙！你的身份是「{identity}」，新用户处于观察期。", data.email, "欢迎加入梧桐墙")
    return {"token": token, "nickname": data.nickname, "identity": identity, "credit": 80}


@app.post("/api/auth/login", tags=["认证"])
def login(data: LoginIn):
    with db() as conn:
        u = conn.execute("SELECT * FROM users WHERE email=?", (data.email.strip(),)).fetchone()
        if not u or hash_pw(data.password, u["pw_salt"]) != u["pw_hash"]:
            raise HTTPException(401, "邮箱或密码错误")
        token = secrets.token_hex(24)
        conn.execute("INSERT INTO tokens VALUES(?,?,?)", (token, u["id"], now()))
    return {"token": token, "nickname": u["nickname"], "identity": u["identity"], "credit": u["credit"]}


@app.get("/api/me", tags=["认证"])
def me(user=Depends(current_user)):
    with db() as conn:
        tags = conn.execute(
            "SELECT tag, COUNT(*) c FROM ratings WHERE target_id=? GROUP BY tag", (user["id"],)).fetchall()
        log = conn.execute(
            "SELECT delta,reason,created_at FROM credit_log WHERE user_id=? ORDER BY id DESC LIMIT 10",
            (user["id"],)).fetchall()
    perms = [{"name": n, "key": k, "need": get_threshold(k, need), "ok": user["credit"] >= get_threshold(k, need)}
             for n, k, need in PERMISSIONS]
    return {"id": user["id"], "nickname": user["nickname"], "identity": user["identity"], "credit": user["credit"],
            "exp": user["exp"], "anon_alias": user["anon_alias"], "email": user["email"], "permissions": perms,
            "rating_tags": {r["tag"]: r["c"] for r in tags},
            "credit_log": [dict(r) for r in log]}


# ---------------------------------------------------------------- 树洞 / 帖子
@app.get("/api/posts", tags=["树洞"])
def list_posts(board: str = "treehole", limit: int = 20):
    with db() as conn:
        rows = conn.execute(
            "SELECT p.*, u.nickname, u.anon_alias FROM posts p JOIN users u ON u.id=p.user_id"
            " WHERE p.board=? AND (p.expires_at IS NULL OR p.expires_at>?)"
            " ORDER BY p.id DESC LIMIT ?", (board, now(), limit)).fetchall()
    out = []
    for r in rows:
        out.append({"id": r["id"], "title": r["title"], "body": r["body"],
                    "author": display_name(r), "identity_mode": r["identity_mode"],
                    "allow_comments": bool(r["allow_comments"]),
                    "expires_at": r["expires_at"], "likes": r["likes"], "replies": r["replies"],
                    "created_at": r["created_at"]})
    return out


@app.post("/api/posts", tags=["树洞"])
def create_post(data: PostIn, user=Depends(current_user)):
    if data.identity_mode == "anon":
        require_credit(user, "anon_post")
    hit_abuse = [w for w in ABUSE_WORDS if w in data.body]
    if hit_abuse:
        raise HTTPException(400, f"内容含辱骂词（{hit_abuse[0]}…），请修改后再发布")
    care = any(w in data.body for w in CARE_WORDS)
    expires = {"24h": datetime.now() + timedelta(hours=24),
               "7d": datetime.now() + timedelta(days=7)}.get(data.visibility)
    with db() as conn:
        cur = conn.execute(
            "INSERT INTO posts(user_id,board,title,body,identity_mode,allow_comments,expires_at,created_at)"
            " VALUES(?,?,?,?,?,?,?,?)",
            (user["id"], data.board, data.title, data.body, data.identity_mode,
             int(data.allow_comments), expires.isoformat(timespec="seconds") if expires else None, now()))
    return {"id": cur.lastrowid, "care": care,
            "care_message": "我们注意到你可能正在经历艰难的时刻。校心理健康中心（明德楼B203）与 24 小时心理援助热线 12356 随时可以联系。" if care else ""}


@app.post("/api/posts/{post_id}/like", tags=["树洞"])
def like_post(post_id: int):
    with db() as conn:
        conn.execute("UPDATE posts SET likes=likes+1 WHERE id=?", (post_id,))
        row = conn.execute("SELECT likes FROM posts WHERE id=?", (post_id,)).fetchone()
    if not row:
        raise HTTPException(404, "帖子不存在或已过期")
    return {"likes": row["likes"]}


# ---------------------------------------------------------------- 游戏车队
def team_dict(conn, r) -> dict:
    members = conn.execute(
        "SELECT u.id,u.nickname,u.credit FROM team_members m JOIN users u ON u.id=m.user_id WHERE m.team_id=?",
        (r["id"],)).fetchall()
    owner = conn.execute("SELECT nickname FROM users WHERE id=?", (r["owner_id"],)).fetchone()
    return {"id": r["id"], "game": r["game"], "mode": r["mode"], "rank_req": r["rank_req"],
            "capacity": r["capacity"], "schedule_type": r["schedule_type"],
            "departure_at": r["departure_at"], "voice": r["voice"], "voice_link": r["voice_link"],
            "newbie": r["newbie"], "vibe": r["vibe"], "remind_channels": r["remind_channels"],
            "notes": r["notes"], "remind_before": r["remind_before"],
            "owner": owner["nickname"], "members": [dict(m) for m in members],
            "seats": f"{len(members)}/{r['capacity']}"}


@app.get("/api/teams", tags=["车队"])
def list_teams(game: Optional[str] = None):
    with db() as conn:
        q = "SELECT * FROM teams ORDER BY departure_at"
        rows = conn.execute(q).fetchall()
        out = [team_dict(conn, r) for r in rows]
    if game:
        game = normalize_game(game)
        out = [t for t in out if t["game"] == game]
    return out


@app.post("/api/teams", tags=["车队"])
def create_team(data: TeamIn, user=Depends(current_user)):
    require_credit(user, "create_team")
    game = normalize_game(data.game)
    with db() as conn:
        cur = conn.execute(
            "INSERT INTO teams(owner_id,game,mode,rank_req,capacity,schedule_type,departure_at,voice,voice_link,newbie,vibe,notes,remind_before,remind_channels,created_at)"
            " VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
            (user["id"], game, data.mode, data.rank_req, data.capacity, data.schedule_type,
             next_departure(data.schedule_type, data.custom_time), data.voice, data.voice_link,
             data.newbie, data.vibe, data.notes, data.remind_before, data.remind_channels, now()))
        conn.execute("INSERT INTO team_members(team_id,user_id,remind_channels,joined_at) VALUES(?,?,?,?)",
                     (cur.lastrowid, user["id"], data.remind_channels, now()))
        row = conn.execute("SELECT * FROM teams WHERE id=?", (cur.lastrowid,)).fetchone()
        result = team_dict(conn, row)
    if game not in GAMES:
        result["note"] = f"「{game}」为自提交游戏，已进入管理员审核合并流程"
    return result


@app.post("/api/teams/{team_id}/join", tags=["车队"])
def join_team(team_id: int, data: JoinIn, user=Depends(current_user)):
    with db() as conn:
        team = conn.execute("SELECT * FROM teams WHERE id=?", (team_id,)).fetchone()
        if not team:
            raise HTTPException(404, "车队不存在")
        n = conn.execute("SELECT COUNT(*) c FROM team_members WHERE team_id=?", (team_id,)).fetchone()["c"]
        if n >= team["capacity"]:
            raise HTTPException(400, "车队已满员")
        try:
            conn.execute("INSERT INTO team_members(team_id,user_id,remind_channels,joined_at) VALUES(?,?,?,?)",
                         (team_id, user["id"], data.remind_channels, now()))
        except sqlite3.IntegrityError:
            raise HTTPException(400, "你已在车上")
        # 上车后立即把车队关键信息推送给车友（时间/语音/链接/注意事项）；车头由调度线程在 3 分钟后提醒
        dep_h = team["departure_at"][11:16]
        info = f"🎫 上车成功：{team['game']} · {team['mode']}，{dep_h} 发车，语音 {team['voice']}"
        if team["voice_link"]:
            info += f"，频道链接：{team['voice_link']}"
        if team["notes"]:
            info += f"。车头注意事项：{team['notes']}"
        notify(conn, user["id"], info, user["email"], f"【梧桐墙】{team['game']} 车队上车确认")
    return {"ok": True, "seats": f"{n + 1}/{team['capacity']}",
            "message": f"上车成功！发车前 {team['remind_before']} 分钟将通过（{data.remind_channels}）提醒你"}


@app.post("/api/teams/{team_id}/leave", tags=["车队"])
def leave_team(team_id: int, user=Depends(current_user)):
    with db() as conn:
        team = conn.execute("SELECT * FROM teams WHERE id=?", (team_id,)).fetchone()
        if not team:
            raise HTTPException(404, "车队不存在")
        cur = conn.execute("DELETE FROM team_members WHERE team_id=? AND user_id=?", (team_id, user["id"]))
        if cur.rowcount == 0:
            raise HTTPException(400, "你不在这个车队")
        dep = datetime.fromisoformat(team["departure_at"])
        penalty = 0
        excused = False
        try:
            excused = bool(conn.execute(
                "SELECT 1 FROM team_leaves WHERE team_id=? AND user_id=? AND departure_at=?",
                (team_id, user["id"], team["departure_at"])).fetchone())
        except Exception:
            pass
        # 发车前 30 分钟内退出且未请假 → 爽约扣分；提前请假可豁免
        if timedelta(0) < dep - datetime.now() < timedelta(minutes=30) and not excused:
            penalty = -3
            add_credit(conn, user["id"], penalty, f"发车前 30 分钟内退出 {team['game']} 车队（爽约）")
    return {"ok": True, "credit_delta": penalty,
            "message": "已下车" + ("，发车前 30 分钟内退出且未请假：信用 −3" if penalty else "")}


@app.post("/api/teams/{team_id}/checkin", tags=["车队"])
def checkin(team_id: int, user=Depends(current_user)):
    """发车时间前后 30 分钟内签到：准时 +1 信用"""
    with db() as conn:
        team = conn.execute("SELECT * FROM teams WHERE id=?", (team_id,)).fetchone()
        if not team:
            raise HTTPException(404, "车队不存在")
        dep = datetime.fromisoformat(team["departure_at"])
        if abs((dep - datetime.now()).total_seconds()) > 1800:
            raise HTTPException(400, "不在签到时间窗口（发车前后 30 分钟）")
        add_credit(conn, user["id"], 1, f"{team['game']} 车队准时签到")
    return {"ok": True, "credit_delta": 1}


@app.post("/api/teams/{team_id}/rate", tags=["车队"])
def rate_member(team_id: int, data: RateIn, user=Depends(current_user)):
    bad = [t for t in data.tags if t not in RATE_TAGS]
    if bad:
        raise HTTPException(400, f"评价标签仅限：{'/'.join(RATE_TAGS)}")
    if data.target_id == user["id"]:
        raise HTTPException(400, "不能评价自己")
    with db() as conn:
        in_team = conn.execute("SELECT 1 FROM team_members WHERE team_id=? AND user_id=?",
                               (team_id, user["id"])).fetchone()
        target_in = conn.execute("SELECT 1 FROM team_members WHERE team_id=? AND user_id=?",
                                 (team_id, data.target_id)).fetchone()
        if not (in_team and target_in):
            raise HTTPException(403, "只能评价同车队友")
        for tag in data.tags:
            conn.execute("INSERT OR IGNORE INTO ratings(team_id,rater_id,target_id,tag,created_at) VALUES(?,?,?,?,?)",
                         (team_id, user["id"], data.target_id, tag, now()))
    return {"ok": True}


# ---------------------------------------------------------------- 二手集市
@app.get("/api/market", tags=["集市"])
def list_market():
    with db() as conn:
        rows = conn.execute(
            "SELECT l.*, u.nickname, u.identity, u.credit,"
            " (SELECT COUNT(*) FROM listings WHERE seller_id=l.seller_id AND status='已成交') sold"
            " FROM listings l JOIN users u ON u.id=l.seller_id"
            " WHERE l.status='在售' ORDER BY l.id DESC").fetchall()
    return [{"id": r["id"], "title": r["title"], "price": r["price"], "condition": r["condition"],
             "bought_at": r["bought_at"], "flaws": r["flaws"], "bargain": r["bargain"],
             "place": r["place"], "escrow": bool(r["escrow"]), "views": r["views"],
             "seller": {"nickname": r["nickname"], "identity": r["identity"],
                        "credit": r["credit"], "sold": r["sold"]}} for r in rows]


@app.post("/api/market", tags=["集市"])
def create_listing(data: ListingIn, user=Depends(current_user)):
    require_credit(user, "trade")
    if user["identity"] == "未认证访客":
        raise HTTPException(403, "发布交易帖需要完成校园身份认证")
    hit = [w for w in BANNED_GOODS if w in data.title + data.flaws]
    if hit:
        with db() as conn:
            add_credit(conn, user["id"], -10, f"尝试发布禁售物品（{hit[0]}）")
        raise HTTPException(400, f"「{hit[0]}」属于禁售物品，本次操作已记录并扣除信用 10 分")
    with db() as conn:
        cur = conn.execute(
            "INSERT INTO listings(seller_id,title,price,condition,bought_at,flaws,bargain,place,escrow,created_at)"
            " VALUES(?,?,?,?,?,?,?,?,?,?)",
            (user["id"], data.title, data.price, data.condition, data.bought_at,
             data.flaws, data.bargain, data.place, int(data.escrow), now()))
    return {"id": cur.lastrowid, "escrow_fee": round(data.price * 0.02, 2) if data.escrow else 0}


# ---------------------------------------------------------------- 热榜 / 通知
@app.get("/api/hot", tags=["热榜"])
def hot():
    """热度 = (浏览×0.2 + 收藏×3 + 回复×2 + 点赞 − 举报×5) × 时间衰减 + 管理员权重"""
    items = []
    with db() as conn:
        for r in conn.execute("SELECT p.*, u.nickname, u.anon_alias FROM posts p JOIN users u ON u.id=p.user_id"
                              " WHERE p.expires_at IS NULL OR p.expires_at>?", (now(),)):
            hours = max(0.0, (datetime.now() - datetime.fromisoformat(r["created_at"])).total_seconds() / 3600)
            decay = 1 / (1 + hours / 24)
            score = (r["views"] * .2 + r["favs"] * 3 + r["replies"] * 2 + r["likes"] - r["reports"] * 5) * decay + r["admin_weight"]
            items.append({"type": "post", "id": r["id"], "title": r["title"] or r["body"][:24] + "…",
                          "meta": f"树洞 · 🔥{int(score)} · 💬{r['replies']}", "score": round(score, 1)})
        for t in conn.execute("SELECT * FROM teams"):
            n = conn.execute("SELECT COUNT(*) c FROM team_members WHERE team_id=?", (t["id"],)).fetchone()["c"]
            score = n * 20 + (50 if t["schedule_type"] == "tonight" else 20)
            items.append({"type": "team", "id": t["id"], "title": f"{t['game']} · {t['mode']}（{n}/{t['capacity']}）",
                          "meta": f"车队 · {t['departure_at'][11:16]} 发车", "score": score})
        for l in conn.execute("SELECT * FROM listings WHERE status='在售'"):
            score = l["views"] * .2 + l["favs"] * 3
            items.append({"type": "listing", "id": l["id"], "title": f"{l['title']} ¥{l['price']:g}",
                          "meta": f"集市 · 👁{l['views']}" + (" · 🛡️担保" if l["escrow"] else ""), "score": round(score, 1)})
    return sorted(items, key=lambda x: -x["score"])[:9]


@app.get("/api/notifications", tags=["通知"])
def notifications(user=Depends(current_user)):
    with db() as conn:
        rows = conn.execute("SELECT * FROM notifications WHERE user_id=? ORDER BY id DESC LIMIT 30",
                            (user["id"],)).fetchall()
    return [dict(r) for r in rows]


@app.get("/api/emails", tags=["通知"])
def emails():
    """演示用：查看模拟发出的提醒邮件（生产环境请移除或加管理员权限）"""
    with db() as conn:
        rows = conn.execute("SELECT * FROM email_log ORDER BY id DESC LIMIT 30").fetchall()
    return [dict(r) for r in rows]


# ---------------------------------------------------------------- 发车提醒调度线程
def reminder_loop():
    while True:
        try:
            with db() as conn:
                # 1) 发车前 N 分钟提醒（N=车头设置的 remind_before，默认 30）
                for t in conn.execute("SELECT * FROM teams WHERE remind_sent=0").fetchall():
                    dep = datetime.fromisoformat(t["departure_at"])
                    delta = (dep - datetime.now()).total_seconds()
                    if 0 < delta <= t["remind_before"] * 60:
                        members = conn.execute(
                            "SELECT m.user_id, m.remind_channels, u.email, u.nickname FROM team_members m"
                            " JOIN users u ON u.id=m.user_id WHERE m.team_id=?", (t["id"],)).fetchall()
                        excused_ids = set()
                        try:
                            excused_ids = {r["user_id"] for r in conn.execute(
                                "SELECT user_id FROM team_leaves WHERE team_id=? AND departure_at=?",
                                (t["id"], t["departure_at"]))}
                        except Exception:
                            pass
                        msg = (f"🚗 准备发车：你的「{t['game']} · {t['mode']}」车队将于 "
                               f"{dep.strftime('%H:%M')} 发车，语音：{t['voice']}。记得准时上号！")
                        if t["voice_link"]:
                            msg += f" 频道：{t['voice_link']}"
                        for m in members:
                            if m["user_id"] in excused_ids:
                                continue  # 已请假本次不提醒
                            notify(conn, m["user_id"], msg,
                                   m["email"] if "邮件" in m["remind_channels"] else None,
                                   f"【梧桐墙】{t['game']} 车队 {t['remind_before']} 分钟后发车")
                        conn.execute("UPDATE teams SET remind_sent=1 WHERE id=?", (t["id"],))
                    elif delta <= 0 and t["schedule_type"] in ("weekly", "friday"):
                        # 长期固定队 / 每周队：滚动到下一周期
                        conn.execute("UPDATE teams SET departure_at=?, remind_sent=0 WHERE id=?",
                                     ((dep + timedelta(days=7)).isoformat(timespec="seconds"), t["id"]))
                # 2) 上车 3 分钟后提醒车头
                for m in conn.execute(
                        "SELECT m.rowid rid, m.team_id, m.user_id, m.joined_at, t.owner_id, t.game, t.capacity,"
                        " u.nickname FROM team_members m JOIN teams t ON t.id=m.team_id"
                        " JOIN users u ON u.id=m.user_id"
                        " WHERE m.owner_notified=0 AND m.user_id != t.owner_id").fetchall():
                    if datetime.fromisoformat(m["joined_at"]) <= datetime.now() - timedelta(minutes=3):
                        n = conn.execute("SELECT COUNT(*) c FROM team_members WHERE team_id=?",
                                         (m["team_id"],)).fetchone()["c"]
                        notify(conn, m["owner_id"],
                               f"🎮 车友上车：「{m['nickname']}」加入了你的 {m['game']} 车队（{n}/{m['capacity']}）")
                        conn.execute("UPDATE team_members SET owner_notified=1 WHERE rowid=?", (m["rid"],))
        except Exception as e:  # 调度失败不中断服务
            print("[reminder]", e)
        time.sleep(20)


threading.Thread(target=reminder_loop, daemon=True).start()


# ---------------------------------------------------------------- 前端托管
@app.get("/", include_in_schema=False)
def index():
    if FRONTEND.exists():
        return FileResponse(FRONTEND)
    raise HTTPException(404, "frontend/index.html 不存在")


# ---------------------------------------------------------------- 扩展模块挂载
import extras  # noqa: E402
extras.register(app)
