package waf

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func passthroughHandler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusOK)
	})
}

func TestMiddleware_AllowsNormalRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/payments?account=42&amount=100", nil)
	rec := httptest.NewRecorder()
	Middleware(DefaultConfig(), passthroughHandler(t)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestMiddleware_RejectsOverlongURL(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/x?q="+strings.Repeat("a", 3000), nil)
	rec := httptest.NewRecorder()
	Middleware(DefaultConfig(), passthroughHandler(t)).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestURITooLong {
		t.Fatalf("status = %d, want 414", rec.Code)
	}
}

func TestMiddleware_RejectsTooManyHeaders(t *testing.T) {
	cfg := Config{MaxBodyBytes: 1 << 20, MaxURLLength: 2048, MaxHeaders: 2}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-One", "a")
	req.Header.Set("X-Two", "b")
	req.Header.Set("X-Three", "c")
	rec := httptest.NewRecorder()
	Middleware(cfg, passthroughHandler(t)).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestMiddleware_RejectsPathTraversal(t *testing.T) {
	cases := []string{"/api/../../etc/passwd", "/api/%2e%2e/%2e%2e/etc/passwd"}
	for _, path := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		Middleware(DefaultConfig(), passthroughHandler(t)).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("path %q: status = %d, want 400", path, rec.Code)
		}
	}
}

func TestMiddleware_RejectsSQLInjectionShapedQuery(t *testing.T) {
	cases := []string{
		"/api/users?id=1%27%20OR%20%271%27=%271",
		"/api/search?q=1%20UNION%20SELECT%20password%20FROM%20users",
		"/api/x?id=1%3B%20DROP%20TABLE%20accounts",
	}
	for _, target := range cases {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		Middleware(DefaultConfig(), passthroughHandler(t)).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("target %q: status = %d, want 400", target, rec.Code)
		}
	}
}

func TestMiddleware_RejectsScriptTagInPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/search?q=%3Cscript%3Ealert(1)%3C/script%3E", nil)
	rec := httptest.NewRecorder()
	Middleware(DefaultConfig(), passthroughHandler(t)).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestMiddleware_EnforcesBodySizeCap proves the wrapped reader actually
// bounds the body: reading past the cap must error, not silently
// succeed and let an oversized payload through.
func TestMiddleware_EnforcesBodySizeCap(t *testing.T) {
	cfg := Config{MaxBodyBytes: 10, MaxURLLength: 2048, MaxHeaders: 100}
	body := strings.NewReader(strings.Repeat("x", 1000))
	req := httptest.NewRequest(http.MethodPost, "/api/x", body)

	var readErr error
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	Middleware(cfg, handler).ServeHTTP(rec, req)

	if readErr == nil {
		t.Fatal("expected reading an oversized body to error")
	}
}
