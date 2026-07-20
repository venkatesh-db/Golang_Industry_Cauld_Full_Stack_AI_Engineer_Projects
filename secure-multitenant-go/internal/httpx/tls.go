// Package httpx builds HTTP servers hardened to a TLS 1.2 floor with
// forward-secret AEAD cipher suites and production timeouts.
package httpx

import (
	"crypto/tls"
	"net/http"
	"time"
)

// SecureTLSConfig enforces TLS 1.2 as the minimum, allows 1.3, and restricts the
// negotiable 1.2 suites to ECDHE + AEAD (GCM / ChaCha20-Poly1305). TLS 1.3 suites
// are fixed by the Go runtime and always used when 1.3 is negotiated.
func SecureTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS13,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		},
		CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256},
	}
}

// NewServer builds an http.Server pre-wired with SecureTLSConfig and sane
// production timeouts to bound slow-loris and idle-connection exposure.
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		TLSConfig:         SecureTLSConfig(),
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
