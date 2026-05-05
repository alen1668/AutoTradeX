.PHONY: test test-v lint build run migrate-up migrate-down migrate-status pg-up pg-down

GOOSE_DRIVER ?= postgres
GOOSE_DBSTRING ?= postgres://tvbot:tvbot@localhost:5432/tvbot?sslmode=disable
MIGRATIONS_DIR := migrations

test:
	go test ./...

test-v:
	go test -race -v ./...

lint:
	go vet ./...

build:
	go build -o bin/tvbot ./cmd/tvbot

run: build
	./bin/tvbot

pg-up:
	docker compose up -d postgres

pg-down:
	docker compose down

migrate-up:
	GOOSE_DRIVER=$(GOOSE_DRIVER) GOOSE_DBSTRING="$(GOOSE_DBSTRING)" go run github.com/pressly/goose/v3/cmd/goose -dir $(MIGRATIONS_DIR) up

migrate-down:
	GOOSE_DRIVER=$(GOOSE_DRIVER) GOOSE_DBSTRING="$(GOOSE_DBSTRING)" go run github.com/pressly/goose/v3/cmd/goose -dir $(MIGRATIONS_DIR) down

migrate-status:
	GOOSE_DRIVER=$(GOOSE_DRIVER) GOOSE_DBSTRING="$(GOOSE_DBSTRING)" go run github.com/pressly/goose/v3/cmd/goose -dir $(MIGRATIONS_DIR) status
