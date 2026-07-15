package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/yatools/wutong-campus-wall/backend/internal/config"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const AlembicHead = "0005_credit_anonymous_team_lifecycle"

var requiredTables = []string{
	"users", "credit_rules", "sessions", "verification_codes", "rate_limit_events", "email_outbox",
	"content_entities", "content_revisions", "attachments", "posts", "comments", "thread_anonymous_identities",
	"reactions", "favorites", "reports", "moderation_cases", "notifications", "audit_logs", "team_games",
	"team_game_aliases", "game_submissions", "teams", "team_runs", "team_memberships", "team_run_members",
	"team_ratings", "questions", "answers", "handbook_articles", "courses", "course_offerings", "course_reviews",
	"campus_services", "campus_service_ratings", "listings", "activities", "activity_members", "lost_items",
	"lost_claims", "observe_posts", "penalties", "appeals", "conversations", "conversation_members", "messages",
	"blocks", "announcements", "announcement_reads", "feedback", "settings", "backup_jobs",
}

func BaselineTables() []string {
	result := make([]string, len(requiredTables))
	copy(result, requiredTables)
	return result
}

func Open(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("解析数据库配置: %w", err)
	}
	poolCfg.MaxConns = cfg.DBPoolSize + cfg.DBMaxOverflow
	if poolCfg.MaxConns < 1 {
		poolCfg.MaxConns = 1
	}
	poolCfg.MinConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接 PostgreSQL: %w", err)
	}
	return pool, nil
}

func OpenSQL(cfg config.Config) (*sql.DB, error) {
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	maxConnections := cfg.DBPoolSize + cfg.DBMaxOverflow
	if maxConnections < 1 {
		maxConnections = 1
	}
	db.SetMaxOpenConns(int(maxConnections))
	return db, nil
}

func Migrate(ctx context.Context, cfg config.Config) error {
	db, err := OpenSQL(cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	if err := adoptAlembicIfNeeded(ctx, db); err != nil {
		return err
	}
	goose.SetBaseFS(migrationFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.UpContext(ctx, db, "migrations")
}

func MigrationStatus(ctx context.Context, cfg config.Config) error {
	db, err := OpenSQL(cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	goose.SetBaseFS(migrationFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.StatusContext(ctx, db, "migrations")
}

func adoptAlembicIfNeeded(ctx context.Context, db *sql.DB) error {
	var gooseExists bool
	if err := db.QueryRowContext(ctx, "SELECT to_regclass('public.goose_db_version') IS NOT NULL").Scan(&gooseExists); err != nil {
		return err
	}
	if gooseExists {
		return nil
	}
	var alembicExists bool
	if err := db.QueryRowContext(ctx, "SELECT to_regclass('public.alembic_version') IS NOT NULL").Scan(&alembicExists); err != nil {
		return err
	}
	if !alembicExists {
		var publicTables int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname='public'").Scan(&publicTables); err != nil {
			return err
		}
		if publicTables != 0 {
			return errors.New("数据库不是空库，也没有可接管的 alembic_version")
		}
		return nil
	}
	var version string
	if err := db.QueryRowContext(ctx, "SELECT version_num FROM alembic_version LIMIT 1").Scan(&version); err != nil {
		return err
	}
	if version != AlembicHead {
		return fmt.Errorf("alembic 版本 %q 不受支持，必须先升级到 %s", version, AlembicHead)
	}
	missing, err := missingRequiredTables(ctx, db)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("alembic 数据库结构不完整，缺少表: %s", strings.Join(missing, ", "))
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE goose_db_version (
		id BIGSERIAL PRIMARY KEY,
		version_id BIGINT NOT NULL,
		is_applied BOOLEAN NOT NULL,
		tstamp TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO goose_db_version(version_id,is_applied) VALUES (0,true),(1,true)"); err != nil {
		return err
	}
	return tx.Commit()
}

func missingRequiredTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT tablename FROM pg_tables WHERE schemaname='public'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		found[name] = true
	}
	var missing []string
	for _, name := range requiredTables {
		if !found[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing, rows.Err()
}
