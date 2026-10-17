// Package secret encrypts values at rest and generates random secrets.
//
// The key lives in its own file, outside the database, so a copied database alone
// reveals nothing (ARCHITECTURE.md §4).
package secret

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

// Box seals and opens values with XChaCha20-Poly1305.
type Box struct {
	key []byte
}

// NewBox returns a Box for a 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf("secret: key is %d bytes, want %d", len(key), chacha20poly1305.KeySize)
	}
	return &Box{key: key}, nil
}

// LoadOrCreateKey reads the key file at path, creating it with a fresh key (mode 0600)
// if it does not exist.
func LoadOrCreateKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("secret: read key: %w", err)
	}
	key = make([]byte, chacha20poly1305.KeySize)
	rand.Read(key)
	// O_EXCL: two processes starting at once must not overwrite each other's key.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("secret: create key: %w", err)
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		return nil, fmt.Errorf("secret: write key: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("secret: write key: %w", err)
	}
	return key, nil
}

// Seal encrypts plaintext. The output is nonce || ciphertext.
func (b *Box) Seal(plaintext []byte) []byte {
	aead, _ := chacha20poly1305.NewX(b.key) // the key size was checked in NewBox
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(plaintext)+aead.Overhead())
	rand.Read(nonce)
	return aead.Seal(nonce, nonce, plaintext, nil)
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(sealed []byte) ([]byte, error) {
	aead, _ := chacha20poly1305.NewX(b.key)
	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("secret: sealed value is too short")
	}
	nonce, ct := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	pt, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, errors.New("secret: cannot decrypt (wrong key or corrupted value)")
	}
	return pt, nil
}

// Generate returns a random value for a spec "hex:N" or "base64:N" (N random bytes).
func Generate(spec string) (string, error) {
	kind, n, ok := strings.Cut(spec, ":")
	size, err := strconv.Atoi(n)
	if !ok || err != nil || size < 1 || size > 1024 {
		return "", fmt.Errorf("secret: bad generate spec %q", spec)
	}
	buf := make([]byte, size)
	rand.Read(buf)
	switch kind {
	case "hex":
		return hex.EncodeToString(buf), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(buf), nil
	}
	return "", fmt.Errorf("secret: bad generate spec %q", spec)
}
