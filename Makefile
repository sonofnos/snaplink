.PHONY: run test test-integration up down loadtest

run:
	docker compose up -d postgres redis
	go run ./cmd/server

test:
	go test ./...

test-integration:
	go test -tags integration ./... -timeout 5m

up:
	docker compose up -d --build

down:
	docker compose down

loadtest:
	@echo "Usage: CODE=<existing short code> make loadtest"
	k6 run loadtest/redirect.js
