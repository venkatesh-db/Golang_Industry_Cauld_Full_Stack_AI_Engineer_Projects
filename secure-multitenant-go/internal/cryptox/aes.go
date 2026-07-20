// Package cryptox provides authenticated AES-256-GCM encryption for data at
// rest. Ciphertext is bound to associated data (e.g. a tenant_id) so a blob
// encrypted for one tenant cannot be silently decrypted in another's context.
package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

var (
	// ErrInvalidKeySize is returned when a key is not exactly 32 bytes.
	ErrInvalidKeySize = errors.New("cryptox: key must be 32 bytes for AES-256")
	// ErrCiphertextShort is returned when input is smaller than the nonce.
	ErrCiphertextShort = errors.New("cryptox: ciphertext too short")
	// ErrEmptyPlaintext is returned when there is nothing to encrypt.
	ErrEmptyPlaintext = errors.New("cryptox: plaintext must not be empty")
)

// Cipher seals and opens payloads with a fixed AES-256-GCM key.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a Cipher from a 32-byte AES-256 key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cryptox: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cryptox: new gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals plaintext with optional associated data (e.g. tenant_id) and
// returns base64(nonce || ciphertext || tag). A fresh random nonce is used per
// call, so encrypting the same plaintext twice yields different ciphertext.
func (c *Cipher) Encrypt(plaintext, aad []byte) (string, error) {
	if len(plaintext) == 0 {
		return "", ErrEmptyPlaintext
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("cryptox: nonce: %w", err)
	}
	// Seal appends the ciphertext+tag to nonce, giving us nonce||sealed.
	sealed := c.aead.Seal(nonce, nonce, plaintext, aad)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. It fails if the ciphertext was tampered with or the
// associated data does not match what was used at seal time.
func (c *Cipher) Decrypt(encoded string, aad []byte) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("cryptox: decode: %w", err)
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return nil, ErrCiphertextShort
	}
	nonce, ciphertext := raw[:ns], raw[ns:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("cryptox: open: %w", err)
	}
	return plaintext, nil
}

// NewKey generates a cryptographically random 32-byte AES-256 key.
func NewKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("cryptox: gen key: %w", err)
	}
	return key, nil
}
