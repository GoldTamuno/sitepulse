.PHONY: run build test lint tidy docker-up docker-down migrate-up migrate-down

run:
	go run ./cmd/api

build:
	go build -o bin/sitepulse ./cmd/api

test:
	go test -v -race -cover ./...

lint:
	go vet ./...

tidy:
	go mod tidy

docker-up:
	docker compose -f deployments/docker-compose.yml up --build

docker-down:
	docker compose -f deployments/docker-compose.yml down

# Requires: https://github.com/golang-migrate/migrate (added Phase 2)
migrate-up:
	migrate -path migrations -database "$$DATABASE_URL" up

migrate-down:
	migrate -path migrations -database "$$DATABASE_URL" down 1
