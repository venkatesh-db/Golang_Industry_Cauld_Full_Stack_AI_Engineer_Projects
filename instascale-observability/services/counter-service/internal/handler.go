package counter

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"instascale/internal/chaos"
	"instascale/internal/obs"
)

type Handler struct {
	store *Store
	chaos *chaos.Registry
}

func NewHandler(store *Store, ch *chaos.Registry) *Handler {
	return &Handler{store: store, chaos: ch}
}

type countsResponse struct {
	UserID    int64 `json:"user_id"`
	Likes     int64 `json:"likes"`
	Followers int64 `json:"followers"`
}

// GetCounts handles GET /counts/{userId}.
func (h *Handler) GetCounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := obs.LoggerFromContext(ctx)

	// Chaos hooks — no-ops when idle.
	if d := h.chaos.SlowDepDelay(); d > 0 {
		time.Sleep(d)
	}
	if h.chaos.ShouldFail() {
		log.Warn("retry-storm chaos: forcing 500")
		http.Error(w, "injected failure", http.StatusInternalServerError)
		return
	}

	userID, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil {
		http.Error(w, "bad user id", http.StatusBadRequest)
		return
	}

	likes, followers, err := h.store.Counts(ctx, userID)
	if err != nil {
		log.Error("counts query failed", "err", err, "user_id", userID)
		http.Error(w, "counts unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, countsResponse{UserID: userID, Likes: likes, Followers: followers})
}

// PostLike handles POST /counts/{userId}/like.
func (h *Handler) PostLike(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := obs.LoggerFromContext(ctx)

	if h.chaos.ShouldFail() {
		http.Error(w, "injected failure", http.StatusInternalServerError)
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil {
		http.Error(w, "bad user id", http.StatusBadRequest)
		return
	}
	n, err := h.store.Like(ctx, userID)
	if err != nil {
		log.Error("like failed", "err", err, "user_id", userID)
		http.Error(w, "like failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, countsResponse{UserID: userID, Likes: n})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
