from __future__ import annotations

import hashlib
import os
import shutil
import smtplib
import subprocess
import time
import zipfile
from datetime import timedelta
from email.message import EmailMessage
from pathlib import Path

from sqlalchemy import delete, func, select, text
from sqlalchemy.engine import make_url
from sqlalchemy.orm import Session

from .config import get_settings
from .database import SessionLocal
from .models import (
    Attachment,
    BackupJob,
    ContentEntity,
    EmailOutbox,
    Message,
    Notification,
    Post,
    RateLimitEvent,
    SessionRecord,
    Team,
    TeamMembership,
    TeamRun,
    TeamRunMember,
    User,
    VerificationCode,
    utcnow,
)
from .security import hash_password
from .services import enqueue_email, notify

settings = get_settings()
LOCK_ID = 846_208_411


def send_email(row: EmailOutbox) -> None:
    message = EmailMessage()
    message["From"] = settings.smtp_from
    message["To"] = row.to_email
    message["Subject"] = row.subject
    message.set_content(row.body)
    if settings.smtp_use_ssl:
        with smtplib.SMTP_SSL(settings.smtp_host, settings.smtp_port, timeout=20) as smtp:
            smtp.login(settings.smtp_username, settings.smtp_password)
            smtp.send_message(message)
    else:
        with smtplib.SMTP(settings.smtp_host, settings.smtp_port, timeout=20) as smtp:
            smtp.starttls()
            smtp.login(settings.smtp_username, settings.smtp_password)
            smtp.send_message(message)


def process_email(db: Session) -> None:
    rows = db.scalars(
        select(EmailOutbox)
        .where(EmailOutbox.status == "pending", EmailOutbox.next_attempt_at <= utcnow())
        .order_by(EmailOutbox.id)
        .limit(10)
        .with_for_update(skip_locked=True)
    ).all()
    for row in rows:
        try:
            send_email(row)
            row.status = "sent"
            row.sent_at = utcnow()
            # 验证码不在发信日志中长期留存。
            row.body = "[邮件正文发送后已清除]"
            row.last_error = ""
        except Exception as exc:  # noqa: BLE001 - worker 必须记录并重试
            row.attempts += 1
            row.last_error = str(exc)[:2000]
            if row.attempts >= 5:
                row.status = "failed"
            else:
                row.next_attempt_at = utcnow() + timedelta(minutes=2**row.attempts)


def process_team_runs(db: Session) -> None:
    runs = db.scalars(
        select(TeamRun).where(TeamRun.status == "scheduled").order_by(TeamRun.starts_at).with_for_update()
    ).all()
    now = utcnow()
    for run in runs:
        team = db.get(Team, run.team_id)
        if not team:
            continue
        if not run.reminder_sent_at and now < run.starts_at <= now + timedelta(minutes=team.reminder_minutes):
            members = db.scalars(
                select(TeamRunMember).where(
                    TeamRunMember.run_id == run.id,
                    TeamRunMember.status.in_(["joined", "checked_in"]),
                )
            ).all()
            for member in members:
                user = db.get(User, member.user_id)
                if not user:
                    continue
                body = f"{team.game} · {team.mode} 将于 {run.starts_at:%m-%d %H:%M} 发车。"
                notify(db, user.id, "车队即将发车", body, f"/teams/{team.entity_id}", "team")
                if user.email:
                    enqueue_email(db, user.email, "【梧桐墙】车队发车提醒", body)
            run.reminder_sent_at = now
        if run.starts_at + timedelta(hours=2) <= now:
            run.status = "completed"
            if team.recurrence == "weekly" and team.status == "active":
                next_start = run.starts_at + timedelta(days=7)
                next_run = db.scalar(
                    select(TeamRun).where(TeamRun.team_id == team.entity_id, TeamRun.starts_at == next_start)
                )
                if not next_run:
                    next_run = TeamRun(team_id=team.entity_id, starts_at=next_start)
                    db.add(next_run)
                    db.flush()
                    members = db.scalars(
                        select(TeamMembership).where(
                            TeamMembership.team_id == team.entity_id, TeamMembership.status == "active"
                        )
                    ).all()
                    db.add_all([TeamRunMember(run_id=next_run.id, user_id=x.user_id) for x in members])


