package ride

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func RunHTTP(ctx context.Context, pool *pgxpool.Pool, address string) error {
	service := NewService(pool)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "ride-api"})
	})
	mux.HandleFunc("POST /api/rides", func(w http.ResponseWriter, r *http.Request) {
		var command CreateCommand
		if err := decodeJSON(w, r, &command); err != nil {
			return
		}
		item, err := service.Create(r.Context(), command)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, item)
	})
	mux.HandleFunc("GET /api/rides/{id}", func(w http.ResponseWriter, r *http.Request) {
		item, err := service.Get(r.Context(), r.PathValue("id"))
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "ride not found")
			return
		}
		if err != nil {
			slog.Error("read ride", "error", err)
			writeError(w, http.StatusInternalServerError, "unable to load ride")
			return
		}
		writeJSON(w, http.StatusOK, item)
	})
	mux.HandleFunc("GET /api/rides/{id}/activity", func(w http.ResponseWriter, r *http.Request) {
		items, err := service.Activity(r.Context(), r.PathValue("id"))
		if err != nil {
			slog.Error("read ride activity", "error", err)
			writeError(w, http.StatusInternalServerError, "unable to load ride activity")
			return
		}
		writeJSON(w, http.StatusOK, items)
	})

	server := &http.Server{Addr: address, Handler: withLogging(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("ride API listening", "address", address)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if !strings.HasPrefix(r.Header.Get("content-type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return errors.New("unsupported media type")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started).String())
	})
}
