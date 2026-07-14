from __future__ import annotations

import secrets

from sqlalchemy import select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from .models import Setting, ThreadAnonymousIdentity

DEFAULT_ANONYMOUS_NICKNAMES = (
    "丹阳子", "小狐狸", "欧阳牛马", "逍遥客", "青衫剑客", "云游道人", "长安故人", "江湖小虾",
    "桃花岛民", "月下书生", "竹林听雨", "白衣少侠", "北冥小鱼", "南山隐士", "星河旅人", "落霞居士",
    "橘子汽水", "苹果派", "草莓麻薯", "蓝莓贝果", "芒果布丁", "葡萄冻冻", "柚子茶", "蜜桃乌龙",
    "西瓜啵啵", "柠檬糖", "山楂雪球", "桂花糕", "红豆年糕", "芝士土豆", "海盐曲奇", "抹茶团子",
    "小浣熊", "雪地松鼠", "圆脸海豹", "长耳兔", "赤狐同学", "树懒学长", "水豚同学", "云朵羊",
    "月光水母", "深海鲸鱼", "银杏小鹿", "竹叶熊猫", "薄荷仓鼠", "夜行猫头鹰", "蒲公英刺猬", "海边企鹅",
    "火箭浣熊", "银河旅客", "木叶丸子", "魔法少女", "机甲小队长", "侦探小熊", "白帽骑士", "风之使者",
    "星星邮差", "月亮船长", "云端画师", "森林向导", "时间旅人", "纸飞机员", "深夜电台", "晨光信使",
    "青提奶盖", "椰子拿铁", "焦糖爆米花", "紫薯芋圆", "番茄锅底", "葱油拌面", "糯米烧麦", "香菇包子",
    "银杏叶子", "梧桐种子", "山茶花", "小雏菊", "薄荷叶", "四叶草", "蒲公英", "向日葵",
)


def normalize_nickname_pool(value: str) -> list[str]:
    names: list[str] = []
    seen: set[str] = set()
    for raw in value.splitlines():
        name = raw.strip()
        if not name or name in seen:
            continue
        if len(name) < 2 or len(name) > 20 or any(ord(char) < 32 for char in name):
            raise ValueError("匿名昵称每行需为 2–20 个字符，且不能包含控制字符")
        seen.add(name)
        names.append(name)
    if not names:
        raise ValueError("匿名昵称池至少需要一个昵称")
    if len(names) > 5000:
        raise ValueError("匿名昵称池最多支持 5000 个昵称")
    return names


def anonymous_nickname_pool(db: Session) -> list[str]:
    row = db.get(Setting, "anonymous_nickname_pool")
    if not row or not row.value.strip():
        return list(DEFAULT_ANONYMOUS_NICKNAMES)
    try:
        return normalize_nickname_pool(row.value)
    except ValueError:
        return list(DEFAULT_ANONYMOUS_NICKNAMES)


def _candidate_name(db: Session, thread_id: int, attempt: int) -> str:
    pool = anonymous_nickname_pool(db)
    used = set(
        db.scalars(
            select(ThreadAnonymousIdentity.display_name).where(ThreadAnonymousIdentity.thread_id == thread_id)
        ).all()
    )
    available = [name for name in pool if name not in used]
    if available:
        return secrets.choice(available)
    base = secrets.choice(pool)
    suffix = 2 + attempt
    return f"{base[: max(1, 37 - len(str(suffix)))]}·{suffix}"


def thread_anonymous_identity(db: Session, thread_id: int, user_id: int) -> str:
    existing = db.scalar(
        select(ThreadAnonymousIdentity).where(
            ThreadAnonymousIdentity.thread_id == thread_id,
            ThreadAnonymousIdentity.user_id == user_id,
        )
    )
    if existing:
        return existing.display_name

    for attempt in range(100):
        candidate = _candidate_name(db, thread_id, attempt)
        try:
            with db.begin_nested():
                row = ThreadAnonymousIdentity(thread_id=thread_id, user_id=user_id, display_name=candidate)
                db.add(row)
                db.flush()
            return candidate
        except IntegrityError:
            existing = db.scalar(
                select(ThreadAnonymousIdentity).where(
                    ThreadAnonymousIdentity.thread_id == thread_id,
                    ThreadAnonymousIdentity.user_id == user_id,
                )
            )
            if existing:
                return existing.display_name
    raise RuntimeError("无法分配树洞匿名昵称")
