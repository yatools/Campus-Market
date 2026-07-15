package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestDecodeStrictBodyRejectsUnknownAndMultipleObjects(t *testing.T) {
	var body struct {
		Title string `json:"title"`
	}
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"title":"ok","unknown":true}`))
	if err := decodeStrictBody(request, &body); err == nil {
		t.Fatal("unknown field accepted")
	}
	request = httptest.NewRequest("POST", "/", strings.NewReader(`{"title":"ok"}{"title":"again"}`))
	if err := decodeStrictBody(request, &body); err == nil {
		t.Fatal("multiple JSON objects accepted")
	}
}

func TestMarketOptionValidation(t *testing.T) {
	if err := validateMarketOption(marketOptionInput{Name: "数码", Slug: "digital", SortOrder: 10}, 60); err != nil {
		t.Fatalf("valid option rejected: %v", err)
	}
	for _, input := range []marketOptionInput{{Name: "", Slug: "digital"}, {Name: "ok", Slug: "Bad Slug"}, {Name: "ok", Slug: "ok", SortOrder: 20000}} {
		if err := validateMarketOption(input, 60); err == nil {
			t.Fatalf("invalid option accepted: %#v", input)
		}
	}
}

func TestUniqueViolationClassification(t *testing.T) {
	if !isUniqueViolation(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("unique violation not recognized")
	}
	if isUniqueViolation(&pgconn.PgError{Code: "23503"}) {
		t.Fatal("foreign-key error misclassified")
	}
}
