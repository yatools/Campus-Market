package api

import _ "embed"

// OpenAPI is the compatibility baseline exported from the final FastAPI implementation.
//
//go:embed openapi.json
var OpenAPI []byte
