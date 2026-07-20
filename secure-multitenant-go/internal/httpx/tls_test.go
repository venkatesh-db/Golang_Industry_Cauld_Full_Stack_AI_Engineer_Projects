package httpx

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecureTLSConfig_VersionFloor(t *testing.T) {
	cfg := SecureTLSConfig()
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %#x want TLS1.2 (%#x)", cfg.MinVersion, tls.VersionTLS12)
	}

	tests := []struct {
		name    string
		version uint16
		wantOK  bool
	}{
		{"tls1.0 rejected", tls.VersionTLS10, false},
		{"tls1.1 rejected", tls.VersionTLS11, false},
		{"tls1.2 allowed", tls.VersionTLS12, true},
		{"tls1.3 allowed", tls.VersionTLS13, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok := tt.version >= cfg.MinVersion && tt.version <= cfg.MaxVersion
			if ok != tt.wantOK {
				t.Fatalf("version %#x allowed=%v want %v", tt.version, ok, tt.wantOK)
			}
		})
	}
}

// TestServer_EnforcesTLSFloor performs real handshakes to prove a <1.2 client is
// rejected while a 1.2 client succeeds.
func TestServer_EnforcesTLSFloor(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = SecureTLSConfig()
	srv.StartTLS()
	defer srv.Close()

	tests := []struct {
		name       string
		maxVersion uint16
		wantErr    bool
	}{
		{"legacy tls1.1 client fails", tls.VersionTLS11, true},
		{"modern tls1.2 client ok", tls.VersionTLS12, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, //nolint:gosec // test-only self-signed cert
					MinVersion:         tls.VersionTLS10,
					MaxVersion:         tt.maxVersion,
				},
			}}
			resp, err := client.Get(srv.URL)
			if tt.wantErr {
				if err == nil {
					resp.Body.Close()
					t.Fatal("expected handshake failure, got success")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			resp.Body.Close()
		})
	}
}
