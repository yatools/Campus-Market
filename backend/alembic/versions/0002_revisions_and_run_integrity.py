"""Add content revisions and team-run concurrency integrity.

Revision ID: 0002_revisions_and_run_integrity
Revises: 0001_production_schema
"""

import sqlalchemy as sa

from alembic import op

revision = "0002_revisions_and_run_integrity"
down_revision = "0001_production_schema"
branch_labels = None
depends_on = None


def upgrade() -> None:
    bind = op.get_bind()
    inspector = sa.inspect(bind)
    if "content_revisions" not in inspector.get_table_names():
        op.create_table(
            "content_revisions",
            sa.Column("id", sa.BigInteger(), autoincrement=True, nullable=False),
            sa.Column("entity_id", sa.BigInteger(), nullable=False),
            sa.Column("editor_id", sa.BigInteger(), nullable=False),
            sa.Column("revision", sa.Integer(), nullable=False),
            sa.Column("title", sa.String(length=160), nullable=False, server_default=""),
            sa.Column("body", sa.Text(), nullable=False),
            sa.Column("created_at", sa.DateTime(timezone=True), nullable=False),
            sa.ForeignKeyConstraint(["editor_id"], ["users.id"], ondelete="RESTRICT"),
            sa.ForeignKeyConstraint(["entity_id"], ["content_entities.id"], ondelete="CASCADE"),
            sa.PrimaryKeyConstraint("id"),
            sa.UniqueConstraint("entity_id", "revision", name="uq_content_revision"),
        )
        op.create_index("ix_content_revisions_entity_id", "content_revisions", ["entity_id"])
        op.create_index("ix_content_revisions_editor_id", "content_revisions", ["editor_id"])
    unique_names = {row["name"] for row in inspector.get_unique_constraints("team_runs")}
    if "uq_team_run_start" not in unique_names:
        with op.batch_alter_table("team_runs") as batch_op:
            batch_op.create_unique_constraint("uq_team_run_start", ["team_id", "starts_at"])

    if bind.dialect.name == "postgresql":
        op.execute("CREATE INDEX IF NOT EXISTS ix_posts_body_trgm ON posts USING gin (body gin_trgm_ops)")
        op.execute("CREATE INDEX IF NOT EXISTS ix_handbook_title_trgm ON handbook_articles USING gin (title gin_trgm_ops)")
        op.execute("CREATE INDEX IF NOT EXISTS ix_listings_description_trgm ON listings USING gin (description gin_trgm_ops)")


def downgrade() -> None:
    bind = op.get_bind()
    if bind.dialect.name == "postgresql":
        op.execute("DROP INDEX IF EXISTS ix_listings_description_trgm")
        op.execute("DROP INDEX IF EXISTS ix_handbook_title_trgm")
        op.execute("DROP INDEX IF EXISTS ix_posts_body_trgm")
    with op.batch_alter_table("team_runs") as batch_op:
        batch_op.drop_constraint("uq_team_run_start", type_="unique")
    op.drop_index("ix_content_revisions_editor_id", table_name="content_revisions")
    op.drop_index("ix_content_revisions_entity_id", table_name="content_revisions")
    op.drop_table("content_revisions")
