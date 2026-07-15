package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	apispec "github.com/yatools/wutong-campus-wall/backend/api"
	"github.com/yatools/wutong-campus-wall/backend/internal/config"
)

func TestEveryCanonicalContractOperationHasRoute(t *testing.T) {
	cfg := config.Config{APIPrefix: "/api/v1", UploadDir: t.TempDir(), BackupDir: t.TempDir(), TrustedHosts: map[string]struct{}{"example.test": {}}, PublicOrigin: "http://localhost:5173"}
	handler := New(cfg, nil)
	routes, ok := handler.(chi.Routes)
	if !ok {
		t.Fatalf("handler does not expose chi routes: %T", handler)
	}
	have := map[string]bool{}
	if err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		have[method+" "+normalizeRoute(route)] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(apispec.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	for path, methods := range spec.Paths {
		for method := range methods {
			upper := strings.ToUpper(method)
			if upper != "GET" && upper != "POST" && upper != "PUT" && upper != "PATCH" && upper != "DELETE" {
				continue
			}
			key := upper + " " + normalizeRoute(path)
			if !have[key] {
				t.Errorf("missing Go route %s %s", upper, path)
			}
		}
	}
}

func TestClientIPIgnoresSpoofedForwardingHeaders(t *testing.T) {
	s := &Server{Config: config.Config{TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}}
	request := httptest.NewRequest(http.MethodGet, "http://example.test/health/live", nil)
	request.RemoteAddr = "203.0.113.8:43210"
	request.Header.Set("X-Forwarded-For", "1.2.3.4")
	request.Header.Set("X-Real-IP", "1.2.3.4")
	if got := s.resolveClientIP(request); got != "203.0.113.8" {
		t.Fatalf("untrusted forwarding header was accepted: %s", got)
	}
}

func TestClientIPAcceptsRealIPOnlyFromTrustedProxy(t *testing.T) {
	s := &Server{Config: config.Config{TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}}
	request := httptest.NewRequest(http.MethodGet, "http://example.test/health/live", nil)
	request.RemoteAddr = "10.1.2.3:43210"
	request.Header.Set("X-Forwarded-For", "198.51.100.9")
	request.Header.Set("X-Real-IP", "2001:db8::1")
	if got := s.resolveClientIP(request); got != "2001:db8::1" {
		t.Fatalf("trusted real ip was not accepted: %s", got)
	}
}

func TestInvalidNumericPathParameterIsRejectedBeforeDatabaseAccess(t *testing.T) {
	cfg := config.Config{APIPrefix: "/api/v1", UploadDir: t.TempDir(), BackupDir: t.TempDir(), TrustedHosts: map[string]struct{}{"example.test": {}}, PublicOrigin: "http://localhost:5173"}
	request := httptest.NewRequest(http.MethodGet, "http://example.test/api/v1/posts/not-a-number", nil)
	response := httptest.NewRecorder()
	New(cfg, nil).ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Code   string            `json:"code"`
		Fields map[string]string `json:"field_errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "VALIDATION_ERROR" || body.Fields["post_id"] == "" {
		t.Fatalf("unexpected validation response: %#v", body)
	}
}

func TestUnknownAPIEndpointUsesStandardErrorEnvelope(t *testing.T) {
	cfg := config.Config{APIPrefix: "/api/v1", UploadDir: t.TempDir(), BackupDir: t.TempDir(), TrustedHosts: map[string]struct{}{"example.test": {}}, PublicOrigin: "http://localhost:5173"}
	request := httptest.NewRequest(http.MethodGet, "http://example.test/api/v1/emails", nil)
	response := httptest.NewRecorder()
	New(cfg, nil).ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Code      string `json:"code"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "HTTP_ERROR" || body.RequestID == "" {
		t.Fatalf("unexpected error envelope: %#v", body)
	}
}

var routeParameter = regexp.MustCompile(`\{[^}]+\}`)

func normalizeRoute(value string) string {
	value = strings.ReplaceAll(value, "/*", "/")
	return routeParameter.ReplaceAllString(value, "{}")
}
