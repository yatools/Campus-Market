package httpapi

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	apispec "github.com/yatools/wutong-campus-wall/backend/api"
	"github.com/yatools/wutong-campus-wall/backend/internal/config"
	"github.com/yatools/wutong-campus-wall/backend/internal/database"
	governanceapp "github.com/yatools/wutong-campus-wall/backend/internal/governance"
	marketapp "github.com/yatools/wutong-campus-wall/backend/internal/market"
	operationalmetrics "github.com/yatools/wutong-campus-wall/backend/internal/metrics"
	"github.com/yatools/wutong-campus-wall/backend/internal/security"
	storagepkg "github.com/yatools/wutong-campus-wall/backend/internal/storage"
	teamapp "github.com/yatools/wutong-campus-wall/backend/internal/team"
)

type contextKey string

//go:embed assets/redoc.standalone.js
var redocBundle []byte

// ReDoc 2.5.3, bundled under the MIT license.
//
//go:embed assets/REDOC-LICENSE
var redocLicense []byte

const requestIDKey contextKey = "request-id"
const clientIPKey contextKey = "client-ip"

type Server struct {
	Config     config.Config
	DB         *pgxpool.Pool
	Storage    *storagepkg.Store
	Metrics    *operationalmetrics.Registry
	Hub        *notificationHub
	Governance *governanceapp.Service
	Market     *marketapp.Service
	Team       *teamapp.Service
}

func New(cfg config.Config, db *pgxpool.Pool) http.Handler {
	store, err := storagepkg.New(cfg)
	if err != nil {
		slog.Error("storage_config_invalid", "error", err)
	}
	marketRepository := marketapp.NewPostgresRepository(db)
	teamRepository := teamapp.NewPostgresRepository(db)
	governanceRepository := governanceapp.NewPostgresRepository(db)
	s := &Server{
		Config: cfg, DB: db, Storage: store, Metrics: operationalmetrics.Default,
		Hub:        newNotificationHub(db),
		Governance: governanceapp.NewService(governanceRepository, cfg.SecretKey),
		Market: marketapp.NewService(
			marketRepository,
			cfg.MarketReservationTTL,
			cfg.MarketReviewBlindTTL,
		),
		Team: teamapp.NewService(teamRepository),
	}
	r := chi.NewRouter()
	// requestContext must run before recoverer so the panic handler can log and echo the
	// request id, and so panics are still counted by the metrics defer it installs.
	r.Use(s.requestContext, s.recoverer, s.securityHeaders, s.trustedHost, s.cors)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": "HTTP_ERROR", "message": "Not Found", "field_errors": map[string]string{}, "request_id": requestID(r.Context())})
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"code": "HTTP_ERROR", "message": "Method Not Allowed", "field_errors": map[string]string{}, "request_id": requestID(r.Context())})
	})
	r.Get("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "api", "version": "1.0.0"})
	})
	r.Get("/health/ready", s.handle(s.healthReady))
	r.Get("/health/dependencies", s.handle(s.healthDependencies))
	r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) { s.Metrics.ServeHTTP(s.DB, w, r) })
	r.Get("/app-config.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"api_prefix": cfg.APIPrefix, "csrf_cookie_name": cfg.CSRFCookieName})
	})
	if cfg.DocsEnabled {
		r.Get("/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(apispec.OpenAPI)
		})
		r.Get("/docs-legacy", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>梧桐墙 API</title></head><body><redoc spec-url="/openapi.json"></redoc><script src="/docs/redoc.standalone.js"></script></body></html>`)
		})
		r.Get("/docs/redoc.standalone.js", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			_, _ = w.Write(redocBundle)
		})
		r.Get("/docs/licenses/redoc", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write(redocLicense)
		})
		r.Get("/docs", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>API documentation</title><style>body{font:14px system-ui;margin:0;color:#17202a}main{max-width:1000px;margin:auto;padding:32px}article{border-top:1px solid #ddd;padding:12px 0}code{display:inline-block;width:64px;color:#075985}p{color:#52606d}</style></head><body><redoc spec-url="/openapi.json"></redoc><script src="/docs/redoc.standalone.js"></script></body></html>`)
		})
	}
	_ = mime.AddExtensionType(".webp", "image/webp")
	r.Get("/uploads/*", s.servePublicUpload)
	contractValidator, err := openAPIRequestValidator()
	if err != nil {
		panic(err)
	}
	r.Route(cfg.APIPrefix, func(api chi.Router) {
		// Order matters. The contract validator buffers the whole request body to validate
		// it, so the body cap has to come first, and the cheap CSRF check should reject
		// before we spend CPU on schema validation.
		api.Use(s.limitRequestBody)
		api.Use(s.csrfProtection)
		api.Use(contractValidator)
		s.registerRoutes(api)
	})
	return r
}

