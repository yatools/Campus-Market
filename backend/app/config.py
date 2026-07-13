from __future__ import annotations

from functools import lru_cache
from pathlib import Path

from pydantic import Field, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

BASE_DIR = Path(__file__).resolve().parents[1]


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=(BASE_DIR.parent / ".env", BASE_DIR.parent / ".env.local"),
        env_file_encoding="utf-8",
        extra="ignore",
    )

    environment: str = "development"
    app_name: str = "梧桐墙"
    api_prefix: str = "/api/v1"
    public_origin: str = "http://localhost:5173"
    secret_key: str = "development-only-change-me"
    database_url: str = f"sqlite:///{(BASE_DIR / 'campus.db').as_posix()}"
    db_pool_size: int = Field(default=10, ge=1, le=50)
    db_max_overflow: int = Field(default=10, ge=0, le=50)
    auto_create_schema: bool = True
    allowed_campus_email_domains: str = "stu.example.edu.cn,example.edu.cn"
    session_cookie_name: str = "wutong_session"
    csrf_cookie_name: str = "wutong_csrf"
    cookie_secure: bool = False
    session_sliding_days: int = 7
    session_absolute_days: int = 30
    session_rotation_hours: int = 24
    upload_dir: Path = BASE_DIR / "uploads"
    backup_dir: Path = BASE_DIR / "backups"
    max_upload_mb: int = 8
    smtp_host: str = ""
    smtp_port: int = 465
    smtp_username: str = ""
    smtp_password: str = ""
    smtp_from: str = ""
    smtp_use_ssl: bool = True
    worker_poll_seconds: int = 10
    log_level: str = "INFO"
    docs_enabled: bool = True
    trusted_hosts: str = "localhost,127.0.0.1,testserver"

    @property
    def campus_domains(self) -> set[str]:
        return {x.strip().lower() for x in self.allowed_campus_email_domains.split(",") if x.strip()}

    @property
    def origins(self) -> list[str]:
        return [self.public_origin.rstrip("/")]

    @property
    def host_list(self) -> list[str]:
        return [x.strip() for x in self.trusted_hosts.split(",") if x.strip()]

    @model_validator(mode="after")
    def validate_production(self) -> Settings:
        if self.environment == "production":
            missing: list[str] = []
            secret_lower = self.secret_key.lower()
            if len(self.secret_key) < 32 or "change-me" in secret_lower or "replace-with" in secret_lower:
                missing.append("SECRET_KEY（至少 32 字符）")
            if not self.database_url.startswith(("postgresql://", "postgresql+psycopg://")) or "replace-with" in self.database_url:
                missing.append("DATABASE_URL（PostgreSQL）")
            if not self.public_origin.startswith("https://") or "example.edu.cn" in self.public_origin:
                missing.append("PUBLIC_ORIGIN（HTTPS）")
            if not self.campus_domains or self.campus_domains <= {"stu.example.edu.cn", "example.edu.cn"}:
                missing.append("ALLOWED_CAMPUS_EMAIL_DOMAINS")
            for name, value in (
                ("SMTP_HOST", self.smtp_host),
                ("SMTP_USERNAME", self.smtp_username),
                ("SMTP_PASSWORD", self.smtp_password),
                ("SMTP_FROM", self.smtp_from),
            ):
                if not value or "replace-with" in value.lower() or "example.com" in value.lower():
                    missing.append(name)
            if missing:
                raise ValueError("生产配置缺失：" + "、".join(missing))
            self.cookie_secure = True
            self.auto_create_schema = False
            self.docs_enabled = False
        return self


@lru_cache
def get_settings() -> Settings:
    return Settings()
