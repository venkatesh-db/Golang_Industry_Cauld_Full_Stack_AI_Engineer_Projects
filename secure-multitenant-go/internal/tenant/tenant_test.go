package tenant

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFromContext(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		want    ID
		wantErr bool
	}{
		{"present", NewContext(context.Background(), "tenant-a"), "tenant-a", false},
		{"missing", context.Background(), "", true},
		{"empty id", NewContext(context.Background(), ""), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FromContext(tt.ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		wantStatus int
		wantTenant ID
	}{
		{"valid", "tenant-a", http.StatusOK, "tenant-a"},
		{"trimmed", "  tenant-b  ", http.StatusOK, "tenant-b"},
		{"missing", "", http.StatusBadRequest, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen ID
			h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen, _ = FromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set(HeaderName, tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d want %d", rec.Code, tt.wantStatus)
			}
			if seen != tt.wantTenant {
				t.Fatalf("tenant = %q want %q", seen, tt.wantTenant)
			}
		})
	}
}
