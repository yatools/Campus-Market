from __future__ import annotations

import asyncio
import json
import logging
import secrets
import time
import uuid
from contextlib import asynccontextmanager
from datetime import UTC, datetime

from fastapi import Depends, FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse, StreamingResponse
from fastapi.staticfiles import StaticFiles
from sqlalchemy import func, select, text
from starlette.middleware.trustedhost import TrustedHostMiddleware

from . import models  # noqa: F401 - 注册 SQLAlchemy metadata
from .config import get_settings
from .database import Base, SessionLocal, engine
from .deps import current_user, get_session_record
from .errors import install_error_handlers
from .models import Notification, SessionRecord, User, utcnow
from .routes import auth, campus_services, content, credit, feed, games, governance, me_admin, modules, teams
from .security import token_hash

settings = get_settings()


class JsonLogFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        payload = {
            "time": datetime.now(UTC).isoformat(),
            "level": record.levelname,
            "logger": record.name,
            "message": record.getMessage(),
        }
        if record.exc_info:
            payload["exception"] = self.formatException(record.exc_info)
        return json.dumps(payload, ensure_ascii=False)


handler = logging.StreamHandler()
handler.setFormatter(JsonLogFormatter())
logging.basicConfig(
    level=getattr(logging, settings.log_level.upper(), logging.INFO),
    handlers=[handler],
    force=True,
)
logger = logging.getLogger("wutong")


@asynccontextmanager
async def lifespan(_: FastAPI):
    settings.upload_dir.mkdir(parents=True, exist_ok=True)
    settings.backup_dir.mkdir(parents=True, exist_ok=True)
    if settings.auto_create_schema:
        Base.metadata.create_all(bind=engine)
    yield


app = FastAPI(
    title="梧桐墙 API",
    version="1.0.0",
    docs_url="/docs" if settings.docs_enabled else None,
    redoc_url="/redoc" if settings.docs_enabled else None,
    openapi_url="/openapi.json" if settings.docs_enabled else None,
    lifespan=lifespan,
)
install_error_handlers(app)

app.add_middleware(
    CORSMiddleware,
    allow_origins=settings.origins,
    allow_credentials=True,
    allow_methods=["GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"],
    allow_headers=["Content-Type", "X-CSRF-Token", "X-Request-ID"],
)
app.add_middleware(TrustedHostMiddleware, allowed_hosts=settings.host_list)


@app.middleware("http")
async def request_context(request: Request, call_next):
    request_id = request.headers.get("x-request-id", "")[:64] or uuid.uuid4().hex
    request.state.request_id = request_id
    started = time.perf_counter()
    response = await call_next(request)
    elapsed_ms = (time.perf_counter() - started) * 1000
    response.headers["X-Request-ID"] = request_id
    response.headers["X-Content-Type-Options"] = "nosniff"
    response.headers["Referrer-Policy"] = "strict-origin-when-cross-origin"
    response.headers["Permissions-Policy"] = "camera=(), microphone=(), geolocation=()"
    if elapsed_ms >= 800:
        logger.warning(
            "slow_request method=%s path=%s elapsed_ms=%.1f request_id=%s",
            request.method,
            request.url.path,
            elapsed_ms,
            request_id,
        )
    return response


CSRF_EXEMPT = {
    f"{settings.api_prefix}/auth/request-code",
    f"{settings.api_prefix}/auth/register",
    f"{settings.api_prefix}/auth/login",
    f"{settings.api_prefix}/auth/reset-password",
}


@app.middleware("http")
async def csrf_protection(request: Request, call_next):
    if request.method in {"POST", "PUT", "PATCH", "DELETE"} and request.url.path.startswith(settings.api_prefix):
        raw_session = request.cookies.get(settings.session_cookie_name, "")
        if raw_session and request.url.path not in CSRF_EXEMPT:
            cookie_token = request.cookies.get(settings.csrf_cookie_name, "")
            header_token = request.headers.get("x-csrf-token", "")
            with SessionLocal() as db:
                session = db.scalar(select(SessionRecord).where(SessionRecord.token_hash == token_hash(raw_session)))
                valid = bool(
                    session
                    and not session.revoked_at
                    and session.expires_at > utcnow()
                    and cookie_token
                    and header_token
                    and secrets.compare_digest(cookie_token, header_token)
                    and secrets.compare_digest(session.csrf_token, header_token)
                )
            if not valid:
                return JSONResponse(
                    status_code=403,
                    content={
                        "code": "CSRF_INVALID",
                        "message": "安全校验失败，请刷新页面后重试",
                        "field_errors": {},
                        "request_id": getattr(request.state, "request_id", ""),
                    },
                )
    return await call_next(request)


api_routers = [
    auth.router,
    content.router,
    credit.router,
    credit.admin_router,
    feed.router,
    teams.router,
    games.router,
    games.admin_router,
    campus_services.router,
    campus_services.admin_router,
    modules.router,
    governance.router,
    me_admin.me_router,
    me_admin.admin_router,
]
for router in api_routers:
    app.include_router(router, prefix=settings.api_prefix)


@app.get("/health/live", include_in_schema=False)
def health_live() -> dict:
    return {"status": "ok", "service": "api", "version": "1.0.0"}


@app.get("/health/ready", include_in_schema=False)
def health_ready() -> dict:
    with SessionLocal() as db:
        db.execute(text("SELECT 1"))
    return {"status": "ready"}


@app.get(f"{settings.api_prefix}/notifications/stream", include_in_schema=False)
async def notification_stream(
    user: User = Depends(current_user),
    record: SessionRecord = Depends(get_session_record),
):
    user_id = user.id
    session_id = record.id

    async def events():
        last_count: int | None = None
        while True:
            with SessionLocal() as db:
                active_session = db.get(SessionRecord, session_id)
                now = utcnow()
                if (
                    not active_session
                    or active_session.revoked_at
                    or active_session.expires_at <= now
                    or active_session.absolute_expires_at <= now
                ):
                    break
                count = (
                    db.scalar(
                        select(func.count(Notification.id)).where(
                            Notification.user_id == user_id, Notification.read_at.is_(None)
                        )
                    )
                    or 0
                )
            if count != last_count:
                yield f"event: unread\ndata: {json.dumps({'count': count})}\n\n"
                last_count = count
            else:
                yield ": keepalive\n\n"
            await asyncio.sleep(15)

    return StreamingResponse(events(), media_type="text/event-stream", headers={"Cache-Control": "no-cache"})


settings.upload_dir.mkdir(parents=True, exist_ok=True)
app.mount("/uploads", StaticFiles(directory=settings.upload_dir), name="uploads")