def cleanup(db: Session) -> None:
    now = utcnow()
    expired_posts = db.execute(
        select(ContentEntity, Post)
        .join(Post, Post.entity_id == ContentEntity.id)
        .where(Post.expires_at.is_not(None), Post.expires_at <= now, ContentEntity.status == "published")
        .with_for_update()
    ).all()
    for entity, _ in expired_posts:
        entity.status = "expired"
        entity.search_visible = False

    old_uploads = db.scalars(
        select(Attachment).where(Attachment.status == "pending", Attachment.created_at <= now - timedelta(hours=24))
    ).all()
    for attachment in old_uploads:
        for relative in (attachment.path, attachment.thumbnail_path):
            if not relative:
                continue
            path = (settings.upload_dir / relative).resolve()
            if settings.upload_dir.resolve() in path.parents and path.is_file():
                path.unlink()
        db.delete(attachment)

    users = db.scalars(
        select(User).where(
            User.status == "disabled",
            User.deactivated_at.is_not(None),
            User.deactivated_at <= now - timedelta(days=30),
        )
    ).all()
    for user in users:
        user.email = None
        user.nickname = "已注销用户"
        user.alias = f"deleted-{user.id}"
        user.avatar_path = None
        user.password_hash = hash_password(os.urandom(32).hex())
        user.status = "deleted"
        message_rows = db.execute(
            select(ContentEntity, Message)
            .join(Message, Message.entity_id == ContentEntity.id)
            .where(ContentEntity.owner_id == user.id)
        ).all()
        for entity, message in message_rows:
            entity.status = "deleted"
            message.body = ""
        db.execute(delete(Notification).where(Notification.user_id == user.id))

    db.execute(delete(VerificationCode).where(VerificationCode.expires_at <= now - timedelta(days=1)))
    db.execute(delete(RateLimitEvent).where(RateLimitEvent.created_at <= now - timedelta(days=2)))
    db.execute(
        delete(SessionRecord).where(
            (SessionRecord.absolute_expires_at <= now - timedelta(days=30))
            | (SessionRecord.revoked_at <= now - timedelta(days=30))
        )
    )


def create_backup_archive(job: BackupJob) -> Path:
    settings.backup_dir.mkdir(parents=True, exist_ok=True)
    stamp = utcnow().strftime("%Y%m%d-%H%M%S")
    work = settings.backup_dir / f"job-{job.id}-{stamp}"
    work.mkdir(parents=True, exist_ok=True)
    dump_path = work / "database.dump"
    if settings.database_url.startswith("sqlite:///"):
        source = Path(settings.database_url.removeprefix("sqlite:///"))
        shutil.copy2(source, dump_path)
    else:
        pg_url = make_url(settings.database_url)
        subprocess.run(
            [
                "pg_dump",
                "--format=custom",
                "--file",
                str(dump_path),
                "--host",
                pg_url.host or "db",
                "--port",
                str(pg_url.port or 5432),
                "--username",
                pg_url.username or "",
                "--dbname",
                pg_url.database or "",
            ],
            check=True,
            timeout=600,
            env={**os.environ, "PGCONNECT_TIMEOUT": "10", "PGPASSWORD": pg_url.password or ""},
        )
    archive = settings.backup_dir / f"wutong-backup-{stamp}.zip"
    checksums: list[str] = []

    def add_file(zf: zipfile.ZipFile, source: Path, archive_name: str) -> None:
        digest = hashlib.sha256()
        with source.open("rb") as handle:
            for chunk in iter(lambda: handle.read(1024 * 1024), b""):
                digest.update(chunk)
        zf.write(source, archive_name)
        checksums.append(f"{digest.hexdigest()}  {archive_name}")

    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=6) as zf:
        add_file(zf, dump_path, "database.dump")
        if settings.upload_dir.exists():
            for path in settings.upload_dir.rglob("*"):
                if path.is_file():
                    add_file(zf, path, (Path("uploads") / path.relative_to(settings.upload_dir)).as_posix())
        zf.writestr("SHA256SUMS", "\n".join(checksums) + "\n")
    shutil.rmtree(work)
    return archive


def process_backups(db: Session) -> None:
    job = db.scalar(
        select(BackupJob)
        .where(BackupJob.status == "pending")
        .order_by(BackupJob.created_at)
        .limit(1)
        .with_for_update(skip_locked=True)
    )
    if job:
        job.status = "running"
        db.commit()
        try:
            archive = create_backup_archive(job)
            job.status = "ready"
            job.file_path = str(archive)
            job.finished_at = utcnow()
        except Exception as exc:  # noqa: BLE001
            job.status = "failed"
            job.error = str(exc)[:4000]
            job.finished_at = utcnow()
    ready = db.scalars(
        select(BackupJob).where(BackupJob.status == "ready").order_by(BackupJob.finished_at.desc())
    ).all()
    for old in ready[7:]:
        path = Path(old.file_path)
        if path.is_file() and settings.backup_dir.resolve() in path.resolve().parents:
            path.unlink()
        old.status = "expired"
        old.file_path = ""


def cycle() -> None:
    with SessionLocal() as db:
        locked = True
        if not settings.database_url.startswith("sqlite"):
            locked = bool(db.scalar(select(func.pg_try_advisory_lock(LOCK_ID))))
        if not locked:
            return
        try:
            process_email(db)
            process_team_runs(db)
            cleanup(db)
            process_backups(db)
            db.commit()
        except Exception:
            db.rollback()
            raise
        finally:
            if not settings.database_url.startswith("sqlite"):
                db.execute(text(f"SELECT pg_advisory_unlock({LOCK_ID})"))
                db.commit()


def main() -> None:
    while True:
        try:
            cycle()
        except Exception as exc:  # noqa: BLE001
            print(f"[worker] {exc}", flush=True)
        time.sleep(settings.worker_poll_seconds)


if __name__ == "__main__":
    main()
