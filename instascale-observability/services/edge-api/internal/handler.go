// Package edge implements edge-api: the BFF/gateway that composes a feed by
// fanning out to feed-service and counter-service, with per-call deadlines,
// bounded retries, and a circuit breaker (ADR-004). Trace context propagates
// across both hops so one trace spans all three services.
package edge

import (
	"encoding/json"
	"net/http"
	"strconv"

	"instascale/internal/chaos"
	"instascale/internal/obs"
)

type Handler struct {
	feedClient    *obs.Client
	counterClient *obs.Client
	feedURL       string
	counterURL    string
	chaos         *chaos.Registry
}

func NewHandler(feedClient, counterClient *obs.Client, feedURL, counterURL string, ch *chaos.Registry) *Handler {
	return &Handler{
		feedClient:    feedClient,
		counterClient: counterClient,
		feedURL:       feedURL,
		counterURL:    counterURL,
		chaos:         ch,
	}
}

type composedFeed struct {
	ViewerID int64           `json:"viewer_id"`
	Counts   json.RawMessage `json:"counts,omitempty"`
	Feed     json.RawMessage `json:"feed,omitempty"`
	Degraded []string        `json:"degraded,omitempty"` // which downstreams failed
}

// GetFeed handles GET /feed/{userId}: concurrent fan-out + graceful degradation.
func (h *Handler) GetFeed(w http.ResponseWriter, r *http.Request) {
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
	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "20"
	}

	type result struct {
		body []byte
		err  error
	}
	feedCh := make(chan result, 1)
	countCh := make(chan result, 1)

	go func() {
		b, err := h.feedClient.Get(ctx, h.feedURL+"/feed/"+itoa(userID)+"?limit="+limit)
		feedCh <- result{b, err}
	}()
	go func() {
		b, err := h.counterClient.Get(ctx, h.counterURL+"/counts/"+itoa(userID))
		countCh <- result{b, err}
	}()

	resp := composedFeed{ViewerID: userID}
	feedRes := <-feedCh
	countRes := <-countCh

	// Graceful degradation: a downstream failure degrades the response, it does
	// not fail the whole request — the interview talking point for partial failure.
	if feedRes.err != nil {
		log.Warn("feed downstream failed", "err", feedRes.err)
		resp.Degraded = append(resp.Degraded, "feed")
	} else {
		resp.Feed = json.RawMessage(feedRes.body)
	}
	if countRes.err != nil {
		log.Warn("counts downstream failed", "err", countRes.err)
		resp.Degraded = append(resp.Degraded, "counts")
	} else {
		resp.Counts = json.RawMessage(countRes.body)
	}

	// If everything failed, surface 503 so the SLO/alert story is honest.
	status := http.StatusOK
	if len(resp.Degraded) == 2 {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
