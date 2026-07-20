package token

import (
	"testing"
	"time"
)

func TestVerifyRoundTrip(t *testing.T) {
	key := []byte("k")
	now := time.Unix(1000, 0)
	tok := Sign(key, Claims{AccountID: "acc1", DeviceLimit: 3, Exp: 2000, AssetScope: "*"})
	c, err := Verify(key, tok, now)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.AccountID != "acc1" || c.DeviceLimit != 3 {
		t.Fatalf("bad claims: %+v", c)
	}
}

func TestVerifyExpired(t *testing.T) {
	key := []byte("k")
	tok := Sign(key, Claims{AccountID: "a", DeviceLimit: 1, Exp: 500, AssetScope: "*"})
	if _, err := Verify(key, tok, time.Unix(600, 0)); err != ErrExpired {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestVerifyTampered(t *testing.T) {
	key := []byte("k")
	tok := Sign(key, Claims{AccountID: "a", DeviceLimit: 5, Exp: 9999, AssetScope: "*"})
	// Flip the device limit in the payload without re-signing.
	bad := "a.9.9999.*" + tok[len("a.5.9999.*"):]
	if _, err := Verify(key, bad, time.Unix(1, 0)); err != ErrBadSig {
		t.Fatalf("want ErrBadSig, got %v", err)
	}
}

func TestVerifyWrongKey(t *testing.T) {
	tok := Sign([]byte("k1"), Claims{AccountID: "a", DeviceLimit: 1, Exp: 9999, AssetScope: "*"})
	if _, err := Verify([]byte("k2"), tok, time.Unix(1, 0)); err != ErrBadSig {
		t.Fatalf("want ErrBadSig, got %v", err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	for _, s := range []string{"", "nodots", "a.b", "a.b.c.d.e.f"} {
		if _, err := Verify([]byte("k"), s, time.Unix(1, 0)); err == nil {
			t.Fatalf("want error for %q", s)
		}
	}
}
