from __future__ import annotations

from logging.config import fileConfig

from sqlalchemy import engine_from_config, pool

from alembic import context
from app import models  # noqa: F401
from app.config import get_settings
from app.database import Base

config = context.config
if config.config_file_name is not None:
    fileConfig(config.config_file_name)
config.set_main_option("sqlalchemy.url", get_settings().database_url.replace("%", "%%"))
target_metadata = Base.metadata

# These PostgreSQL-only GIN indexes are created explicitly by the migrations
# because they depend on the pg_trgm extension. They are intentionally absent
# from the cross-dialect SQLAlchemy metadata, so autogenerate must not treat
# their reflected database definitions as indexes to remove.
TRIGRAM_INDEXES = {
    "ix_handbook_title_trgm",
    "ix_listings_description_trgm",
    "ix_listings_title_trgm",
    "ix_posts_body_trgm",
    "ix_posts_title_trgm",
    "ix_questions_title_trgm",
}


def include_object(object_, name: str | None, type_: str, reflected: bool, compare_to: object | None) -> bool:
    if type_ == "index" and reflected and compare_to is None and name in TRIGRAM_INDEXES:
        return False
    return True


def run_migrations_offline() -> None:
    context.configure(
        url=config.get_main_option("sqlalchemy.url"),
        target_metadata=target_metadata,
        literal_binds=True,
        dialect_opts={"paramstyle": "named"},
        compare_type=True,
        include_object=include_object,
    )
    with context.begin_transaction():
        context.run_migrations()


def run_migrations_online() -> None:
    connectable = engine_from_config(
        config.get_section(config.config_ini_section, {}),
        prefix="sqlalchemy.",
        poolclass=pool.NullPool,
    )
    with connectable.connect() as connection:
        context.configure(
            connection=connection,
            target_metadata=target_metadata,
            compare_type=True,
            include_object=include_object,
        )
        with context.begin_transaction():
            context.run_migrations()


if context.is_offline_mode():
    run_migrations_offline()
else:
    run_migrations_online()
