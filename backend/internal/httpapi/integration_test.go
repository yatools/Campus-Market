package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/yatools/wutong-campus-wall/backend/internal/config"
	"github.com/yatools/wutong-campus-wall/backend/internal/database"
	"github.com/yatools/wutong-campus-wall/backend/internal/security"
)

func TestPostgresAuthAndTreeholeWorkflow(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	cfg := config.Config{Environment: "test", APIPrefix: "/api/v1", PublicOrigin: "http://localhost:5173", SecretKey: "test-secret-key-that-is-long-enough-for-ci", DatabaseURL: dsn, DBPoolSize: 5, AllowedCampusEmailDomains: map[string]struct{}{"test.edu.cn": {}}, SessionCookieName: "wutong_session", CSRFCookieName: "wutong_csrf", SessionSliding: 7 * 24 * time.Hour, SessionAbsolute: 30 * 24 * time.Hour, SessionRotation: 24 * time.Hour, UploadDir: t.TempDir(), BackupDir: t.TempDir(), MaxUploadBytes: 8 << 20, DocsEnabled: true, TrustedHosts: map[string]struct{}{"127.0.0.1": {}, "localhost": {}}}
	if endpoint := os.Getenv("TEST_S3_ENDPOINT"); endpoint != "" {
		cfg.S3Endpoint = endpoint
		cfg.S3Region = "us-east-1"
		cfg.S3AccessKeyID = os.Getenv("S3_ACCESS_KEY_ID")
		cfg.S3SecretAccessKey = os.Getenv("S3_SECRET_ACCESS_KEY")
		cfg.S3PublicBucket = "wutong-test-public"
		cfg.S3PrivateBucket = "wutong-test-private"
		cfg.S3BackupBucket = "wutong-test-backups"
		cfg.S3PublicBaseURL = endpoint + "/wutong-test-public"
		if err := EnsureStorage(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
	}
	sqlDB, err := database.OpenSQL(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sqlDB.Exec("DROP SCHEMA public CASCADE"); err != nil {
		t.Fatal(err)
	}
	if _, err = sqlDB.Exec("CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	sqlDB.Close()
	if err := database.Migrate(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	code := "123456"
	email := "go@test.edu.cn"
	_, err = pool.Exec(context.Background(), `INSERT INTO verification_codes(email,purpose,code_hash,ip_address,expires_at,created_at) VALUES($1,'register',$2,'test',now()+interval '1 hour',now())`, email, security.CodeHash(cfg.SecretKey, email, "register", code))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(cfg, pool))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	register := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/register", map[string]any{"email": email, "code": code, "password": "SafePassword123", "nickname": "Go同学", "agreed_terms_version": "test"}, "")
	if register.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %s", register.StatusCode, readResponse(register))
	}
	var csrf string
	for _, cookie := range jar.Cookies(register.Request.URL) {
		if cookie.Name == cfg.CSRFCookieName {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("missing CSRF cookie")
	}
	if _, err := exec.LookPath("vipsthumbnail"); err == nil && cfg.S3Endpoint != "" {
		upload := uploadTestPNG(t, client, server.URL+"/api/v1/uploads/images", csrf, "image/png")
		if upload.StatusCode != http.StatusCreated {
			t.Fatalf("upload image: %d %s", upload.StatusCode, readResponse(upload))
		}
		var uploaded struct {
			URL          string `json:"url"`
			ThumbnailURL string `json:"thumbnail_url"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
		}
		if err := json.NewDecoder(upload.Body).Decode(&uploaded); err != nil {
			t.Fatal(err)
		}
		upload.Body.Close()
		if uploaded.Width != 20 || uploaded.Height != 20 {
			t.Fatalf("unexpected upload dimensions: %#v", uploaded)
		}
		for _, path := range []string{uploaded.URL, uploaded.ThumbnailURL} {
			response, err := client.Get(server.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			generated, format, err := image.DecodeConfig(response.Body)
			response.Body.Close()
			if err != nil || format != "webp" || generated.Width != 20 || generated.Height != 20 {
				t.Fatalf("generated image changed dimensions or format: %s %#v %s %v", path, generated, format, err)
			}
		}
		mismatch := uploadTestPNG(t, client, server.URL+"/api/v1/uploads/images", csrf, "image/jpeg")
		if mismatch.StatusCode != http.StatusBadRequest {
			t.Fatalf("mime mismatch: %d %s", mismatch.StatusCode, readResponse(mismatch))
		}
		var mismatchBody struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(mismatch.Body).Decode(&mismatchBody); err != nil {
			t.Fatal(err)
		}
		mismatch.Body.Close()
		if mismatchBody.Code != "IMAGE_MIME_MISMATCH" {
			t.Fatalf("unexpected mismatch code: %s", mismatchBody.Code)
		}
	}
	post := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/posts", map[string]any{"title": "Go 契约测试", "body": "这是一条由 Go 集成测试发布的树洞内容", "identity_mode": "nickname", "visibility": "forever", "allow_comments": true}, csrf)
	if post.StatusCode != http.StatusCreated {
		t.Fatalf("create post: %d %s", post.StatusCode, readResponse(post))
	}
	list, err := client.Get(server.URL + "/api/v1/posts")
	if err != nil {
		t.Fatal(err)
	}
	defer list.Body.Close()
	if list.StatusCode != 200 {
		t.Fatalf("list posts: %d", list.StatusCode)
	}
	var payload struct {
		Total int `json:"total"`
	}
	if err := json.NewDecoder(list.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Total != 1 {
		t.Fatalf("expected one post, got %d", payload.Total)
	}

	gamesResponse, err := client.Get(server.URL + "/api/v1/team-games")
	if err != nil {
		t.Fatal(err)
	}
	if gamesResponse.StatusCode != http.StatusOK {
		t.Fatalf("team games: %d %s", gamesResponse.StatusCode, readResponse(gamesResponse))
	}
	var games struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(gamesResponse.Body).Decode(&games); err != nil {
		t.Fatal(err)
	}
	gamesResponse.Body.Close()
	if len(games.Items) == 0 {
		t.Fatal("team game catalog was not initialized")
	}

	teamResponse := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/teams", map[string]any{
		"game_id": games.Items[0].ID, "mode": "pgx 生命周期回归", "capacity": 5,
		"starts_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}, csrf)
	if teamResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create team: %d %s", teamResponse.StatusCode, readResponse(teamResponse))
	}
	var team struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(teamResponse.Body).Decode(&team); err != nil {
		t.Fatal(err)
	}
	teamResponse.Body.Close()
	if _, err := pool.Exec(context.Background(), "UPDATE team_runs SET expires_at=NULL WHERE team_id=$1", team.ID); err != nil {
		t.Fatal(err)
	}
	teamsResponse, err := client.Get(server.URL + "/api/v1/teams?page_size=50")
	if err != nil {
		t.Fatal(err)
	}
	if teamsResponse.StatusCode != http.StatusOK {
		t.Fatalf("team lifecycle query: %d %s", teamsResponse.StatusCode, readResponse(teamsResponse))
	}
	teamsResponse.Body.Close()

	adminHash, err := security.HashPassword("AdminPassword123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO users(email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,dm_stranger_off,hide_online,verified_at,created_at,updated_at)
		VALUES('admin@test.edu.cn',$1,'管理员','梧桐#integration-admin','staff','admin','active',1000,0,false,false,now(),now(),now())`, adminHash); err != nil {
		t.Fatal(err)
	}
	adminJar, _ := cookiejar.New(nil)
	adminClient := &http.Client{Jar: adminJar}
	login := requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{"email": "admin@test.edu.cn", "password": "AdminPassword123"}, "")
	if login.StatusCode != http.StatusOK {
		t.Fatalf("admin login: %d %s", login.StatusCode, readResponse(login))
	}
	login.Body.Close()
	adminCSRF := ""
	for _, cookie := range adminJar.Cookies(login.Request.URL) {
		if cookie.Name == cfg.CSRFCookieName {
			adminCSRF = cookie.Value
		}
	}
	announcement := requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/announcements", map[string]any{
		"title": "事务结果集回归", "body": "验证强提醒通知和邮件队列", "level": "strong", "audience": "all",
	}, adminCSRF)
	if announcement.StatusCode != http.StatusCreated {
		t.Fatalf("strong announcement: %d %s", announcement.StatusCode, readResponse(announcement))
	}
	announcement.Body.Close()
	var queued int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM email_outbox WHERE status='pending'").Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued < 2 {
		t.Fatalf("expected strong announcement email for active users, got %d", queued)
	}

	publicReads := []string{
		"/api/v1/posts", "/api/v1/hot", "/api/v1/feed",
		"/api/v1/feed/changes?after=" + url.QueryEscape(time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)),
		"/api/v1/teams", "/api/v1/team-games", "/api/v1/campus-services", "/api/v1/questions",
		"/api/v1/handbook", "/api/v1/course-offerings", "/api/v1/listings", "/api/v1/activities",
		"/api/v1/lost-items", "/api/v1/observe-posts", "/api/v1/penalties", "/api/v1/announcements",
		"/api/v1/credit-rules", "/api/v1/search?q=Go",
	}
	assertReadsOK(t, client, server.URL, publicReads)
	memberReads := []string{
		"/api/v1/me", "/api/v1/me/sessions", "/api/v1/me/content", "/api/v1/me/favorites",
		"/api/v1/me/reports", "/api/v1/me/appeals", "/api/v1/conversations", "/api/v1/notifications",
	}
	assertReadsOK(t, client, server.URL, memberReads)
	adminReads := []string{
		"/api/v1/admin/overview", "/api/v1/admin/users", "/api/v1/admin/moderation-cases",
		"/api/v1/admin/reports", "/api/v1/admin/appeals", "/api/v1/admin/settings",
		"/api/v1/admin/feedback", "/api/v1/admin/backups", "/api/v1/admin/audit-logs",
		"/api/v1/admin/credit-rules", "/api/v1/admin/game-submissions",
	}
	assertReadsOK(t, adminClient, server.URL, adminReads)
}

func assertReadsOK(t *testing.T, client *http.Client, baseURL string, paths []string) {
	t.Helper()
	for _, path := range paths {
		response, err := client.Get(baseURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, response.StatusCode, readResponse(response))
		}
		response.Body.Close()
	}
}

func requestJSON(t *testing.T, client *http.Client, method, url string, body any, csrf string) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func uploadTestPNG(t *testing.T, client *http.Client, url, csrf, declaredType string) *http.Response {
	t.Helper()
	var imageBytes bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 20, 20))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{R: 24, G: 96, B: 48, A: 255}), image.Point{}, draw.Src)
	if err := png.Encode(&imageBytes, canvas); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="test.png"`)
	header.Set("Content-Type", declaredType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(imageBytes.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-CSRF-Token", csrf)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func readResponse(response *http.Response) string {
	defer response.Body.Close()
	var value any
	_ = json.NewDecoder(response.Body).Decode(&value)
	data, _ := json.Marshal(value)
	return string(data)
}
