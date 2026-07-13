"""Initial production schema.

Revision ID: 0001_production_schema
Revises:
"""
from alembic import op

from app import models  # noqa: F401
from app.database import Base


revision = "0001_production_schema"
down_revision = None
branch_labels = None
depends_on = None


def upgrade() -> None:
    bind = op.get_bind()
    Base.metadata.create_all(bind=bind)
    if bind.dialect.name == "postgresql":
        op.execute("CREATE EXTENSION IF NOT EXISTS pg_trgm")
        op.execute("CREATE INDEX IF NOT EXISTS ix_posts_title_trgm ON posts USING gin (title gin_trgm_ops)")
        op.execute("CREATE INDEX IF NOT EXISTS ix_questions_title_trgm ON questions USING gin (title gin_trgm_ops)")
        op.execute("CREATE INDEX IF NOT EXISTS ix_listings_title_trgm ON listings USING gin (title gin_trgm_ops)")


def downgrade() -> None:
    Base.metadata.drop_all(bind=op.get_bind())

