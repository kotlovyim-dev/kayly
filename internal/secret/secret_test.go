package secret

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestBoxRoundTrip(t *testing.T) {
	key, err := LoadOrCreateKey(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	box, err := NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	a, b := box.Seal([]byte("hunter2")), box.Seal([]byte("hunter2"))
	if bytes.Equal(a, b) {
		t.Error("two seals of one value are equal: nonce reused")
	}
	if pt, err := box.Open(a); err != nil || string(pt) != "hunter2" {
		t.Fatalf("Open = %q, %v", pt, err)
	}
	a[len(a)-1] ^= 1
	if _, err := box.Open(a); err == nil {
		t.Error("tampered value opened")
	}
	other, _ := NewBox(bytes.Repeat([]byte{1}, 32))
	if _, err := other.Open(b); err == nil {
		t.Error("opened with the wrong key")
	}
}

func TestLoadOrCreateKeyKeepsKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	k1, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadOrCreateKey(path)
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatalf("key changed between loads: %v", err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestGenerate(t *testing.T) {
	h, err := Generate("hex:32")
	if b, _ := hex.DecodeString(h); err != nil || len(b) != 32 {
		t.Errorf("hex:32 = %q, %v", h, err)
	}
	s, err := Generate("base64:16")
	if b, _ := base64.StdEncoding.DecodeString(s); err != nil || len(b) != 16 {
		t.Errorf("base64:16 = %q, %v", s, err)
	}
	for _, bad := range []string{"hex", "hex:0", "rot13:8", "hex:x", "hex:5000"} {
		if _, err := Generate(bad); err == nil {
			t.Errorf("Generate(%q) succeeded", bad)
		}
	}
}
