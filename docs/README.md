# API Documentation

The canonical OpenAPI spec lives at
[`internal/handler/openapi.yaml`](../internal/handler/openapi.yaml), not
here — Go's `go:embed` directive can only embed files from the same
directory as the `.go` file using it (or a subdirectory of it), so the spec
has to be co-located with `docs_handler.go` rather than in this top-level
`docs/` folder.

## Viewing it

With the server running locally:

- **Interactive Swagger UI**: http://localhost:8080/docs
- **Raw spec** (for importing into Postman/Insomnia, or any OpenAPI-aware
  tool): http://localhost:8080/docs/openapi.yaml

The spec is embedded into the compiled binary at build time — no
filesystem path dependency, and no risk of the docs going stale relative
to a separately-deployed file.
