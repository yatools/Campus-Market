package httpapi

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
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
	"github.com/yatools/wutong-campus-wall/backend/internal/security"
)

type contextKey string

const requestIDKey contextKey = "request-id"

type Server struct {
	Config config.Config
	DB     *pgxpool.Pool
}

func New(cfg config.Config, db *pgxpool.Pool) http.Handler {
	s := &Server{Config: cfg, DB: db}
	r := chi.NewRouter()
	r.Use(s.recoverer, s.requestContext, s.securityHeaders, s.trustedHost, s.cors)
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
	if cfg.DocsEnabled {
		r.Get("/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(apispec.OpenAPI)
		})
		r.Get("/docs", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>梧桐墙 API</title></head><body><redoc spec-url="/openapi.json"></redoc><script src="https://cdn.redoc.ly/redoc/latest/bundles/redoc.standalone.js"></script></body></html>`)
		})
	}
	_ = mime.AddExtensionType(".webp", "image/webp")
	uploads := http.StripPrefix("/uploads/", http.FileServer(http.Dir(cfg.UploadDir)))
	r.Handle("/uploads/*", uploads)
	r.Route(cfg.APIPrefix, func(api chi.Router) {
		api.Use(s.csrfProtection)
		s.registerRoutes(api)
	})
	return r
}

func (s *Server) healthReady(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.DB.Ping(ctx); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"status": "ready"})
	return nil
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
		next.ServeHTTP(w, r.WithContext(ctx))
		if elapsed := time.Since(started); elapsed >= 800*time.Millisecond {
			slog.Warn("slow_request", "method", r.Method, "path", r.URL.Path, "elapsed_ms", elapsed.Milliseconds(), "request_id", id)
		}
	})
}

func requestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
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
			err = s.DB.QueryRow(r.Context(), `SELECT csrf_token FROM sessions WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>now() AND absolute_expires_at>now()`, security.TokenHash(s.Config.SecretKey, sessionCookie.Value)).Scan(&stored)
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
	for _, dir := range []string{cfg.UploadDir, cfg.BackupDir} {
		if err := os.MkdirAll(filepath.Clean(dir), 0o750); err != nil {
			return err
		}
	}
	return nil
}

var _ = middleware.GetReqID
