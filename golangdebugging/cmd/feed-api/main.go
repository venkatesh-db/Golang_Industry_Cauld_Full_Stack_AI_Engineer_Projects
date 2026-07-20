package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"

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
		Name:   "feed-api",
		Routes: []string{"/healthz", "/readyz", "/v1/feed"},
		Dependencies: map[string][]string{
			"postgres":       {"read_feed"},
			"redis":          {"get_feed", "set_feed"},
			"recommendation": {"list"},
			"profile":        {"get"},
		},
	})
	if err != nil {
		panic(err)
	}
	defer func() {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = runtime.Shutdown(ctx)
	}()

	database, err := platform.OpenPostgres(startup, service.Env("DATABASE_URL", "postgres://observability:observability@localhost:5432/observability?sslmode=disable"), int32(service.EnvInt("POSTGRES_MAX_CONNS", 10)))
	if err != nil {
		runtime.Logger.Error("connect PostgreSQL", "error", err)
		return
	}
	defer database.Close()
	cache, err := platform.OpenRedis(startup, service.Env("REDIS_ADDR", "localhost:6379"), service.EnvInt("REDIS_POOL_SIZE", 20))
	if err != nil {
		runtime.Logger.Error("connect Redis", "error", err)
		return
	}
	defer cache.Close()

	client := runtime.Traces.HTTPClient(2 * time.Second)
	handler := &feedHandler{
		database:          database,
		cache:             cache,
		metrics:           runtime.Metrics,
		logger:            runtime.Logger,
		client:            client,
		recommendationURL: service.Env("RECOMMENDATION_URL", "http://localhost:8081"),
		profileURL:        service.Env("PROFILE_URL", "http://localhost:8082"),
	}
	labToken := service.Env("FAILURE_LAB_TOKEN", "local-failure-lab-token")
	lab := failurelab.New(failurelab.Config{
		Enabled:        service.EnvBool("FAILURE_LAB_ENABLED", false),
		Token:          labToken,
		Logger:         runtime.Logger,
		Database:       database,
		HTTPClient:     client,
		RetryTargetURL: handler.recommendationURL + "/internal/failure-lab/always-fail",
		MaxDBHolders:   service.EnvInt("FAILURE_LAB_MAX_DB_HOLDERS", 16),
	})

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", runtime.Wrap("/healthz", http.HandlerFunc(noContent)))
	mux.Handle("GET /readyz", runtime.Wrap("/readyz", readiness(database, cache)))
	mux.Handle("GET /v1/feed", runtime.Wrap("/v1/feed", http.HandlerFunc(handler.feed)))
	runtime.MountOperations(mux, lab)
	if err := runtime.ListenAndServe(service.Env("LISTEN_ADDR", ":8080"), mux); err != nil {
		runtime.Logger.Error("feed API stopped", "error", err)
	}
}

type feedHandler struct {
	database *pgxpool.Pool
	cache    *redis.Client
	metrics  *telemetry.Metrics
	logger   interface {
		ErrorContext(context.Context, string, ...any)
		WarnContext(context.Context, string, ...any)
	}
	client            *http.Client
	recommendationURL string
	profileURL        string
}

