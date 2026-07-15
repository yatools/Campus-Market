package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	apispec "github.com/yatools/wutong-campus-wall/backend/api"
	"github.com/yatools/wutong-campus-wall/backend/internal/config"
)

func TestEveryFastAPIContractOperationHasGoRoute(t *testing.T) {
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
	operations := 0
	for path, methods := range spec.Paths {
		for method := range methods {
			upper := strings.ToUpper(method)
			if upper != "GET" && upper != "POST" && upper != "PUT" && upper != "PATCH" && upper != "DELETE" {
				continue
			}
			operations++
			key := upper + " " + normalizeRoute(path)
			if !have[key] {
				t.Errorf("missing Go route %s %s", upper, path)
			}
		}
	}
	if operations != 134 {
		t.Fatalf("baseline operation count changed: %d", operations)
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

func TestUnknownAPIEndpointUsesTheCompatibilityErrorEnvelope(t *testing.T) {
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
