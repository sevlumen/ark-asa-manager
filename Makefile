.PHONY: config build test up down logs

config:
	docker compose config

build:
	docker compose build control-plane agent web

test:
	docker run --rm -v "$(CURDIR):/src" -w /src golang:1.23-alpine sh -c "go test ./apps/agent/... && go test ./apps/control-plane/... && go test ./cmd/arkctl/... && GOWORK=off go test ./tests/contracts/..."

up:
	docker compose up -d postgres control-plane socket-proxy agent web

down:
	docker compose down

logs:
	docker compose logs -f --tail=100
