package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	domain "github.com/yatools/wutong-campus-wall/backend/internal/app"
	"github.com/yatools/wutong-campus-wall/backend/internal/config"
	"github.com/yatools/wutong-campus-wall/backend/internal/database"
	"github.com/yatools/wutong-campus-wall/backend/internal/security"
)

type integrationQueryCounter struct{ count atomic.Int64 }

func (c *integrationQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.count.Add(1)
	return ctx
}

func (*integrationQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestMarketTransactionRequiresTwoPartyCompletion(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	cfg := config.Config{Environment: "test", APIPrefix: "/api/v1", PublicOrigin: "http://localhost:5173", SecretKey: "market-test-secret-key-long-enough", DatabaseURL: dsn, DBPoolSize: 8, SessionCookieName: "wutong_session", CSRFCookieName: "wutong_csrf", SessionSliding: 7 * 24 * time.Hour, SessionAbsolute: 30 * 24 * time.Hour, SessionRotation: 24 * time.Hour, MarketReservationTTL: 24 * time.Hour, MarketReviewBlindTTL: 14 * 24 * time.Hour, TrustedHosts: map[string]struct{}{"127.0.0.1": {}, "localhost": {}}}
	db, err := database.OpenSQL(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DROP SCHEMA public CASCADE"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := database.Migrate(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed, limited := 0, 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := pool.Begin(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			defer tx.Rollback(context.Background())
			err = domain.CheckRateLimit(context.Background(), tx, "concurrency_test", "same-subject", 5, 15)
			mu.Lock()
			defer mu.Unlock()
			if errors.Is(err, domain.ErrRateLimited) {
				limited++
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			if err := tx.Commit(context.Background()); err != nil {
				t.Error(err)
				return
			}
			allowed++
		}()
	}
	wg.Wait()
	if allowed != 5 || limited != 15 {
		t.Fatalf("atomic limiter allowed=%d limited=%d", allowed, limited)
	}
	createUser := func(name, alias string) (int64, string, string) {
		var id int64
		if err := pool.QueryRow(context.Background(), `INSERT INTO users(email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,dm_stranger_off,hide_online,verified_at,created_at,updated_at) VALUES($1,'unused',$2,$3,'student','user','active',900,100,false,false,now(),now(),now()) RETURNING id`, alias+"@test.edu.cn", name, alias).Scan(&id); err != nil {
			t.Fatal(err)
		}
		token := fmt.Sprintf("token-%d", id)
		csrf := fmt.Sprintf("csrf-%d", id)
		_, err := pool.Exec(context.Background(), `INSERT INTO sessions(user_id,token_hash,csrf_token,ip_address,user_agent,expires_at,absolute_expires_at,last_seen_at,created_at) VALUES($1,$2,$3,'127.0.0.1','test',now()+interval '1 day',now()+interval '2 day',now(),now())`, id, security.TokenHash(cfg.SecretKey, token), csrf)
		if err != nil {
			t.Fatal(err)
		}
		return id, token, csrf
	}
	sellerID, sellerToken, sellerCSRF := createUser("卖家", "seller")
	buyerID, buyerToken, buyerCSRF := createUser("买家", "buyer")
	allowed = 0
	limited = 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var remaining int
			err := pool.QueryRow(context.Background(), "UPDATE users SET xp=xp-80 WHERE id=$1 AND xp>=80 RETURNING xp", buyerID).Scan(&remaining)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				allowed++
			} else {
				limited++
			}
		}()
	}
	wg.Wait()
	var xp int
	if err := pool.QueryRow(context.Background(), "SELECT xp FROM users WHERE id=$1", buyerID).Scan(&xp); err != nil || allowed != 1 || limited != 1 || xp != 20 {
		t.Fatalf("atomic XP spend allowed=%d rejected=%d xp=%d err=%v", allowed, limited, xp, err)
	}
	server := httptest.NewServer(New(cfg, pool))
	defer server.Close()
	newClient := func(token, csrf string) *http.Client {
		jar, _ := cookiejar.New(nil)
		u, _ := url.Parse(server.URL)
		jar.SetCookies(u, []*http.Cookie{{Name: cfg.SessionCookieName, Value: token, Path: "/"}, {Name: cfg.CSRFCookieName, Value: csrf, Path: "/"}})
		return &http.Client{Jar: jar}
	}
	sellerClient := newClient(sellerToken, sellerCSRF)
	buyerClient := newClient(buyerToken, buyerCSRF)
	var categoryID, locationID int64
	if err := pool.QueryRow(context.Background(), "SELECT (SELECT id FROM market_categories ORDER BY id LIMIT 1),(SELECT id FROM market_locations ORDER BY id LIMIT 1)").Scan(&categoryID, &locationID); err != nil {
		t.Fatal(err)
	}
	created := requestJSON(t, sellerClient, http.MethodPost, server.URL+"/api/v1/listings", map[string]any{"category_id": categoryID, "location_id": locationID, "title": "九成新显示器", "description": "功能正常，仅校内当面验货交易", "price_cents": 50000, "condition": "excellent", "negotiable": true}, sellerCSRF)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create listing: %d %s", created.StatusCode, readResponse(created))
	}
	var listing struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(created.Body).Decode(&listing); err != nil {
		t.Fatal(err)
	}
	created.Body.Close()
	requested := requestJSON(t, buyerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/listings/%d/transactions", server.URL, listing.ID), map[string]any{"message": "希望明天下午面交"}, buyerCSRF)
	if requested.StatusCode != http.StatusCreated {
		t.Fatalf("request: %d %s", requested.StatusCode, readResponse(requested))
	}
	var transaction struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(requested.Body).Decode(&transaction); err != nil {
		t.Fatal(err)
	}
	requested.Body.Close()
	accepted := requestJSON(t, sellerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/market-transactions/%d/accept", server.URL, transaction.ID), nil, sellerCSRF)
	if accepted.StatusCode != http.StatusOK {
		t.Fatalf("accept: %d %s", accepted.StatusCode, readResponse(accepted))
	}
	accepted.Body.Close()
	first := requestJSON(t, buyerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/market-transactions/%d/confirm", server.URL, transaction.ID), nil, buyerCSRF)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("buyer confirm: %d %s", first.StatusCode, readResponse(first))
	}
	first.Body.Close()
	var status string
	if err := pool.QueryRow(context.Background(), "SELECT status FROM market_transactions WHERE id=$1", transaction.ID).Scan(&status); err != nil || status != "reserved" {
		t.Fatalf("one confirmation completed transaction: %s %v", status, err)
	}
	second := requestJSON(t, sellerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/market-transactions/%d/confirm", server.URL, transaction.ID), nil, sellerCSRF)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("seller confirm: %d %s", second.StatusCode, readResponse(second))
	}
	second.Body.Close()
	if err := pool.QueryRow(context.Background(), "SELECT status FROM market_transactions WHERE id=$1", transaction.ID).Scan(&status); err != nil || status != "completed" {
		t.Fatalf("transaction not completed: %s %v", status, err)
	}
	var sales int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM market_transactions WHERE seller_id=$1 AND status='completed'", sellerID).Scan(&sales); err != nil || sales != 1 {
		t.Fatalf("completed sales not transaction-backed: %d %v", sales, err)
	}
	if sellerID == buyerID {
		t.Fatal("test parties unexpectedly equal")
	}

	traceCounter := &integrationQueryCounter{}
	traceConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	traceConfig.ConnConfig.Tracer = traceCounter
	tracedPool, err := pgxpool.NewWithConfig(context.Background(), traceConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer tracedPool.Close()
	listServer := httptest.NewServer(New(cfg, tracedPool))
	defer listServer.Close()
	for _, path := range []string{"/api/v1/listings?page_size=20", "/api/v1/handbook?page_size=20", "/api/v1/course-offerings?page_size=20", "/api/v1/activities?page_size=20", "/api/v1/lost-items?page_size=20", "/api/v1/observe-posts?page_size=20", "/api/v1/campus-services", "/api/v1/teams?page_size=20"} {
		traceCounter.count.Store(0)
		response, err := http.Get(listServer.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body := readResponse(response)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("list %s: %d %s", path, response.StatusCode, body)
		}
		if queries := traceCounter.count.Load(); queries > 2 {
			t.Fatalf("list %s used %d SQL queries; query count must not grow with page size", path, queries)
		}
	}
	traceCounter.count.Store(0)
	conversationResponse, err := buyerClient.Get(listServer.URL + "/api/v1/conversations?page_size=20")
	if err != nil {
		t.Fatal(err)
	}
	conversationBody := readResponse(conversationResponse)
	if conversationResponse.StatusCode != http.StatusOK {
		t.Fatalf("conversation list: %d %s", conversationResponse.StatusCode, conversationBody)
	}
	if queries := traceCounter.count.Load(); queries > 3 {
		t.Fatalf("conversation list used %d SQL queries; query count must not grow with page size", queries)
	}
}
