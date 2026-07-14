"""Add 1000-point credit rules, anonymous identities and team expiry.

Revision ID: 0005_credit_anonymous_team_lifecycle
Revises: 0004_campus_service_ratings
"""

from datetime import datetime, timedelta

import sqlalchemy as sa

from alembic import op
from app.anonymous import DEFAULT_ANONYMOUS_NICKNAMES
from app.credit import CREDIT_RULE_DEFAULTS

revision = "0005_credit_anonymous_team_lifecycle"
down_revision = "0004_campus_service_ratings"
branch_labels = None
depends_on = None


def _has_column(inspector: sa.Inspector, table: str, column: str) -> bool:
    return any(item["name"] == column for item in inspector.get_columns(table))


def upgrade() -> None:
    bind = op.get_bind()
    inspector = sa.inspect(bind)
    tables = set(inspector.get_table_names())
    bigint = sa.BigInteger().with_variant(sa.Integer(), "sqlite")

    checks = {item.get("name"): str(item.get("sqltext", "")) for item in inspector.get_check_constraints("users")}
    credit_check = checks.get("ck_user_credit", "")
    old_credit_scale = "1000" not in credit_check
    if old_credit_scale or not credit_check:
        with op.batch_alter_table("users") as batch:
            if credit_check:
                batch.drop_constraint("ck_user_credit", type_="check")
            batch.alter_column("credit", existing_type=sa.Integer(), server_default="800")
            batch.create_check_constraint("ck_user_credit", "credit >= 0 AND credit <= 1000")
        if old_credit_scale:
            op.execute(sa.text("UPDATE users SET credit = credit * 10"))

    if "credit_rules" not in tables:
        op.create_table(
            "credit_rules",
            sa.Column("key", sa.String(length=80), nullable=False),
            sa.Column("label", sa.String(length=120), nullable=False),
            sa.Column("kind", sa.String(length=20), nullable=False),
            sa.Column("value", sa.Integer(), nullable=False),
            sa.Column("description", sa.String(length=500), nullable=False, server_default=""),
            sa.Column("updated_by", bigint),
            sa.Column("updated_at", sa.DateTime(timezone=True), nullable=False),
            sa.CheckConstraint("kind IN ('baseline', 'threshold', 'reward', 'penalty')", name="ck_credit_rule_kind"),
            sa.CheckConstraint("value >= -1000 AND value <= 1000", name="ck_credit_rule_value"),
            sa.ForeignKeyConstraint(["updated_by"], ["users.id"], ondelete="SET NULL"),
            sa.PrimaryKeyConstraint("key"),
        )
        op.create_index("ix_credit_rules_kind", "credit_rules", ["kind"])

    credit_table = sa.table(
        "credit_rules",
        sa.column("key", sa.String()),
        sa.column("label", sa.String()),
        sa.column("kind", sa.String()),
        sa.column("value", sa.Integer()),
        sa.column("description", sa.String()),
        sa.column("updated_at", sa.DateTime(timezone=True)),
    )
    existing_rules = set(bind.execute(sa.select(credit_table.c.key)).scalars())
    now = sa.func.now()
    for key, default in CREDIT_RULE_DEFAULTS.items():
        if key not in existing_rules:
            bind.execute(
                credit_table.insert().values(
                    key=key,
                    label=default.label,
                    kind=default.kind,
                    value=default.value,
                    description=default.description,
                    updated_at=now,
                )
            )

    inspector = sa.inspect(bind)
    if not _has_column(inspector, "teams", "post_departure_retention_minutes"):
        with op.batch_alter_table("teams") as batch:
            batch.add_column(
                sa.Column("post_departure_retention_minutes", sa.Integer(), nullable=False, server_default="120")
            )
            batch.create_check_constraint(
                "ck_team_retention",
                "post_departure_retention_minutes >= 60 AND post_departure_retention_minutes <= 480",
            )
    inspector = sa.inspect(bind)
    if not _has_column(inspector, "team_runs", "expires_at"):
        with op.batch_alter_table("team_runs") as batch:
            batch.add_column(sa.Column("expires_at", sa.DateTime(timezone=True), nullable=True))
            batch.create_index("ix_team_runs_expires_at", ["expires_at"])
    rows = bind.execute(
        sa.text(
            "SELECT team_runs.id, team_runs.starts_at, teams.post_departure_retention_minutes "
            "FROM team_runs JOIN teams ON teams.entity_id = team_runs.team_id "
            "WHERE team_runs.expires_at IS NULL"
        )
    ).all()
    for row in rows:
        starts_at = datetime.fromisoformat(row.starts_at) if isinstance(row.starts_at, str) else row.starts_at
        bind.execute(
            sa.text("UPDATE team_runs SET expires_at = :expires_at WHERE id = :id"),
            {"id": row.id, "expires_at": starts_at + timedelta(minutes=row.post_departure_retention_minutes)},
        )

    if "thread_anonymous_identities" not in tables:
        op.create_table(
            "thread_anonymous_identities",
            sa.Column("id", bigint, autoincrement=True, nullable=False),
            sa.Column("thread_id", bigint, nullable=False),
            sa.Column("user_id", bigint, nullable=False),
            sa.Column("display_name", sa.String(length=40), nullable=False),
            sa.Column("created_at", sa.DateTime(timezone=True), nullable=False),
            sa.ForeignKeyConstraint(["thread_id"], ["content_entities.id"], ondelete="CASCADE"),
            sa.ForeignKeyConstraint(["user_id"], ["users.id"], ondelete="CASCADE"),
            sa.PrimaryKeyConstraint("id"),
            sa.UniqueConstraint("thread_id", "display_name", name="uq_thread_anonymous_name"),
            sa.UniqueConstraint("thread_id", "user_id", name="uq_thread_anonymous_user"),
        )
        op.create_index("ix_thread_anonymous_identities_thread_id", "thread_anonymous_identities", ["thread_id"])
        op.create_index("ix_thread_anonymous_identities_user_id", "thread_anonymous_identities", ["user_id"])

    setting_exists = bind.execute(
        sa.text("SELECT 1 FROM settings WHERE key = 'anonymous_nickname_pool'")
    ).scalar()
    if not setting_exists:
        bind.execute(
            sa.text(
                "INSERT INTO settings (key, value, updated_at) "
                "VALUES ('anonymous_nickname_pool', :value, CURRENT_TIMESTAMP)"
            ),
            {"value": "\n".join(DEFAULT_ANONYMOUS_NICKNAMES)},
        )

    identities = {
        (row.thread_id, row.user_id)
        for row in bind.execute(sa.text("SELECT thread_id, user_id FROM thread_anonymous_identities")).all()
    }
    pairs = list(
        bind.execute(
            sa.text(
                "SELECT posts.entity_id AS thread_id, content_entities.owner_id AS user_id "
                "FROM posts JOIN content_entities ON content_entities.id = posts.entity_id "
                "WHERE posts.identity_mode = 'anonymous' "
                "UNION "
                "SELECT comments.target_entity_id AS thread_id, content_entities.owner_id AS user_id "
                "FROM comments JOIN content_entities ON content_entities.id = comments.entity_id "
                "WHERE comments.identity_mode = 'anonymous'"
            )
        ).all()
    )
    used_by_thread: dict[int, set[str]] = {}
    for thread_id, user_id in pairs:
        if (thread_id, user_id) in identities:
            continue
        used = used_by_thread.setdefault(
            thread_id,
            set(
                bind.execute(
                    sa.text("SELECT display_name FROM thread_anonymous_identities WHERE thread_id = :thread_id"),
                    {"thread_id": thread_id},
                ).scalars()
            ),
        )
        base = DEFAULT_ANONYMOUS_NICKNAMES[(thread_id + user_id) % len(DEFAULT_ANONYMOUS_NICKNAMES)]
        name = base
        suffix = 2
        while name in used:
            name = f"{base}·{suffix}"
            suffix += 1
        bind.execute(
            sa.text(
                "INSERT INTO thread_anonymous_identities (thread_id, user_id, display_name, created_at) "
                "VALUES (:thread_id, :user_id, :display_name, CURRENT_TIMESTAMP)"
            ),
            {"thread_id": thread_id, "user_id": user_id, "display_name": name},
        )
        used.add(name)


def downgrade() -> None:
    bind = op.get_bind()
    inspector = sa.inspect(bind)
    tables = set(inspector.get_table_names())
    if "thread_anonymous_identities" in tables:
        op.drop_table("thread_anonymous_identities")
    inspector = sa.inspect(bind)
    if _has_column(inspector, "team_runs", "expires_at"):
        with op.batch_alter_table("team_runs") as batch:
            batch.drop_index("ix_team_runs_expires_at")
            batch.drop_column("expires_at")
    inspector = sa.inspect(bind)
    if _has_column(inspector, "teams", "post_departure_retention_minutes"):
        with op.batch_alter_table("teams") as batch:
            batch.drop_constraint("ck_team_retention", type_="check")
            batch.drop_column("post_departure_retention_minutes")
    if "credit_rules" in tables:
        op.drop_table("credit_rules")
    op.execute(sa.text("UPDATE users SET credit = CAST(credit / 10 AS INTEGER)"))
    with op.batch_alter_table("users") as batch:
        batch.drop_constraint("ck_user_credit", type_="check")
        batch.alter_column("credit", existing_type=sa.Integer(), server_default="80")
        batch.create_check_constraint("ck_user_credit", "credit >= 0 AND credit <= 100")
