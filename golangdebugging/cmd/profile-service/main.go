package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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
		Name:         "profile-service",
		Routes:       []string{"/healthz", "/readyz", "/v1/profiles/{id}"},
		Dependencies: map[string][]string{"postgres": {"get_profile"}},
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
	handler := &profileHandler{database: database, metrics: runtime.Metrics}
	lab := failurelab.New(failurelab.Config{
		Enabled:      service.EnvBool("FAILURE_LAB_ENABLED", false),
		Token:        service.Env("FAILURE_LAB_TOKEN", "local-failure-lab-token"),
		Logger:       runtime.Logger,
		Database:     database,
		MaxDBHolders: service.EnvInt("FAILURE_LAB_MAX_DB_HOLDERS", 16),
	})

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", runtime.Wrap("/healthz", http.HandlerFunc(noContent)))
	mux.Handle("GET /readyz", runtime.Wrap("/readyz", postgresReadiness(database)))
	mux.Handle("GET /v1/profiles/{id}", runtime.Wrap("/v1/profiles/{id}", http.HandlerFunc(handler.profile)))
	runtime.MountOperations(mux, lab)
	if err := runtime.ListenAndServe(service.Env("LISTEN_ADDR", ":8082"), mux); err != nil {
		runtime.Logger.Error("profile service stopped", "error", err)
	}
}

type profileHandler struct {
	database *pgxpool.Pool
	metrics  *telemetry.Metrics
}

func (h *profileHandler) profile(writer http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(writer, "invalid profile id", http.StatusBadRequest)
		return
	}
	ctx, span := observability.StartDependencySpan(request.Context(), "postgres", "get_profile")
	done := h.metrics.StartDependencyContext(ctx, "postgres", "get_profile")
	defer span.End()
	var profile domain.Profile
	err = h.database.QueryRow(ctx, `SELECT id, username, bio FROM profiles WHERE id = $1`, id).Scan(&profile.ID, &profile.Username, &profile.Bio)
	done(dependencyStatus(err))
	observability.RecordError(span, err)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(writer, "profile not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(writer, "profile unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(writer, http.StatusOK, profile)
}

func postgresReadiness(database *pgxpool.Pool) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), time.Second)
		defer cancel()
		if err := database.Ping(ctx); err != nil {
			http.Error(writer, "postgres unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}
}

func dependencyStatus(err error) telemetry.DependencyStatus {
	if err == nil || errors.Is(err, pgx.ErrNoRows) {
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
