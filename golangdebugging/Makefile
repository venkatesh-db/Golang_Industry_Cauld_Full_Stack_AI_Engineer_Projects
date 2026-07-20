.PHONY: test race vet stack-up stack-down stack-status smoke load failure-load

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

stack-up:
	docker compose up -d --build

stack-down:
	docker compose down

stack-status:
	docker compose ps

smoke:
	./scripts/smoke.sh

load:
	docker compose --profile loadtest run --rm -e RATE=$${RATE:-100} -e DURATION=$${DURATION:-30s} k6 run /scripts/feed.js

failure-load:
	docker compose --profile loadtest run --rm k6 run /scripts/failure.js
