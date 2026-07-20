.DEFAULT_GOAL := help
COMPOSE := docker compose
EDGE := http://localhost:8080

.PHONY: help up down logs ps build test tidy \
        load-baseline load-spike load-write bench-vegeta \
        chaos-goroutine chaos-dbpool chaos-retrystorm chaos-slowdep chaos-mem chaos-reset

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

## --- lifecycle ---
up: ## Build + start the whole stack
	$(COMPOSE) up -d --build
	@echo "Grafana:      http://localhost:3001"
	@echo "Prometheus:   http://localhost:9090"
	@echo "Alertmanager: http://localhost:9093"
	@echo "edge-api:     $(EDGE)/feed/1"

down: ## Stop + remove everything
	$(COMPOSE) down -v

logs: ## Tail all logs
	$(COMPOSE) logs -f

ps: ## Show container status + RAM
	$(COMPOSE) ps
	@docker stats --no-stream

## --- go ---
build: ## Compile all services
	go build ./...

test: ## Run unit tests
	go test ./...

tidy: ## Resolve go.mod
	go mod tidy

## --- load tests ---
load-baseline: ## k6 steady-RPS feed read
	k6 run loadtest/k6/feed_baseline.js

load-spike: ## k6 ramp/spike
	k6 run loadtest/k6/feed_spike.js

load-write: ## k6 like write path
	k6 run loadtest/k6/like_write.js

bench-vegeta: ## Vegeta fixed-rate benchmark
	loadtest/vegeta/feed.sh

## --- chaos (requires CHAOS_ENABLED=true) ---
chaos-goroutine: ## Trigger goroutine leak on edge-api
	curl -s -XPOST "$(EDGE)/chaos/goroutine-leak?n=2000"; echo

chaos-dbpool: ## Exhaust counter-service DB pool
	curl -s -XPOST "http://localhost:8082/chaos/db-pool-exhaust?n=20"; echo

chaos-retrystorm: ## Force counter-service failures -> retry storm + breaker
	curl -s -XPOST "http://localhost:8082/chaos/retry-storm"; echo

chaos-slowdep: ## Inject 800ms latency in feed-service
	curl -s -XPOST "http://localhost:8081/chaos/slow-dep?ms=800"; echo

chaos-mem: ## Allocate 512MB on edge-api
	curl -s -XPOST "$(EDGE)/chaos/mem-pressure?mb=512"; echo

chaos-reset: ## Reset chaos on all services
	@for p in 8080 8081 8082; do curl -s -XDELETE "http://localhost:$$p/chaos/reset" >/dev/null; done; echo "reset all"
