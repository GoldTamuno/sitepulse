package handler

import (
	"embed"
	"log/slog"
	"net/http"
)

// DocsHandler serves the OpenAPI spec itself and a browsable Swagger UI on
// top of it. The spec file is embedded into the compiled binary using Go's
// embed directive (see the line above specFS below) — this means API docs
// ship as part of the binary with zero runtime file-path dependencies (no
// risk of "docs/openapi.yaml not found" because of a wrong working
// directory in Docker/Railway), and no separate deploy step to keep the
// docs in sync with the running binary.
type DocsHandler struct {
	spec []byte
}

//go:embed openapi.yaml
var specFS embed.FS

func NewDocsHandler() (*DocsHandler, error) {
	data, err := specFS.ReadFile("openapi.yaml")
	if err != nil {
		return nil, err
	}
	return &DocsHandler{spec: data}, nil
}

// Spec serves the raw OpenAPI YAML — useful for importing into Postman,
// Insomnia, or any other OpenAPI-aware tool, not just the bundled UI below.
func (h *DocsHandler) Spec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	if _, err := w.Write(h.spec); err != nil {
		slog.Default().Error("failed to write OpenAPI spec response", "error", err)
	}
}

// UI serves a minimal HTML shell that loads Swagger UI's static assets
// from a CDN (jsDelivr) and points it at /docs/openapi.yaml. We don't
// vendor Swagger UI's JS/CSS into this repo — it's a few megabytes of
// third-party static assets that would need manual version updates; a
// pinned CDN version is simpler here and standard practice for
// documentation UIs specifically (unlike the actual API server, which
// must work with zero external calls, docs are allowed to depend on a
// CDN being reachable).
func (h *DocsHandler) UI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(swaggerUIHTML)); err != nil {
		slog.Default().Error("failed to write Swagger UI response", "error", err)
	}
}

const swaggerUIHTML = `<!DOCTYPE html>
<html>
<head>
  <title>SitePulse API Docs</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css" />
  <style>body { margin: 0; }</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = () => {
      SwaggerUIBundle({
        url: "/docs/openapi.yaml",
        dom_id: "#swagger-ui",
        presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
      });
    };
  </script>
</body>
</html>`
