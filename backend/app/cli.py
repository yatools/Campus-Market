from __future__ import annotations

import argparse
import getpass
import os

from sqlalchemy import select

from .config import get_settings
from .database import SessionLocal
from .models import User, utcnow
from .security import hash_password, new_alias
from .services import normalize_email


def create_admin(email: str, nickname: str) -> None:
    password = os.environ.get("INITIAL_ADMIN_PASSWORD") or getpass.getpass("管理员初始密码（至少 12 位）：")
    if len(password) < 12:
        raise SystemExit("管理员密码至少需要 12 位")
    email = normalize_email(email)
    with SessionLocal() as db:
        if db.scalar(select(User.id).where(User.email == email)):
            raise SystemExit("该邮箱已存在")
        user = User(
            email=email,
            password_hash=hash_password(password),
            nickname=nickname.strip(),
            alias=new_alias(),
            campus_identity="staff",
            role="admin",
            credit=100,
            verified_at=utcnow(),
        )
        db.add(user)
        db.commit()
        print(f"管理员已创建：id={user.id} email={email}")


def verify_config() -> None:
    settings = get_settings()
    print(f"environment={settings.environment}")
    print(f"origin={settings.public_origin}")
    print(f"database={settings.database_url.split('@')[-1]}")
    print(f"campus_domains={','.join(sorted(settings.campus_domains))}")
    print("配置校验通过")


def main() -> None:
    parser = argparse.ArgumentParser(description="梧桐墙运维命令")
    sub = parser.add_subparsers(dest="command", required=True)
    admin = sub.add_parser("create-admin")
    admin.add_argument("--email", required=True)
    admin.add_argument("--nickname", default="站点管理员")
    sub.add_parser("verify-config")
    args = parser.parse_args()
    if args.command == "create-admin":
        create_admin(args.email, args.nickname)
    elif args.command == "verify-config":
        verify_config()


if __name__ == "__main__":
    main()