func (s *Server) servePublicUpload(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/uploads/")
	if key == "" || strings.Contains(key, "..") || strings.Contains(key, "\\") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.Redirect(w, r, s.Storage.PublicURL(key), http.StatusTemporaryRedirect)
}

func (s *Server) healthReady(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.DB.Ping(ctx); err != nil {
		slog.Warn("readiness_database_failed", "error", err, "request_id", requestID(r.Context()))
		return apiError(http.StatusServiceUnavailable, "NOT_READY", "服务尚未就绪")
	}
	var version int64
	// Accept a database that is ahead of this binary: a rollback to the previous image
	// leaves the schema at the newer version, and demanding exact equality would pin the
	// service at NOT_READY with no way out but a manual migrate down.
	if err := s.DB.QueryRow(ctx, "SELECT COALESCE(max(version_id),0) FROM goose_db_version WHERE is_applied=true").Scan(&version); err != nil || version < database.LatestMigrationVersion {
		slog.Warn("readiness_migration_failed", "version", version, "want_at_least", database.LatestMigrationVersion, "error", err, "request_id", requestID(r.Context()))
		return apiError(http.StatusServiceUnavailable, "NOT_READY", "服务尚未就绪")
	}
	if s.Storage == nil {
		return apiError(http.StatusServiceUnavailable, "NOT_READY", "服务尚未就绪")
	}
	if err := s.Storage.Probe(ctx); err != nil {
		slog.Warn("readiness_storage_failed", "error", err, "request_id", requestID(r.Context()))
		return apiError(http.StatusServiceUnavailable, "NOT_READY", "服务尚未就绪")
	}
	writeJSON(w, 200, map[string]any{"status": "ready"})
	return nil
}

func (s *Server) healthDependencies(w http.ResponseWriter, r *http.Request) error {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if s.Config.HealthCheckToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.Config.HealthCheckToken)) != 1 {
		return apiError(http.StatusUnauthorized, "HEALTH_TOKEN_REQUIRED", "需要健康检查令牌")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	components := map[string]any{}
	dbErr := s.DB.Ping(ctx)
	components["database"] = componentStatus(dbErr)
	var storageErr error
	if s.Storage == nil {
		storageErr = fmt.Errorf("对象存储未初始化")
	} else {
		storageErr = s.Storage.Probe(ctx)
	}
	components["object_storage"] = componentStatus(storageErr)
	var version int64
	migrationErr := s.DB.QueryRow(ctx, "SELECT COALESCE(max(version_id),0) FROM goose_db_version WHERE is_applied=true").Scan(&version)
	if migrationErr == nil && version < database.LatestMigrationVersion {
		migrationErr = fmt.Errorf("want at least %d got %d", database.LatestMigrationVersion, version)
	}
	components["migrations"] = componentStatus(migrationErr)
	status := http.StatusOK
	if dbErr != nil || storageErr != nil || migrationErr != nil {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"status": map[bool]string{true: "ok", false: "degraded"}[status == http.StatusOK], "components": components})
	return nil
}

