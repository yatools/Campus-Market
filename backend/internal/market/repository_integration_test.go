package market_test

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
	"github.com/yatools/wutong-campus-wall/backend/internal/market"
)

func TestPostgresRepositoryPersistsMarketLifecycleAtomically(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("wutong_market_%d", time.Now().UnixNano())
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
	sellerID := createUser("seller@market.test", "seller")
	buyerID := createUser("buyer@market.test", "buyer")
	var entityID int64
	err = pool.QueryRow(context.Background(), `INSERT INTO content_entities(
		type,owner_id,publication_status,moderation_status,allow_comments,search_visible,
		moderation_reason,revision,created_at,updated_at
	) VALUES('listing',$1,'published','approved',true,true,'',1,now(),now()) RETURNING id`, sellerID).Scan(&entityID)
	if err != nil {
		t.Fatal(err)
	}
	var categoryID, locationID int64
	if err := pool.QueryRow(context.Background(), "SELECT id FROM market_categories WHERE active=true ORDER BY id LIMIT 1").Scan(&categoryID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), "SELECT id FROM market_locations WHERE active=true ORDER BY id LIMIT 1").Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO listings(
		entity_id,category_id,title,description,price_cents,condition,negotiable,location_id,trade_status
	) VALUES($1,$2,'bike','well maintained bike',20000,'good',true,$3,'available')`, entityID, categoryID, locationID); err != nil {
		t.Fatal(err)
	}

	service := market.NewService(market.NewPostgresRepository(pool), time.Hour, 14*24*time.Hour)
	requested, err := service.RequestTransaction(context.Background(), market.Actor{ID: buyerID}, entityID, "reserve it")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcceptTransaction(context.Background(), market.Actor{ID: sellerID}, requested.TransactionID); err != nil {
		t.Fatal(err)
	}
	var transactionStatus, listingStatus string
	err = pool.QueryRow(context.Background(), `SELECT mt.status,l.trade_status
		FROM market_transactions mt JOIN listings l ON l.entity_id=mt.listing_id
		WHERE mt.id=$1`, requested.TransactionID).Scan(&transactionStatus, &listingStatus)
	if err != nil {
		t.Fatal(err)
	}
	if transactionStatus != "reserved" || listingStatus != "reserved" {
		t.Fatalf("transaction=%s listing=%s", transactionStatus, listingStatus)
	}
	var notificationCount int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM notifications WHERE user_id IN ($1,$2)", sellerID, buyerID).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if notificationCount != 2 {
		t.Fatalf("notification count=%d, want 2", notificationCount)
	}
}
