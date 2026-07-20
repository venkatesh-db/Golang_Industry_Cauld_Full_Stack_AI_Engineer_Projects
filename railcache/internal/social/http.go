package social

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

//go:embed templates/index.html
var templates embed.FS

type HTTPServer struct {
	store *Store
	log   *slog.Logger
	tmpl  *template.Template
}

func NewHTTPServer(store *Store, log *slog.Logger) (*HTTPServer, error) {
	tmpl, err := template.ParseFS(templates, "templates/index.html")
	if err != nil {
		return nil, fmt.Errorf("parse social template: %w", err)
	}
	return &HTTPServer{store: store, log: log, tmpl: tmpl}, nil
}

func (s *HTTPServer) Router() http.Handler {
	router := chi.NewRouter()
	router.Use(s.recover, s.requestLog, requestTimeout(5*time.Second))
	router.Get("/", s.handleHome)
	router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
	})
	router.Get("/readyz", s.handleReady)
	router.Route("/api", func(api chi.Router) {
		api.Get("/posts", s.handlePosts)
		api.Post("/posts/{postID}/likes", s.handleLike)
		api.Get("/notifications", s.handleNotifications)
		api.Get("/pipeline", s.handlePipeline)
	})
	return router
}

func (s *HTTPServer) handleHome(w http.ResponseWriter, r *http.Request) {
	var body bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&body, "index.html", map[string]any{}); err != nil {
		s.log.Error("render home", "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = body.WriteTo(w)
}

func (s *HTTPServer) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "postgres": false})
		return
	}
	// Kafka readiness is intentionally not part of this endpoint: accepted likes
	// remain safely stored in the outbox while the broker is being maintained.
	writeJSON(w, http.StatusOK, map[string]any{"ready": true, "postgres": true, "delivery": "outbox-buffered"})
}

func (s *HTTPServer) handlePosts(w http.ResponseWriter, r *http.Request) {
	posts, err := s.store.ListPosts(r.Context())
	if err != nil {
		s.internalError(w, r, "list posts", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": posts})
}

func (s *HTTPServer) handleLike(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ActorID string `json:"actor_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	body.ActorID = strings.TrimSpace(body.ActorID)
	if body.ActorID == "" || len(body.ActorID) > 64 {
		http.Error(w, "actor_id is required", http.StatusBadRequest)
		return
	}
	postID := chi.URLParam(r, "postID")
	if postID == "" || len(postID) > 64 {
		http.Error(w, "invalid post id", http.StatusBadRequest)
		return
	}
	created, err := s.store.RecordLike(r.Context(), postID, body.ActorID, requestID(r))
	if err != nil {
		if errors.Is(err, ErrPostNotFound) {
			http.Error(w, "post not found", http.StatusNotFound)
			return
		}
		s.internalError(w, r, "record like", err)
		return
	}
	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"accepted":   created,
		"delivery":   "durably queued in postgres outbox",
		"request_id": requestID(r),
	})
}

func (s *HTTPServer) handleNotifications(w http.ResponseWriter, r *http.Request) {
	recipient := strings.TrimSpace(r.URL.Query().Get("recipient"))
	if recipient == "" {
		recipient = "maya"
	}
	items, err := s.store.ListNotifications(r.Context(), recipient)
	if err != nil {
		s.internalError(w, r, "list notifications", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": items})
}

func (s *HTTPServer) handlePipeline(w http.ResponseWriter, r *http.Request) {
	stats, events, err := s.store.Pipeline(r.Context())
	if err != nil {
		s.internalError(w, r, "read pipeline", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": stats, "events": events})
}

func (s *HTTPServer) internalError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	s.log.Error(operation, "err", err, "request_id", requestID(r))
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

type contextKey string

const requestIDKey contextKey = "request-id"

func requestID(r *http.Request) string {
	if value, ok := r.Context().Value(requestIDKey).(string); ok {
		return value
	}
	return "unknown"
}

func (s *HTTPServer) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 96 {
			id = newID()
		}
		start := time.Now()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
		s.log.Info("http request", "request_id", id, "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})
}

func (s *HTTPServer) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.log.Error("panic recovered", "panic", recovered, "request_id", requestID(r))
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestTimeout(duration time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, duration, `{"error":"request timed out"}`)
	}
}
