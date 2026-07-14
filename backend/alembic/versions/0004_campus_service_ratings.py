"""Add real campus service ratings.

Revision ID: 0004_campus_service_ratings
Revises: 0003_v4_games_teams_and_listings
"""

import sqlalchemy as sa

from alembic import op

revision = "0004_campus_service_ratings"
down_revision = "0003_v4_games_teams_and_listings"
branch_labels = None
depends_on = None


def upgrade() -> None:
    inspector = sa.inspect(op.get_bind())
    tables = set(inspector.get_table_names())
    bigint = sa.BigInteger().with_variant(sa.Integer(), "sqlite")
    if "campus_services" not in tables:
        op.create_table(
            "campus_services",
            sa.Column("id", bigint, autoincrement=True, nullable=False),
            sa.Column("name", sa.String(length=160), nullable=False),
            sa.Column("category", sa.String(length=60), nullable=False, server_default="校园服务"),
            sa.Column("manager_user_id", bigint),
            sa.Column("active", sa.Boolean(), nullable=False, server_default=sa.true()),
            sa.Column("created_at", sa.DateTime(timezone=True), nullable=False),
            sa.Column("updated_at", sa.DateTime(timezone=True), nullable=False),
            sa.ForeignKeyConstraint(["manager_user_id"], ["users.id"], ondelete="SET NULL"),
            sa.PrimaryKeyConstraint("id"),
            sa.UniqueConstraint("name"),
        )
        op.create_index("ix_campus_services_name", "campus_services", ["name"])
        op.create_index("ix_campus_services_category", "campus_services", ["category"])
        op.create_index("ix_campus_services_manager_user_id", "campus_services", ["manager_user_id"])
        op.create_index("ix_campus_services_active", "campus_services", ["active"])
    if "campus_service_ratings" not in tables:
        op.create_table(
            "campus_service_ratings",
            sa.Column("id", bigint, autoincrement=True, nullable=False),
            sa.Column("service_id", bigint, nullable=False),
            sa.Column("user_id", bigint, nullable=False),
            sa.Column("rating", sa.Integer(), nullable=False),
            sa.Column("body", sa.Text(), nullable=False, server_default=""),
            sa.Column("response", sa.Text(), nullable=False, server_default=""),
            sa.Column("responder_id", bigint),
            sa.Column("responded_at", sa.DateTime(timezone=True)),
            sa.Column("created_at", sa.DateTime(timezone=True), nullable=False),
            sa.Column("updated_at", sa.DateTime(timezone=True), nullable=False),
            sa.CheckConstraint("rating >= 1 AND rating <= 5", name="ck_campus_service_rating"),
            sa.ForeignKeyConstraint(["responder_id"], ["users.id"], ondelete="SET NULL"),
            sa.ForeignKeyConstraint(["service_id"], ["campus_services.id"], ondelete="CASCADE"),
            sa.ForeignKeyConstraint(["user_id"], ["users.id"], ondelete="CASCADE"),
            sa.PrimaryKeyConstraint("id"),
        )
        op.create_index("ix_campus_service_ratings_service_id", "campus_service_ratings", ["service_id"])
        op.create_index("ix_campus_service_ratings_user_id", "campus_service_ratings", ["user_id"])
        op.create_index("ix_campus_service_ratings_created_at", "campus_service_ratings", ["created_at"])


def downgrade() -> None:
    inspector = sa.inspect(op.get_bind())
    tables = set(inspector.get_table_names())
    if "campus_service_ratings" in tables:
        op.drop_table("campus_service_ratings")
    if "campus_services" in tables:
        op.drop_table("campus_services")
