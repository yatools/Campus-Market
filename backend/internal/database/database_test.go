package database

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatools/wutong-campus-wall/backend/internal/config"
)

func TestBaselineContainsEveryRequiredTable(t *testing.T) {
	data, err := migrationFS.ReadFile("migrations/00001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(data))
	for _, table := range requiredTables {
		if !strings.Contains(sql, "create table "+table+" ") && !strings.Contains(sql, "create table "+table+"(") {
			t.Errorf("baseline is missing table %s", table)
		}
	}
	for _, index := range []string{"ix_posts_title_trgm", "ix_questions_title_trgm", "ix_listings_title_trgm"} {
		if !strings.Contains(sql, index) {
			t.Errorf("baseline is missing %s", index)
		}
	}
}

func TestMigrateAdoptsAlembicHeadWithoutReapplyingBaseline(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("wutong_adopt_%d", time.Now().UnixNano())
	adminURL := *parsed
	adminURL.Path = "/postgres"
	admin, err := pgx.Connect(context.Background(), adminURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE DATABASE "+identifier); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+identifier+" WITH (FORCE)")
	}()
	targetURL := *parsed
	targetURL.Path = "/" + name
	cfg := config.Config{DatabaseURL: targetURL.String(), DBPoolSize: 2}
	if err := Migrate(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := OpenSQL(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE goose_db_version"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE alembic_version(version_num VARCHAR(64) NOT NULL)"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO alembic_version(version_num) VALUES($1)", AlembicHead); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if err := Migrate(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err = OpenSQL(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int64
	if err := db.QueryRow("SELECT version_id FROM goose_db_version WHERE is_applied ORDER BY id DESC LIMIT 1").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("unexpected adopted goose version: %d", version)
	}
	var alembic string
	if err := db.QueryRow("SELECT version_num FROM alembic_version").Scan(&alembic); err != nil {
		t.Fatal(err)
	}
	if alembic != AlembicHead {
		t.Fatalf("alembic marker changed: %s", alembic)
	}
}
