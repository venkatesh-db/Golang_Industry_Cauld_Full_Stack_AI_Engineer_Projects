package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// signHS256 is a minimal test-only token builder — production issuance
// lives outside this gateway (an identity/auth service), so the
// verifier package intentionally doesn't ship a signer.
func signHS256(t *testing.T, secret []byte, alg string, claims map[string]any) string {
	t.Helper()
	h, err := json.Marshal(map[string]string{"alg": alg, "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	headerB64 := base64.RawURLEncoding.EncodeToString(h)
	payloadB64 := base64.RawURLEncoding.EncodeToString(p)
	signingInput := headerB64 + "." + payloadB64

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	sigB64 := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + sigB64
}

func validClaims(now time.Time) map[string]any {
	return map[string]any{
		"sub": "user-123",
		"iat": float64(now.Unix()),
		"exp": float64(now.Add(time.Hour).Unix()),
	}
}

func TestVerify_ValidToken(t *testing.T) {
	secret := []byte("test-secret-key")
	now := time.Now()
	v, err := newVerifierWithClock(secret, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	token := signHS256(t, secret, "HS256", validClaims(now))
	claims, err := v.Verify(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claims.Subject != "user-123" {
		t.Fatalf("subject = %q, want user-123", claims.Subject)
	}
}

func TestVerify_WrongSecretRejected(t *testing.T) {
	now := time.Now()
	v, _ := newVerifierWithClock([]byte("correct-secret"), func() time.Time { return now })
	token := signHS256(t, []byte("wrong-secret"), "HS256", validClaims(now))

	if _, err := v.Verify(token); err != ErrBadSignature {
		t.Fatalf("got %v, want ErrBadSignature", err)
	}
}

func TestVerify_ExpiredTokenRejected(t *testing.T) {
	now := time.Now()
	secret := []byte("test-secret-key")
	v, _ := newVerifierWithClock(secret, func() time.Time { return now })

	claims := map[string]any{"sub": "user-123", "exp": float64(now.Add(-time.Minute).Unix())}
	token := signHS256(t, secret, "HS256", claims)

	if _, err := v.Verify(token); err != ErrExpired {
		t.Fatalf("got %v, want ErrExpired", err)
	}
}

func TestVerify_MissingExpiryRejected(t *testing.T) {
	now := time.Now()
	secret := []byte("test-secret-key")
	v, _ := newVerifierWithClock(secret, func() time.Time { return now })

	claims := map[string]any{"sub": "user-123"} // no exp
	token := signHS256(t, secret, "HS256", claims)

	if _, err := v.Verify(token); err != ErrMissingExpiry {
		t.Fatalf("got %v, want ErrMissingExpiry (tokens must always expire)", err)
	}
}

func TestVerify_TamperedPayloadRejected(t *testing.T) {
	now := time.Now()
	secret := []byte("test-secret-key")
	v, _ := newVerifierWithClock(secret, func() time.Time { return now })

	token := signHS256(t, secret, "HS256", validClaims(now))
	parts := splitToken(t, token)

	// Attacker swaps the subject claim but keeps the original signature.
	tamperedPayload, _ := json.Marshal(map[string]any{"sub": "attacker", "exp": float64(now.Add(time.Hour).Unix())})
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(tamperedPayload) + "." + parts[2]

	if _, err := v.Verify(tampered); err != ErrBadSignature {
		t.Fatalf("got %v, want ErrBadSignature", err)
	}
}

// TestVerify_AlgNoneRejected is the regression test for the classic JWT
// algorithm-confusion vulnerability: a token that declares alg "none"
// (sometimes with an empty signature segment) must never be accepted
// just because it's syntactically well-formed.
func TestVerify_AlgNoneRejected(t *testing.T) {
	now := time.Now()
	v, _ := newVerifierWithClock([]byte("test-secret-key"), func() time.Time { return now })

	h, _ := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	p, _ := json.Marshal(validClaims(now))
	token := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p) + "."

	if _, err := v.Verify(token); err != ErrUnsupportedAlg {
		t.Fatalf("got %v, want ErrUnsupportedAlg", err)
	}
}

func TestVerify_UnsupportedAlgRejected(t *testing.T) {
	now := time.Now()
	secret := []byte("test-secret-key")
	v, _ := newVerifierWithClock(secret, func() time.Time { return now })

	token := signHS256(t, secret, "HS512", validClaims(now))
	if _, err := v.Verify(token); err != ErrUnsupportedAlg {
		t.Fatalf("got %v, want ErrUnsupportedAlg", err)
	}
}

func TestVerify_MalformedTokenRejected(t *testing.T) {
	v, _ := newVerifierWithClock([]byte("secret"), time.Now)
	cases := []string{"", "not-a-jwt", "a.b", "a.b.c.d", "!!!.b.c"}
	for _, tok := range cases {
		if _, err := v.Verify(tok); err == nil {
			t.Errorf("token %q should have been rejected", tok)
		}
	}
}

func TestNewVerifier_RejectsEmptySecret(t *testing.T) {
	if _, err := NewVerifier(nil); err != ErrEmptySecret {
		t.Fatalf("got %v, want ErrEmptySecret", err)
	}
}

func splitToken(t *testing.T, token string) []string {
	t.Helper()
	parts := make([]string, 0, 3)
	start := 0
	for i, c := range token {
		if c == '.' {
			parts = append(parts, token[start:i])
			start = i + 1
		}
	}
	parts = append(parts, token[start:])
	if len(parts) != 3 {
		t.Fatalf("test helper: expected 3 token parts, got %d", len(parts))
	}
	return parts
}
