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

	conversationCreated := requestJSON(t, sellerClient, http.MethodPost, server.URL+"/api/v1/conversations", map[string]any{"recipient_id": buyerID, "context_type": "direct", "first_message": "你好，这是拉黑状态测试"}, sellerCSRF)
	if conversationCreated.StatusCode != http.StatusCreated {
		t.Fatalf("create conversation: %d %s", conversationCreated.StatusCode, readResponse(conversationCreated))
	}
	var conversationResult struct {
		Conversation struct {
			ID int64 `json:"id"`
		} `json:"conversation"`
	}
	if err := json.NewDecoder(conversationCreated.Body).Decode(&conversationResult); err != nil {
		t.Fatal(err)
	}
	conversationCreated.Body.Close()
	blocked := requestJSON(t, sellerClient, http.MethodPut, fmt.Sprintf("%s/api/v1/blocks/%d", server.URL, buyerID), nil, sellerCSRF)
	if blocked.StatusCode != http.StatusOK {
		t.Fatalf("block user: %d %s", blocked.StatusCode, readResponse(blocked))
	}
	blocked.Body.Close()
	assertBlockDirection := func(client *http.Client, want bool) {
		response, err := client.Get(server.URL + "/api/v1/conversations")
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Items []struct {
				ID          int64 `json:"id"`
				BlockedByMe bool  `json:"blocked_by_me"`
			} `json:"items"`
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("list conversations: %d %s", response.StatusCode, readResponse(response))
		}
		if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		for _, item := range page.Items {
			if item.ID == conversationResult.Conversation.ID {
				if item.BlockedByMe != want {
					t.Fatalf("blocked_by_me=%v want %v", item.BlockedByMe, want)
				}
				return
			}
		}
		t.Fatal("created conversation missing from list")
	}
	assertBlockDirection(sellerClient, true)
	assertBlockDirection(buyerClient, false)
	if _, err := pool.Exec(context.Background(), "INSERT INTO notifications(user_id,type,title,body,link,created_at) VALUES($1,'system','系统通知','不应被私信已读清除','/me',now())", buyerID); err != nil {
		t.Fatal(err)
	}
	unreadCounts := func() (int, int) {
		response, err := buyerClient.Get(server.URL + "/api/v1/me")
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("load me: %d %s", response.StatusCode, readResponse(response))
		}
		var payload struct {
			UnreadNotifications int `json:"unread_notifications"`
			UnreadMessages      int `json:"unread_messages"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return payload.UnreadNotifications, payload.UnreadMessages
	}
	if all, messages := unreadCounts(); all != 2 || messages != 1 {
		t.Fatalf("initial unread counts all=%d messages=%d", all, messages)
	}
	readConversation, err := buyerClient.Get(fmt.Sprintf("%s/api/v1/conversations/%d/messages", server.URL, conversationResult.Conversation.ID))
	if err != nil {
		t.Fatal(err)
	}
	if readConversation.StatusCode != http.StatusOK {
		t.Fatalf("read conversation: %d %s", readConversation.StatusCode, readResponse(readConversation))
	}
	readConversation.Body.Close()
	if all, messages := unreadCounts(); all != 1 || messages != 0 {
		t.Fatalf("single-conversation read cleared unrelated notifications: all=%d messages=%d", all, messages)
	}
	blockedSend := requestJSON(t, sellerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/conversations/%d/messages", server.URL, conversationResult.Conversation.ID), map[string]any{"body": "这条消息不应发送"}, sellerCSRF)
	if blockedSend.StatusCode != http.StatusForbidden {
		t.Fatalf("blocked send: %d %s", blockedSend.StatusCode, readResponse(blockedSend))
	}
	blockedSend.Body.Close()
	for i := 0; i < 2; i++ {
		unblocked := requestJSON(t, sellerClient, http.MethodDelete, fmt.Sprintf("%s/api/v1/blocks/%d", server.URL, buyerID), nil, sellerCSRF)
		if unblocked.StatusCode != http.StatusOK {
			t.Fatalf("unblock user attempt %d: %d %s", i+1, unblocked.StatusCode, readResponse(unblocked))
		}
		unblocked.Body.Close()
	}
	assertBlockDirection(sellerClient, false)
	unblockedSend := requestJSON(t, sellerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/conversations/%d/messages", server.URL, conversationResult.Conversation.ID), map[string]any{"body": "取消拉黑后可以发送"}, sellerCSRF)
	if unblockedSend.StatusCode != http.StatusCreated {
		t.Fatalf("send after unblock: %d %s", unblockedSend.StatusCode, readResponse(unblockedSend))
	}
	unblockedSend.Body.Close()
	for i := 0; i < 2; i++ {
		readAll := requestJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/conversations/read-all", nil, buyerCSRF)
		if readAll.StatusCode != http.StatusOK {
			t.Fatalf("read all messages attempt %d: %d %s", i+1, readAll.StatusCode, readResponse(readAll))
		}
		var payload struct {
			MarkedMessages      int64 `json:"marked_messages"`
			UnreadMessages      int   `json:"unread_messages"`
			UnreadNotifications int   `json:"unread_notifications"`
		}
		if err := json.NewDecoder(readAll.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		readAll.Body.Close()
		wantMarked := int64(1)
		if i == 1 {
			wantMarked = 0
		}
		if payload.MarkedMessages != wantMarked || payload.UnreadMessages != 0 || payload.UnreadNotifications != 1 {
			t.Fatalf("read all attempt %d payload=%+v want marked=%d, private=0, total=1", i+1, payload, wantMarked)
		}
	}
	observeThreshold := creditDefault("threshold.observe_publish")
	if _, err := pool.Exec(context.Background(), "UPDATE users SET credit=$1 WHERE id=$2", observeThreshold, sellerID); err != nil {
		t.Fatal(err)
	}
	observeCreated := requestJSON(t, sellerClient, http.MethodPost, server.URL+"/api/v1/observe-posts", map[string]any{"title": "文明观察测试", "body": "只描述发生的公共事件，不包含任何个人隐私信息。"}, sellerCSRF)
	if observeCreated.StatusCode != http.StatusCreated {
		t.Fatalf("credit-qualified observe post: %d %s", observeCreated.StatusCode, readResponse(observeCreated))
	}
	observeCreated.Body.Close()
	if _, err := pool.Exec(context.Background(), "UPDATE users SET credit=$1 WHERE id=$2", observeThreshold-1, buyerID); err != nil {
		t.Fatal(err)
	}
	observeRejected := requestJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/observe-posts", map[string]any{"title": "信用不足测试", "body": "这条观察记录不应在信用不足时创建成功。"}, buyerCSRF)
	if observeRejected.StatusCode != http.StatusForbidden {
		t.Fatalf("low-credit observe post: %d %s", observeRejected.StatusCode, readResponse(observeRejected))
	}
	observeRejected.Body.Close()
	if _, err := pool.Exec(context.Background(), "UPDATE users SET credit=900 WHERE id IN ($1,$2)", sellerID, buyerID); err != nil {
		t.Fatal(err)
	}

	teamGamesResponse, err := http.Get(server.URL + "/api/v1/team-games")
	if err != nil {
		t.Fatal(err)
	}
	if teamGamesResponse.StatusCode != http.StatusOK {
		t.Fatalf("seed team games: %d %s", teamGamesResponse.StatusCode, readResponse(teamGamesResponse))
	}
	teamGamesResponse.Body.Close()
	var gameID int64
	if err := pool.QueryRow(context.Background(), "SELECT id FROM team_games WHERE active=true ORDER BY id LIMIT 1").Scan(&gameID); err != nil {
		t.Fatal(err)
	}
	// createTeamRun 要求发车时间至少提前 10 分钟，而签到窗口是「发车后 30 分钟内」，
	// 因此这里先按未来时间建队（走正常校验），再把场次时间改到刚刚过去，
	// 以便在同一个用例里覆盖签到与奖励幂等。
	teamStart := time.Now().UTC().Add(15 * time.Minute)
	teamCreated := requestJSON(t, sellerClient, http.MethodPost, server.URL+"/api/v1/teams", map[string]any{"game_id": gameID, "mode": "集成测试", "capacity": 3, "starts_at": teamStart, "recurrence": "once", "reminder_channels": []string{"in_app"}}, sellerCSRF)
	if teamCreated.StatusCode != http.StatusCreated {
		t.Fatalf("create team: %d %s", teamCreated.StatusCode, readResponse(teamCreated))
	}
	var team struct {
		ID      int64 `json:"id"`
		NextRun struct {
			ID int64 `json:"id"`
		} `json:"next_run"`
	}
	if err := json.NewDecoder(teamCreated.Body).Decode(&team); err != nil {
		t.Fatal(err)
	}
	teamCreated.Body.Close()
	joined := requestJSON(t, buyerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/teams/%d/join", server.URL, team.ID), map[string]any{"reminder_channels": []string{"in_app"}}, buyerCSRF)
	if joined.StatusCode != http.StatusOK {
		t.Fatalf("join team: %d %s", joined.StatusCode, readResponse(joined))
	}
	joined.Body.Close()
	var creditBefore int
	if err := pool.QueryRow(context.Background(), "SELECT credit FROM users WHERE id=$1", sellerID).Scan(&creditBefore); err != nil {
		t.Fatal(err)
	}
	// 把发车时间挪到 5 分钟前，进入签到窗口。签到奖励只在「已发车且本场成团（≥2 人）」时
	// 发放，此时队里已有车头与买家两人。
	if _, err := pool.Exec(context.Background(), "UPDATE team_runs SET starts_at=now()-interval '5 minutes' WHERE id=$1", team.NextRun.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		checkedIn := requestJSON(t, sellerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/teams/%d/runs/%d/check-in", server.URL, team.ID, team.NextRun.ID), nil, sellerCSRF)
		if checkedIn.StatusCode != http.StatusOK {
			t.Fatalf("owner check-in attempt %d: %d %s", i+1, checkedIn.StatusCode, readResponse(checkedIn))
		}
		checkedIn.Body.Close()
	}
	var creditAfter int
	if err := pool.QueryRow(context.Background(), "SELECT credit FROM users WHERE id=$1", sellerID).Scan(&creditAfter); err != nil {
		t.Fatal(err)
	}
	reward := creditDefault("reward.team_check_in")
	if creditAfter-creditBefore != reward {
		t.Fatalf("check-in reward applied more than once: before=%d after=%d reward=%d", creditBefore, creditAfter, reward)
	}
	var firstExcusedAt time.Time
	for i := 0; i < 2; i++ {
		excused := requestJSON(t, buyerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/teams/%d/runs/%d/excuse", server.URL, team.ID, team.NextRun.ID), nil, buyerCSRF)
		if excused.StatusCode != http.StatusOK {
			t.Fatalf("member excuse attempt %d: %d %s", i+1, excused.StatusCode, readResponse(excused))
		}
		var payload struct {
			ExcusedAt time.Time `json:"excused_at"`
		}
		if err := json.NewDecoder(excused.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		excused.Body.Close()
		if i == 0 {
			firstExcusedAt = payload.ExcusedAt
		} else if !payload.ExcusedAt.Equal(firstExcusedAt) {
			t.Fatalf("idempotent excuse changed timestamp: first=%s second=%s", firstExcusedAt, payload.ExcusedAt)
		}
	}
	secondRun := requestJSON(t, sellerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/teams/%d/runs", server.URL, team.ID), map[string]any{"starts_at": time.Now().UTC().Add(2 * time.Hour)}, sellerCSRF)
	if secondRun.StatusCode != http.StatusCreated {
		t.Fatalf("create second run: %d %s", secondRun.StatusCode, readResponse(secondRun))
	}
	var secondRunPayload struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(secondRun.Body).Decode(&secondRunPayload); err != nil {
		t.Fatal(err)
	}
	secondRun.Body.Close()
	outOfWindow := requestJSON(t, sellerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/teams/%d/runs/%d/check-in", server.URL, team.ID, secondRunPayload.ID), nil, sellerCSRF)
	if outOfWindow.StatusCode != http.StatusBadRequest {
		t.Fatalf("outside-window check-in: %d %s", outOfWindow.StatusCode, readResponse(outOfWindow))
	}
	outOfWindow.Body.Close()
	if _, err := pool.Exec(context.Background(), "UPDATE team_runs SET starts_at=now()-interval '1 minute' WHERE id=$1", secondRunPayload.ID); err != nil {
		t.Fatal(err)
	}
	lateExcuse := requestJSON(t, buyerClient, http.MethodPost, fmt.Sprintf("%s/api/v1/teams/%d/runs/%d/excuse", server.URL, team.ID, secondRunPayload.ID), nil, buyerCSRF)
	if lateExcuse.StatusCode != http.StatusBadRequest {
		t.Fatalf("post-start excuse: %d %s", lateExcuse.StatusCode, readResponse(lateExcuse))
	}
	lateExcuse.Body.Close()

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
