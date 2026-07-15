package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
)

type APIError struct {
	Status      int
	Code        string
	Message     string
	FieldErrors map[string]string
}

func (e *APIError) Error() string { return e.Message }

func apiError(status int, code, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message, FieldErrors: map[string]string{}}
}

type envelopeHandler func(http.ResponseWriter, *http.Request) error

func (s *Server) handle(fn envelopeHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
			for index, key := range routeContext.URLParams.Keys {
				if !strings.HasSuffix(strings.ToLower(key), "id") || index >= len(routeContext.URLParams.Values) {
					continue
				}
				value, err := strconv.ParseInt(routeContext.URLParams.Values[index], 10, 64)
				if err != nil || value < 1 {
					s.writeError(w, r, validation(camelToSnake(key), "Input should be a valid integer"))
					return
				}
			}
		}
		if err := fn(w, r); err != nil {
			s.writeError(w, r, err)
		}
	}
}

func camelToSnake(value string) string {
	if strings.HasSuffix(value, "ID") {
		return strings.ToLower(value[:len(value)-2]) + "_id"
	}
	var result strings.Builder
	for index, char := range value {
		if unicode.IsUpper(char) && index > 0 {
			result.WriteByte('_')
		}
		result.WriteRune(unicode.ToLower(char))
	}
	return result.String()
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var api *APIError
	if !errors.As(err, &api) {
		slog.Error("unhandled_error", "path", r.URL.Path, "request_id", requestID(r.Context()), "error", err)
		api = apiError(http.StatusInternalServerError, "INTERNAL_ERROR", "服务器暂时无法处理该请求")
	}
	writeJSON(w, api.Status, map[string]any{
		"code": api.Code, "message": api.Message, "field_errors": api.FieldErrors, "request_id": requestID(r.Context()),
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
