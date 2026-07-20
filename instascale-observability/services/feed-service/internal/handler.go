package feed

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

type feedResponse struct {
	ViewerID int64  `json:"viewer_id"`
	CacheHit bool   `json:"cache_hit"`
	Posts    []Post `json:"posts"`
}

// GetFeed handles GET /feed/{userId}?limit=N.
func (h *Handler) GetFeed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := obs.LoggerFromContext(ctx)

	if d := h.chaos.SlowDepDelay(); d > 0 {
		time.Sleep(d)
	}
	if h.chaos.ShouldFail() {
		http.Error(w, "injected failure", http.StatusInternalServerError)
		return
	}

	viewerID, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil {
		http.Error(w, "bad user id", http.StatusBadRequest)
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	posts, hit, err := h.store.Feed(ctx, viewerID, limit)
	if err != nil {
		log.Error("feed query failed", "err", err, "viewer_id", viewerID)
		http.Error(w, "feed unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(feedResponse{ViewerID: viewerID, CacheHit: hit, Posts: posts})
}
