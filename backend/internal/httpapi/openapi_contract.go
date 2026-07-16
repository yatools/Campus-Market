package httpapi

import (
	"fmt"
	"net/http"
	"regexp"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"

	generated "github.com/yatools/wutong-campus-wall/backend/internal/openapi"
)

func openAPIRequestValidator() (func(http.Handler) http.Handler, error) {
	spec, err := generated.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load OpenAPI contract: %w", err)
	}
	spec.Servers = nil
	router, err := legacy.NewRouter(spec, openapi3.AllowExtraSiblingFields("contentMediaType", "contentEncoding"))
	if err != nil {
		return nil, fmt.Errorf("index OpenAPI contract: %w", err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route, pathParams, err := router.FindRoute(r)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			input := &openapi3filter.RequestValidationInput{Request: r, PathParams: pathParams, Route: route}
			if err := openapi3filter.ValidateRequest(r.Context(), input); err != nil {
				field := "request"
				if match := regexp.MustCompile(`parameter "([^"]+)"`).FindStringSubmatch(err.Error()); len(match) == 2 {
					field = match[1]
				}
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"code": "VALIDATION_ERROR", "message": "Request does not match the API contract", "field_errors": map[string]string{field: err.Error()}, "request_id": requestID(r.Context())})
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}
