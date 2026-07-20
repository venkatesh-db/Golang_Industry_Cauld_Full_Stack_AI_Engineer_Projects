package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMiddleware_ValidTokenPassesThrough(t *testing.T) {
	secret := []byte("test-secret-key")
	now := time.Now()
	v, _ := newVerifierWithClock(secret, func() time.Time { return now })
	token := signHS256(t, secret, "HS256", validClaims(now))

	var gotSubject string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := ClaimsFromContext(r.Context())
		if !ok {
			t.Fatal("expected claims in context")
		}
		gotSubject = claims.Subject
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	Middleware(v, next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotSubject != "user-123" {
		t.Fatalf("subject = %q, want user-123", gotSubject)
	}
}

func TestMiddleware_MissingHeaderRejected(t *testing.T) {
	v, _ := newVerifierWithClock([]byte("secret"), time.Now)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	Middleware(v, next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Fatal("next handler must not run without a valid token")
	}
}

func TestMiddleware_InvalidTokenRejected(t *testing.T) {
	v, _ := newVerifierWithClock([]byte("secret"), time.Now)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run with an invalid token")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer garbage-token")
	rec := httptest.NewRecorder()
	Middleware(v, next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
