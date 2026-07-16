package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/yatools/wutong-campus-wall/backend/internal/config"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const LatestMigrationVersion int64 = 3

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

func MigrationDown(ctx context.Context, cfg config.Config) error {
	db, err := OpenSQL(cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	goose.SetBaseFS(migrationFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.DownContext(ctx, db, "migrations")
}
