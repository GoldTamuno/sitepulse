# --- Build stage ---
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go.mod/go.sum first so this layer is cached unless dependencies
# actually change — avoids re-downloading modules on every code edit.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /sitepulse ./cmd/api

# --- Run stage ---
# A distroless/alpine final image keeps the deployed artifact small and
# reduces attack surface — no shell, no package manager, just the binary.
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /sitepulse /sitepulse

EXPOSE 8080

ENTRYPOINT ["/sitepulse"]
