"""Add V4 game catalog, richer teams, and offline listing details.

Revision ID: 0003_v4_games_teams_and_listings
Revises: 0002_revisions_and_run_integrity
"""

from datetime import UTC, datetime

import sqlalchemy as sa

from alembic import op

revision = "0003_v4_games_teams_and_listings"
down_revision = "0002_revisions_and_run_integrity"
branch_labels = None
depends_on = None

GAMES = [
    ("英雄联盟", ["LOL", "League of Legends"]), ("无畏契约", ["瓦", "Valorant"]),
    ("CS2", ["Counter-Strike 2", "CS"]), ("原神", ["Genshin Impact"]),
    ("崩坏：星穹铁道", ["星铁", "星穹铁道", "Honkai Star Rail"]), ("王者荣耀", ["王者"]),
    ("雀魂", ["Mahjong Soul"]), ("Minecraft", ["MC", "我的世界"]),
    ("饥荒", ["Don't Starve Together", "DST"]), ("DND", ["D&D", "龙与地下城"]),
]


def normalized(value: str) -> str:
    return "".join(value.casefold().split())


def columns(inspector: sa.Inspector, table: str) -> set[str]:
    return {row["name"] for row in inspector.get_columns(table)}


def upgrade() -> None:
    bind = op.get_bind()
    inspector = sa.inspect(bind)
    tables = set(inspector.get_table_names())
    bigint = sa.BigInteger().with_variant(sa.Integer(), "sqlite")

    if "team_games" not in tables:
        op.create_table(
            "team_games",
            sa.Column("id", bigint, autoincrement=True, nullable=False),
            sa.Column("name", sa.String(length=80), nullable=False),
            sa.Column("active", sa.Boolean(), nullable=False, server_default=sa.true()),
            sa.Column("created_at", sa.DateTime(timezone=True), nullable=False),
            sa.Column("updated_at", sa.DateTime(timezone=True), nullable=False),
            sa.PrimaryKeyConstraint("id"), sa.UniqueConstraint("name"),
        )
        op.create_index("ix_team_games_name", "team_games", ["name"])
        op.create_index("ix_team_games_active", "team_games", ["active"])
    if "team_game_aliases" not in tables:
        op.create_table(
            "team_game_aliases",
            sa.Column("id", bigint, autoincrement=True, nullable=False),
            sa.Column("game_id", bigint, nullable=False),
            sa.Column("alias", sa.String(length=80), nullable=False),
            sa.Column("normalized_alias", sa.String(length=80), nullable=False),
            sa.ForeignKeyConstraint(["game_id"], ["team_games.id"], ondelete="CASCADE"),
            sa.PrimaryKeyConstraint("id"), sa.UniqueConstraint("normalized_alias"),
        )
        op.create_index("ix_team_game_aliases_game_id", "team_game_aliases", ["game_id"])
        op.create_index("ix_team_game_aliases_normalized_alias", "team_game_aliases", ["normalized_alias"])
    if "game_submissions" not in tables:
        op.create_table(
            "game_submissions",
            sa.Column("id", bigint, autoincrement=True, nullable=False),
            sa.Column("submitter_id", bigint, nullable=False),
            sa.Column("proposed_name", sa.String(length=80), nullable=False),
            sa.Column("aliases", sa.JSON(), nullable=False),
            sa.Column("status", sa.String(length=20), nullable=False, server_default="pending"),
            sa.Column("resolved_game_id", bigint), sa.Column("reviewer_id", bigint),
            sa.Column("admin_note", sa.Text(), nullable=False, server_default=""),
            sa.Column("created_at", sa.DateTime(timezone=True), nullable=False),
            sa.Column("reviewed_at", sa.DateTime(timezone=True)),
            sa.ForeignKeyConstraint(["resolved_game_id"], ["team_games.id"], ondelete="SET NULL"),
            sa.ForeignKeyConstraint(["reviewer_id"], ["users.id"], ondelete="SET NULL"),
            sa.ForeignKeyConstraint(["submitter_id"], ["users.id"], ondelete="CASCADE"),
            sa.PrimaryKeyConstraint("id"),
        )
        op.create_index("ix_game_submissions_submitter_id", "game_submissions", ["submitter_id"])
        op.create_index("ix_game_submissions_status", "game_submissions", ["status"])

    team_columns = columns(inspector, "teams")
    missing_team = {"game_id", "newbie_level", "vibe", "reminder_channels"} - team_columns
    if missing_team:
        with op.batch_alter_table("teams") as batch_op:
            if "game_id" in missing_team:
                batch_op.add_column(sa.Column("game_id", bigint))
                batch_op.create_foreign_key("fk_teams_game_id", "team_games", ["game_id"], ["id"], ondelete="SET NULL")
                batch_op.create_index("ix_teams_game_id", ["game_id"])
            if "newbie_level" in missing_team:
                batch_op.add_column(sa.Column("newbie_level", sa.String(length=80), nullable=False, server_default="欢迎新手"))
            if "vibe" in missing_team:
                batch_op.add_column(sa.Column("vibe", sa.String(length=160), nullable=False, server_default=""))
            if "reminder_channels" in missing_team:
                batch_op.add_column(sa.Column("reminder_channels", sa.String(length=160), nullable=False, server_default="email,in_app"))
    if "reminder_channels" not in columns(inspector, "team_memberships"):
        with op.batch_alter_table("team_memberships") as batch_op:
            batch_op.add_column(sa.Column("reminder_channels", sa.String(length=160), nullable=False, server_default="email,in_app"))
    listing_columns = columns(inspector, "listings")
    if {"negotiable", "purchased_at"} - listing_columns:
        with op.batch_alter_table("listings") as batch_op:
            if "negotiable" not in listing_columns:
                batch_op.add_column(sa.Column("negotiable", sa.Boolean(), nullable=False, server_default=sa.true()))
            if "purchased_at" not in listing_columns:
                batch_op.add_column(sa.Column("purchased_at", sa.Date()))

    if not bind.scalar(sa.text("SELECT COUNT(*) FROM team_games")):
        now = datetime.now(UTC)
        for name, _ in GAMES:
            bind.execute(sa.text("INSERT INTO team_games (name, active, created_at, updated_at) VALUES (:name, :active, :created, :updated)"), {"name": name, "active": True, "created": now, "updated": now})
        ids = {row.name: row.id for row in bind.execute(sa.text("SELECT id, name FROM team_games"))}
        alias_to_id: dict[str, int] = {}
        for name, values in GAMES:
            for alias in [name, *values]:
                key = normalized(alias)
                bind.execute(sa.text("INSERT INTO team_game_aliases (game_id, alias, normalized_alias) VALUES (:game_id, :alias, :key)"), {"game_id": ids[name], "alias": alias, "key": key})
                alias_to_id[key] = ids[name]
        for row in bind.execute(sa.text("SELECT entity_id, game FROM teams")):
            if game_id := alias_to_id.get(normalized(row.game)):
                bind.execute(sa.text("UPDATE teams SET game_id=:game_id WHERE entity_id=:id"), {"game_id": game_id, "id": row.entity_id})


def downgrade() -> None:
    inspector = sa.inspect(op.get_bind())
    listing_columns = columns(inspector, "listings")
    with op.batch_alter_table("listings") as batch_op:
        if "purchased_at" in listing_columns:
            batch_op.drop_column("purchased_at")
        if "negotiable" in listing_columns:
            batch_op.drop_column("negotiable")
    if "reminder_channels" in columns(inspector, "team_memberships"):
        with op.batch_alter_table("team_memberships") as batch_op:
            batch_op.drop_column("reminder_channels")
    team_columns = columns(inspector, "teams")
    with op.batch_alter_table("teams") as batch_op:
        for name in ["reminder_channels", "vibe", "newbie_level", "game_id"]:
            if name in team_columns:
                batch_op.drop_column(name)
    for table in ["game_submissions", "team_game_aliases", "team_games"]:
        if table in inspector.get_table_names():
            op.drop_table(table)
