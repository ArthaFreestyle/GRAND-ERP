// Package docs embeds the OpenAPI contract into the binary so the running server
// can serve it without needing docs/openapi.yaml on disk beside it.
//
// The embed directive has to live in this directory: go:embed only reaches files
// in the package's own directory or below, so it cannot be written from
// internal/delivery.
//
// Consequence worth knowing when changing the build: openapi.yaml is now a build
// input. Leaving docs/ out of the Docker build context breaks compilation rather
// than merely omitting the docs page, which is why .dockerignore does not exclude
// it and the Dockerfile copies it in.
package docs

import _ "embed"

// OpenAPI is docs/openapi.yaml verbatim. Served as-is; Swagger UI parses YAML.
//
//go:embed openapi.yaml
var OpenAPI []byte
