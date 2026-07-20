package cryptox

import (
	"bytes"
	"errors"
	"testing"
)

func mustKey(t *testing.T) []byte {
	t.Helper()
	k, err := NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

func TestNewCipher_KeySizes(t *testing.T) {
	tests := []struct {
		name    string
		keyLen  int
		wantErr error
	}{
		{"aes256 valid", 32, nil},
		{"too short (aes128)", 16, ErrInvalidKeySize},
		{"empty", 0, ErrInvalidKeySize},
		{"too long", 40, ErrInvalidKeySize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCipher(make([]byte, tt.keyLen))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewCipher(len=%d) err = %v, want %v", tt.keyLen, err, tt.wantErr)
			}
		})
	}
}

func TestCipher_RoundTrip(t *testing.T) {
	c, err := NewCipher(mustKey(t))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	tests := []struct {
		name      string
		plaintext string
		aad       string
	}{
		{"simple", "hello world", "tenant-a"},
		{"empty aad", "card-4242-4242-4242", ""},
		{"unicode", "日本語テキスト", "tenant-b"},
		{"long", string(bytes.Repeat([]byte("x"), 4096)), "tenant-c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := c.Encrypt([]byte(tt.plaintext), []byte(tt.aad))
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			got, err := c.Decrypt(enc, []byte(tt.aad))
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if string(got) != tt.plaintext {
				t.Fatalf("roundtrip mismatch: got %q want %q", got, tt.plaintext)
			}
		})
	}
}

func TestCipher_NonceIsRandom(t *testing.T) {
	c, _ := NewCipher(mustKey(t))
	a, _ := c.Encrypt([]byte("same"), nil)
	b, _ := c.Encrypt([]byte("same"), nil)
	if a == b {
		t.Fatal("expected distinct ciphertext for identical plaintext (nonce reuse)")
	}
}

func TestCipher_Failures(t *testing.T) {
	c, _ := NewCipher(mustKey(t))
	enc, err := c.Encrypt([]byte("secret"), []byte("tenant-a"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	tampered := []byte(enc)
	tampered[len(tampered)-2] ^= 0x01

	tests := []struct {
		name    string
		decrypt func() error
	}{
		{"wrong aad", func() error { _, e := c.Decrypt(enc, []byte("tenant-b")); return e }},
		{"tampered ciphertext", func() error { _, e := c.Decrypt(string(tampered), []byte("tenant-a")); return e }},
		{"not base64", func() error { _, e := c.Decrypt("!!!not-base64!!!", nil); return e }},
		{"too short", func() error { _, e := c.Decrypt("YWI=", nil); return e }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.decrypt(); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestCipher_EmptyPlaintext(t *testing.T) {
	c, _ := NewCipher(mustKey(t))
	if _, err := c.Encrypt(nil, nil); !errors.Is(err, ErrEmptyPlaintext) {
		t.Fatalf("got %v want ErrEmptyPlaintext", err)
	}
}
