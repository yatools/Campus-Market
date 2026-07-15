package api

import _ "embed"

// OpenAPI is the canonical contract for the Go/PostgreSQL implementation.
//
//go:embed openapi.yaml
var OpenAPI []byte
