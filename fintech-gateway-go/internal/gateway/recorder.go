package gateway

import (
	"bytes"
	"net/http"

	"fintechgateway/internal/pool"
)

// responseRecorder buffers a proxied response so the gateway can decide
// whether to retry against a different backend, or cache it, before
// anything is written to the real client connection. Its body buffer
// comes from pool.Get, matching the rest of the hot path's
// allocation-avoidance strategy — callers must call release when done.
type responseRecorder struct {
	header      http.Header
	status      int
	body        *bytes.Buffer
	wroteHeader bool
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{header: make(http.Header), status: http.StatusOK, body: pool.Get()}
}

func (r *responseRecorder) Header() http.Header { return r.header }

func (r *responseRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.body.Write(b)
}

// release returns the body buffer to the pool. After release, the
// recorder's body content must not be read — copy anything that needs
// to outlive this call (e.g. into a cache entry) first.
func (r *responseRecorder) release() {
	if r.body != nil {
		pool.Put(r.body)
		r.body = nil
	}
}
