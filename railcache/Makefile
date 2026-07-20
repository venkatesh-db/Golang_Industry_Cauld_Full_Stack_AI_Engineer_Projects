.PHONY: up down seed run build test tidy herd

# Start Postgres + Redis (app runs on host via `make run`).
up:
	docker compose up -d postgres redis

down:
	docker compose down

# Apply schema + seed data.
seed:
	docker compose exec -T postgres psql -U railcache -d railcache < internal/store/schema.sql
	docker compose exec -T postgres psql -U railcache -d railcache < internal/store/seed.sql

# Run the service on the host against the compose datastores.
# ADMIN_TOKEN enables the internal admin/herd/metrics routes on :9090.
run:
	ADMIN_TOKEN=$${ADMIN_TOKEN:-dev-admin-token} go run ./cmd/railcache

build:
	go build -o bin/railcache ./cmd/railcache

test:
	go test -race ./...

tidy:
	go mod tidy

# Fire a thundering herd at the hot key (admin route on the internal listener).
herd:
	curl -s -H "Authorization: Bearer $${ADMIN_TOKEN:-dev-admin-token}" \
	  "http://localhost:9090/debug/herd?from=NDLS&to=BCT&date=2026-07-20&class=3A&n=1000&concurrency=200" | python3 -m json.tool
