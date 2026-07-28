package team_test

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
	"github.com/yatools/wutong-campus-wall/backend/internal/team"
)

func TestPostgresRepositoryPersistsMembershipAndRunTogether(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("wutong_team_%d", time.Now().UnixNano())
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

	createUser := func(email, nickname string) int64 {
		t.Helper()
		var id int64
		err := pool.QueryRow(context.Background(), `INSERT INTO users(
			email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,
			dm_stranger_off,hide_online,verified_at,created_at,updated_at
		) VALUES($1,'unused',$2,$2,'student','user','active',800,0,false,false,now(),now(),now())
		RETURNING id`, email, nickname).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ownerID := createUser("owner@team.test", "owner")
	memberID := createUser("member@team.test", "member")
	var teamID int64
	err = pool.QueryRow(context.Background(), `INSERT INTO content_entities(
		type,owner_id,publication_status,moderation_status,allow_comments,search_visible,
		moderation_reason,revision,created_at,updated_at
	) VALUES('team',$1,'published','approved',true,true,'',1,now(),now()) RETURNING id`, ownerID).Scan(&teamID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO teams(
		entity_id,owner_id,game,mode,rank_requirement,capacity,voice_name,voice_link,notes,
		newbie_level,vibe,reminder_channels,recurrence,reminder_minutes,
		post_departure_retention_minutes,status
	) VALUES($1,$2,'game','ranked','',3,'','','','','','email,in_app','once',30,120,'active')`,
		teamID, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO team_memberships(
		team_id,user_id,role,status,reminder_channels,joined_at
	) VALUES($1,$2,'owner','active','email,in_app',now())`, teamID, ownerID); err != nil {
		t.Fatal(err)
	}
	var runID int64
	startsAt := time.Now().UTC().Add(time.Hour)
	err = pool.QueryRow(context.Background(), `INSERT INTO team_runs(
		team_id,starts_at,expires_at,status,created_at
	) VALUES($1,$2,$3,'scheduled',now()) RETURNING id`, teamID, startsAt, startsAt.Add(2*time.Hour)).Scan(&runID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO team_run_members(
		run_id,user_id,status,credit_awarded
	) VALUES($1,$2,'joined',false)`, runID, ownerID); err != nil {
		t.Fatal(err)
	}

	service := team.NewService(team.NewPostgresRepository(pool))
	result, err := service.Join(
		context.Background(),
		team.Actor{ID: memberID, Nickname: "member"},
		teamID,
		[]string{"email", "in_app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != runID {
		t.Fatalf("run id=%d want %d", result.RunID, runID)
	}
	var membershipStatus, runStatus string
	err = pool.QueryRow(context.Background(), `SELECT tm.status,trm.status
		FROM team_memberships tm JOIN team_run_members trm ON trm.user_id=tm.user_id
		WHERE tm.team_id=$1 AND tm.user_id=$2 AND trm.run_id=$3`,
		teamID, memberID, runID).Scan(&membershipStatus, &runStatus)
	if err != nil {
		t.Fatal(err)
	}
	if membershipStatus != "active" || runStatus != "joined" {
		t.Fatalf("membership=%s run=%s", membershipStatus, runStatus)
	}
}
