package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"livescale/internal/concurrency"
	"livescale/internal/config"
	"livescale/internal/obs"
	"livescale/internal/session"
	"livescale/internal/token"
)

func newTestServer() (http.Handler, config.Config) {
	cfg := config.Default()
	cfg.ShardCount = 16
	m := obs.New()
	return NewServer(cfg, concurrency.New(cfg.ShardCount), session.NewMemory(cfg.ShardCount), m).Handler(), cfg
}

func tok(cfg config.Config, acct string, limit int) string {
	return token.Sign(cfg.HMACKey, token.Claims{
		AccountID: acct, DeviceLimit: limit, Exp: time.Now().Add(time.Hour).Unix(), AssetScope: "*",
	})
}

func do(t *testing.T, h http.Handler, method, path, tk string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, path, &buf)
	if tk != "" {
		r.Header.Set("X-Playback-Token", tk)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestLifecycle(t *testing.T) {
	h, cfg := newTestServer()
	tk := tok(cfg, "acc1", 2)

	// authorize
	w := do(t, h, "POST", "/v1/authorize", tk, map[string]string{"assetId": "movie", "deviceId": "phone"})
	if w.Code != 200 {
		t.Fatalf("authorize code=%d body=%s", w.Code, w.Body)
	}
	var ar authorizeResp
	_ = json.Unmarshal(w.Body.Bytes(), &ar)
	if ar.SessionID == "" {
		t.Fatal("no session id")
	}

	// heartbeat
	if w := do(t, h, "POST", "/v1/heartbeat", tk, map[string]string{"sessionId": ar.SessionID}); w.Code != 200 {
		t.Fatalf("heartbeat code=%d body=%s", w.Code, w.Body)
	}

	// stop
	if w := do(t, h, "POST", "/v1/stop", tk, map[string]string{"sessionId": ar.SessionID}); w.Code != 200 {
		t.Fatalf("stop code=%d", w.Code)
	}

	// heartbeat after stop -> expired/conflict
	if w := do(t, h, "POST", "/v1/heartbeat", tk, map[string]string{"sessionId": ar.SessionID}); w.Code != 409 {
		t.Fatalf("heartbeat after stop code=%d, want 409", w.Code)
	}
}

func TestDeviceLimitEnforced(t *testing.T) {
	h, cfg := newTestServer()
	tk := tok(cfg, "acc2", 1)
	if w := do(t, h, "POST", "/v1/authorize", tk, map[string]string{"deviceId": "d1"}); w.Code != 200 {
		t.Fatalf("first authorize code=%d", w.Code)
	}
	w := do(t, h, "POST", "/v1/authorize", tk, map[string]string{"deviceId": "d2"})
	if w.Code != 403 {
		t.Fatalf("over-limit authorize code=%d, want 403", w.Code)
	}
}

// TestAuthorizeIdempotentPerDevice is the H1 regression guard: re-authorizing
// the SAME device must return the SAME session, not mint a second one (which
// previously let stop() zero the device count and bypass the device limit).
func TestReauthorizeSameDevice(t *testing.T) {
	h, cfg := newTestServer()
	tk := tok(cfg, "accH1", 1)
	first := do(t, h, "POST", "/v1/authorize", tk, map[string]string{"deviceId": "d0"})
	second := do(t, h, "POST", "/v1/authorize", tk, map[string]string{"deviceId": "d0"})
	if first.Code != 200 || second.Code != 200 {
		t.Fatalf("codes: %d %d", first.Code, second.Code)
	}
	var a, b authorizeResp
	_ = json.Unmarshal(first.Body.Bytes(), &a)
	_ = json.Unmarshal(second.Body.Bytes(), &b)
	if a.SessionID != b.SessionID {
		t.Fatalf("re-authorize minted a new session (%s != %s) — H1 bypass", a.SessionID, b.SessionID)
	}
	// A genuinely new device must still be rejected at limit=1.
	if w := do(t, h, "POST", "/v1/authorize", tk, map[string]string{"deviceId": "d1"}); w.Code != 403 {
		t.Fatalf("new device over limit code=%d, want 403", w.Code)
	}
}

func TestUnauthorized(t *testing.T) {
	h, _ := newTestServer()
	if w := do(t, h, "POST", "/v1/authorize", "", map[string]string{"deviceId": "d"}); w.Code != 401 {
		t.Fatalf("no-token code=%d, want 401", w.Code)
	}
	if w := do(t, h, "POST", "/v1/authorize", "garbage.token.here", map[string]string{"deviceId": "d"}); w.Code != 401 {
		t.Fatalf("bad-token code=%d, want 401", w.Code)
	}
}
