package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"golangdebugging/failurelab"
	"golangdebugging/internal/domain"
	"golangdebugging/internal/observability"
	"golangdebugging/internal/platform"
	"golangdebugging/internal/service"
	"golangdebugging/telemetry"
)

func main() {
	startup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runtime, err := service.New(startup, service.Config{
		Name:         "recommendation-service",
		Routes:       []string{"/healthz", "/readyz", "/v1/recommendations"},
		Dependencies: map[string][]string{"redis": {"get_recommendations", "set_recommendations"}},
	})
	if err != nil {
		panic(err)
	}
	defer func() {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = runtime.Shutdown(ctx)
	}()
	cache, err := platform.OpenRedis(startup, service.Env("REDIS_ADDR", "localhost:6379"), service.EnvInt("REDIS_POOL_SIZE", 20))
	if err != nil {
		runtime.Logger.Error("connect Redis", "error", err)
		return
	}
	defer cache.Close()
	handler := &recommendationHandler{cache: cache, metrics: runtime.Metrics}
	labToken := service.Env("FAILURE_LAB_TOKEN", "local-failure-lab-token")
	lab := failurelab.New(failurelab.Config{
		Enabled: service.EnvBool("FAILURE_LAB_ENABLED", false),
		Token:   labToken,
		Logger:  runtime.Logger,
	})

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", runtime.Wrap("/healthz", http.HandlerFunc(noContent)))
	mux.Handle("GET /readyz", runtime.Wrap("/readyz", redisReadiness(cache)))
	mux.Handle("GET /v1/recommendations", runtime.Wrap("/v1/recommendations", http.HandlerFunc(handler.recommendations)))
	runtime.MountOperations(mux, lab)
	if err := runtime.ListenAndServe(service.Env("LISTEN_ADDR", ":8081"), mux); err != nil {
		runtime.Logger.Error("recommendation service stopped", "error", err)
	}
}

type recommendationHandler struct {
	cache   *redis.Client
	metrics *telemetry.Metrics
}

func (h *recommendationHandler) recommendations(writer http.ResponseWriter, request *http.Request) {
	userID, err := strconv.ParseInt(request.URL.Query().Get("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		userID = 1
	}
	key := "recommendations:" + strconv.FormatInt(userID, 10)
	ctx, span := observability.StartDependencySpan(request.Context(), "redis", "get_recommendations")
	done := h.metrics.StartDependencyContext(ctx, "redis", "get_recommendations")
	items, err := h.cache.LRange(ctx, key, 0, 9).Result()
	observability.RecordError(span, err)
	span.End()
	done(dependencyStatus(err))
	if err != nil && !errors.Is(err, redis.Nil) {
		http.Error(writer, "recommendations unavailable", http.StatusServiceUnavailable)
		return
	}
	if len(items) == 0 {
		items = []string{"distributed-systems", "go-performance", "observability"}
		ctx, setSpan := observability.StartDependencySpan(request.Context(), "redis", "set_recommendations")
		setDone := h.metrics.StartDependencyContext(ctx, "redis", "set_recommendations")
		values := make([]any, len(items))
		for index := range items {
			values[index] = items[index]
		}
		pipeline := h.cache.TxPipeline()
		pipeline.RPush(ctx, key, values...)
		pipeline.Expire(ctx, key, time.Minute)
		_, err = pipeline.Exec(ctx)
		observability.RecordError(setSpan, err)
		setSpan.End()
		setDone(dependencyStatus(err))
	}
	writeJSON(writer, http.StatusOK, domain.Recommendations{UserID: userID, Items: items})
}

func redisReadiness(cache *redis.Client) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), time.Second)
		defer cancel()
		if err := cache.Ping(ctx).Err(); err != nil {
			http.Error(writer, "redis unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}
}

func dependencyStatus(err error) telemetry.DependencyStatus {
	if err == nil || errors.Is(err, redis.Nil) {
		return telemetry.DependencySuccess
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return telemetry.DependencyTimeout
	}
	return telemetry.DependencyError
}

func noContent(writer http.ResponseWriter, request *http.Request) {
	writer.WriteHeader(http.StatusNoContent)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