func (h *feedHandler) feed(writer http.ResponseWriter, request *http.Request) {
	userID, err := strconv.ParseInt(request.URL.Query().Get("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		userID = 1
	}
	ctx := request.Context()
	cacheKey := fmt.Sprintf("feed:v1:%d", userID)
	if feed, found := h.cachedFeed(ctx, cacheKey); found {
		feed.CacheHit = true
		writeJSON(writer, http.StatusOK, feed)
		return
	}

	feed := domain.Feed{User: domain.Profile{ID: userID}, Recommendations: domain.Recommendations{UserID: userID}}
	group, groupContext := errgroup.WithContext(ctx)
	group.Go(func() error {
		posts, queryErr := h.posts(groupContext)
		feed.Posts = posts
		return queryErr
	})
	group.Go(func() error {
		profile, profileErr := h.profile(groupContext, userID)
		if profileErr != nil {
			h.logger.WarnContext(groupContext, "profile service degraded", "error", profileErr)
			return nil
		}
		feed.User = profile
		return nil
	})
	group.Go(func() error {
		recommendations, recommendationErr := h.recommendations(groupContext, userID)
		if recommendationErr != nil {
			h.logger.WarnContext(groupContext, "recommendation service degraded", "error", recommendationErr)
			return nil
		}
		feed.Recommendations = recommendations
		return nil
	})
	if err := group.Wait(); err != nil {
		h.logger.ErrorContext(ctx, "build feed", "error", err)
		http.Error(writer, "feed unavailable", http.StatusServiceUnavailable)
		return
	}
	h.cacheFeed(ctx, cacheKey, feed)
	writeJSON(writer, http.StatusOK, feed)
}

func (h *feedHandler) cachedFeed(ctx context.Context, key string) (domain.Feed, bool) {
	spanContext, span := observability.StartDependencySpan(ctx, "redis", "get_feed")
	done := h.metrics.StartDependencyContext(spanContext, "redis", "get_feed")
	defer span.End()
	value, err := h.cache.Get(spanContext, key).Bytes()
	if errors.Is(err, redis.Nil) {
		done(telemetry.DependencySuccess)
		return domain.Feed{}, false
	}
	if err != nil {
		done(dependencyStatus(err))
		observability.RecordError(span, err)
		h.logger.WarnContext(ctx, "read feed cache", "error", err)
		return domain.Feed{}, false
	}
	var feed domain.Feed
	if err := json.Unmarshal(value, &feed); err != nil {
		done(telemetry.DependencyError)
		observability.RecordError(span, err)
		return domain.Feed{}, false
	}
	done(telemetry.DependencySuccess)
	return feed, true
}

func (h *feedHandler) cacheFeed(ctx context.Context, key string, feed domain.Feed) {
	encoded, err := json.Marshal(feed)
	if err != nil {
		return
	}
	spanContext, span := observability.StartDependencySpan(ctx, "redis", "set_feed")
	done := h.metrics.StartDependencyContext(spanContext, "redis", "set_feed")
	defer span.End()
	err = h.cache.Set(spanContext, key, encoded, 15*time.Second).Err()
	done(dependencyStatus(err))
	observability.RecordError(span, err)
}

func (h *feedHandler) posts(ctx context.Context) ([]domain.Post, error) {
	spanContext, span := observability.StartDependencySpan(ctx, "postgres", "read_feed")
	done := h.metrics.StartDependencyContext(spanContext, "postgres", "read_feed")
	defer span.End()
	rows, err := h.database.Query(spanContext, `SELECT id, author_id, body FROM posts ORDER BY created_at DESC LIMIT 20`)
	if err != nil {
		done(dependencyStatus(err))
		observability.RecordError(span, err)
		return nil, err
	}
	defer rows.Close()
	posts := make([]domain.Post, 0, 20)
	for rows.Next() {
		var post domain.Post
		if err := rows.Scan(&post.ID, &post.AuthorID, &post.Body); err != nil {
			done(telemetry.DependencyError)
			observability.RecordError(span, err)
			return nil, err
		}
		posts = append(posts, post)
	}
	err = rows.Err()
	done(dependencyStatus(err))
	observability.RecordError(span, err)
	return posts, err
}

func (h *feedHandler) profile(ctx context.Context, userID int64) (domain.Profile, error) {
	done := h.metrics.StartDependencyContext(ctx, "profile", "get")
	var profile domain.Profile
	err := getJSON(ctx, h.client, fmt.Sprintf("%s/v1/profiles/%d", h.profileURL, userID), &profile)
	done(dependencyStatus(err))
	return profile, err
}

func (h *feedHandler) recommendations(ctx context.Context, userID int64) (domain.Recommendations, error) {
	done := h.metrics.StartDependencyContext(ctx, "recommendation", "list")
	var recommendations domain.Recommendations
	err := getJSON(ctx, h.client, fmt.Sprintf("%s/v1/recommendations?user_id=%d", h.recommendationURL, userID), &recommendations)
	done(dependencyStatus(err))
	return recommendations, err
}

func getJSON(ctx context.Context, client *http.Client, url string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("downstream status %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(destination)
}

func readiness(database *pgxpool.Pool, cache *redis.Client) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), time.Second)
		defer cancel()
		if err := database.Ping(ctx); err != nil {
			http.Error(writer, "postgres unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := cache.Ping(ctx).Err(); err != nil {
			http.Error(writer, "redis unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}
}

func dependencyStatus(err error) telemetry.DependencyStatus {
	if err == nil {
		return telemetry.DependencySuccess
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return telemetry.DependencyTimeout
	}
	if errors.Is(err, context.Canceled) {
		return telemetry.DependencyCanceled
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
