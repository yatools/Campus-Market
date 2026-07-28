package governance_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatools/wutong-campus-wall/backend/internal/config"
	"github.com/yatools/wutong-campus-wall/backend/internal/database"
	"github.com/yatools/wutong-campus-wall/backend/internal/governance"
)

func TestPostgresRepositoryPersistsPenaltyCreditAndAppealAtomically(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("wutong_governance_%d", time.Now().UnixNano())
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
	cfg := config.Config{DatabaseURL: targetURL.String(), DBPoolSize: 4, AppTimezone: "Asia/Shanghai"}
	if err := database.Migrate(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	createUser := func(email, nickname, role string) int64 {
		t.Helper()
		var id int64
		err := pool.QueryRow(context.Background(), `INSERT INTO users(
			email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,
			dm_stranger_off,hide_online,verified_at,created_at,updated_at
		) VALUES($1,'unused',$2,$2,'student',$3,'active',800,0,false,false,now(),now(),now())
		RETURNING id`, email, nickname, role).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	moderatorID := createUser("moderator@governance.test", "moderator", "moderator")
	userID := createUser("user@governance.test", "user", "user")

	service := governance.NewService(governance.NewPostgresRepository(pool), "integration-secret")
	penalty, err := service.CreatePenalty(
		context.Background(),
		governance.Actor{ID: moderatorID, Role: "moderator"},
		governance.PenaltyCommand{
			UserID: userID, Violation: "spam", Result: "warning",
			Rule: "community rule", Delta: -25, RequestID: "integration",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if penalty.Credit != 775 {
		t.Fatalf("credit=%d want 775", penalty.Credit)
	}
	appeal, err := service.AppealPenalty(
		context.Background(),
		governance.Actor{ID: userID, Role: "user"},
		penalty.ID,
		"please review the context",
		"integration",
	)
	if err != nil {
		t.Fatal(err)
	}
	var storedCredit int
	var storedOwner int64
	var storedStatus string
	err = pool.QueryRow(context.Background(), `SELECT u.credit,p.user_id,a.status
		FROM users u
		JOIN penalties p ON p.user_id=u.id
		JOIN appeals a ON a.penalty_id=p.id
		WHERE u.id=$1 AND p.id=$2 AND a.id=$3`,
		userID, penalty.ID, appeal.ID,
	).Scan(&storedCredit, &storedOwner, &storedStatus)
	if err != nil {
		t.Fatal(err)
	}
	if storedCredit != 775 || storedOwner != userID || storedStatus != "pending" {
		t.Fatalf("credit=%d owner=%d status=%s", storedCredit, storedOwner, storedStatus)
	}
}
