package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"expvar"
	"net/http"
	"time"

	"livescale/internal/concurrency"
	"livescale/internal/config"
	"livescale/internal/obs"
	"livescale/internal/session"
	"livescale/internal/token"
)

// Server wires the control-plane dependencies into an http.Handler.
type Server struct {
	cfg     config.Config
	mgr     *concurrency.Manager
	store   *session.Memory
	adm     *Admission
	metrics *obs.Metrics
}

func NewServer(cfg config.Config, mgr *concurrency.Manager, store *session.Memory, m *obs.Metrics) *Server {
	return &Server{
		cfg:     cfg,
		mgr:     mgr,
		store:   store,
		adm:     NewAdmission(cfg.SoftMaxInflight, cfg.HardMaxInflight, m),
		metrics: m,
	}
}

// Handler returns the fully-wired mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/authorize", s.adm.guard(high, s.adm.authed(s.cfg.HMACKey, s.authorize)))
	mux.HandleFunc("POST /v1/heartbeat", s.adm.guard(high, s.adm.authed(s.cfg.HMACKey, s.heartbeat)))
	mux.HandleFunc("POST /v1/stop", s.adm.guard(high, s.adm.authed(s.cfg.HMACKey, s.stop)))
	mux.HandleFunc("GET /v1/stats/concurrency", s.adm.guard(low, s.stats))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("GET /metrics", expvar.Handler())
	return mux
}

type authorizeReq struct {
	AssetID  string `json:"assetId"`
	DeviceID string `json:"deviceId"`
}
type authorizeResp struct {
	SessionID    string `json:"sessionId"`
	ExpiresInSec int    `json:"expiresInSec"`
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request, c token.Claims) {
	var req authorizeReq
	if !decode(w, r, &req) || req.DeviceID == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST")
		return
	}
	now := time.Now()
	exp := now.Add(s.cfg.SessionTTL).UnixNano()
	candidate := newID()
	sid, ok := s.mgr.Admit(c.AccountID, req.DeviceID, c.DeviceLimit, now.UnixNano(), exp, candidate)
	if !ok {
		s.metrics.Denied.Add(1)
		writeErr(w, http.StatusForbidden, "DEVICE_LIMIT")
		return
	}
	sess := session.Session{AccountID: c.AccountID, DeviceID: req.DeviceID, AssetID: req.AssetID, ExpiresAt: exp}
	if sid == candidate {
		s.store.Put(sid, sess) // genuinely new device+session
		s.metrics.Authorized.Add(1)
	} else {
		s.store.Extend(sid, exp) // re-authorize of existing device: reuse its session
	}
	writeJSON(w, http.StatusOK, authorizeResp{SessionID: sid, ExpiresInSec: int(s.cfg.SessionTTL.Seconds())})
}

type sessionReq struct {
	SessionID string `json:"sessionId"`
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request, c token.Claims) {
	var req sessionReq
	if !decode(w, r, &req) || req.SessionID == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST")
		return
	}
	now := time.Now()
	sess, ok := s.store.Get(req.SessionID, now.UnixNano())
	if !ok || sess.AccountID != c.AccountID {
		writeErr(w, http.StatusConflict, "SESSION_EXPIRED")
		return
	}
	exp := now.Add(s.cfg.SessionTTL).UnixNano()
	s.store.Extend(req.SessionID, exp)
	s.mgr.Refresh(c.AccountID, sess.DeviceID, exp)
	s.metrics.Heartbeats.Add(1)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "expiresInSec": int(s.cfg.SessionTTL.Seconds())})
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request, c token.Claims) {
	var req sessionReq
	if !decode(w, r, &req) || req.SessionID == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST")
		return
	}
	if sess, ok := s.store.Delete(req.SessionID); ok && sess.AccountID == c.AccountID {
		s.mgr.Release(c.AccountID, sess.DeviceID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true}) // idempotent
}

func (s *Server) stats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"globalEstimate":    int64(s.mgr.GlobalEstimate()),
		"shedTotal":         s.metrics.Shed.Value(),
		"authorizedTotal":   s.metrics.Authorized.Value(),
		"meanLatencyMicros": s.metrics.MeanLatency().Microseconds(),
	})
}

// --- helpers ---

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(v); err != nil {
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
