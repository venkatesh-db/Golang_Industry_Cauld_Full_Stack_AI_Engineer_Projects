.PHONY: up down logs test fmt build

up:
	docker compose up --build --remove-orphans

down:
	docker compose down

logs:
	docker compose logs -f --tail=100

test:
	go test ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

build:
	go build ./cmd/rapido