func componentStatus(err error) map[string]any {
	if err == nil {
		return map[string]any{"ok": true}
	}
	return map[string]any{"ok": false, "error": truncate(err.Error(), 300)}
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				slog.Error("panic", "value", value, "stack", string(debug.Stack()), "request_id", requestID(r.Context()))
				s.writeError(w, r, fmt.Errorf("panic: %v", value))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if len(id) > 64 {
			id = id[:64]
		}
		if id == "" {
			id = strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		started := time.Now()
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		ctx = context.WithValue(ctx, clientIPKey, s.resolveClientIP(r))
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		// Record in a defer so a panic unwinding through this middleware still lands in
		// the metrics (the recoverer runs further out and turns it into a 500).
		defer func() {
			// Collapse unmatched requests to a single "unmatched" label instead of the raw
			// URL path: raw paths are attacker-controlled and unbounded, which would both
			// explode metric cardinality and (before escaping) corrupt the exposition.
			route := "unmatched"
			if routeContext := chi.RouteContext(r.Context()); routeContext != nil && routeContext.RoutePattern() != "" {
				route = routeContext.RoutePattern()
			}
			elapsed := time.Since(started)
			s.Metrics.ObserveHTTP(r.Method, route, recorder.status, elapsed)
			if elapsed >= 800*time.Millisecond {
				slog.Warn("slow_request", "method", r.Method, "path", r.URL.Path, "elapsed_ms", elapsed.Milliseconds(), "request_id", id)
			}
		}()
		next.ServeHTTP(recorder, r.WithContext(ctx))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer. Without it every
// SetWriteDeadline/SetReadDeadline call made by a handler (notably the SSE stream, which
// must clear the server's WriteTimeout) silently fails with http.ErrNotSupported.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) resolveClientIP(r *http.Request) string {
	host := r.RemoteAddr
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	remote, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return host
	}
	for _, prefix := range s.Config.TrustedProxyCIDRs {
		if !prefix.Contains(remote) {
			continue
		}
		forwarded, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP")))
		if err == nil {
			return forwarded.Unmap().String()
		}
		break
	}
	return remote.Unmap().String()
}

func requestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

// limitRequestBody caps the request body before anything else reads it. The OpenAPI
// request validator buffers the entire body in memory to check it against the schema,
// and that happens before authentication, so without a cap an anonymous client could
// force arbitrary allocations with a single request.
func (s *Server) limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
			limit := maxJSONBodyBytes
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				limit = s.Config.MaxUploadBytes + 1<<20
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", requestID(r.Context()))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) trustedHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		host = strings.ToLower(strings.Trim(host, "[]"))
		if _, ok := s.Config.TrustedHosts[host]; !ok {
			writeJSON(w, 400, map[string]any{"code": "HTTP_ERROR", "message": "Invalid host header", "field_errors": map[string]string{}, "request_id": requestID(r.Context())})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == s.Config.PublicOrigin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token, X-Request-ID")
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var csrfExempt = map[string]bool{
	"/auth/request-code": true, "/auth/register": true, "/auth/login": true, "/auth/reset-password": true,
}

func (s *Server) csrfProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || csrfExempt[strings.TrimPrefix(r.URL.Path, s.Config.APIPrefix)] {
			next.ServeHTTP(w, r)
			return
		}
		sessionCookie, err := r.Cookie(s.Config.SessionCookieName)
		if err == http.ErrNoCookie {
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			s.writeError(w, r, apiError(403, "CSRF_INVALID", "安全校验失败，请刷新页面后重试"))
			return
		}
		csrfCookie, err := r.Cookie(s.Config.CSRFCookieName)
		header := r.Header.Get("X-CSRF-Token")
		var stored string
		var valid bool
		if err == nil && csrfCookie.Value != "" && header != "" {
			err = s.DB.QueryRow(r.Context(), `SELECT csrf_token FROM sessions
				WHERE (token_hash=$1 OR (previous_token_hash=$1 AND previous_token_expires_at>now()))
				  AND revoked_at IS NULL AND expires_at>now() AND absolute_expires_at>now()`, security.TokenHash(s.Config.SecretKey, sessionCookie.Value)).Scan(&stored)
			valid = err == nil && subtle.ConstantTimeCompare([]byte(csrfCookie.Value), []byte(header)) == 1 && subtle.ConstantTimeCompare([]byte(stored), []byte(header)) == 1
		}
		if !valid {
			s.writeError(w, r, apiError(403, "CSRF_INVALID", "安全校验失败，请刷新页面后重试"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func EnsureDirs(cfg config.Config) error {
	probe, err := os.CreateTemp("", "wutong-write-probe-")
	if err != nil {
		return fmt.Errorf("临时目录不可写: %w", err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		return err
	}
	return os.Remove(filepath.Clean(name))
}

func EnsureStorage(ctx context.Context, cfg config.Config) error {
	store, err := storagepkg.New(cfg)
	if err != nil {
		return err
	}
	if cfg.Environment == "production" {
		return store.Probe(ctx)
	}
	return store.EnsureBuckets(ctx)
}

var _ = middleware.GetReqID
