// Package upgrade fetches a signed Kayly release and puts its binary in place.
//
// A release publishes kayly-linux-<arch>, checksums.txt (sha256sum format) and
// checksums.txt.sig, an ed25519 signature of checksums.txt by the release key. A binary is
// trusted only when the signature verifies against the public key built into this binary and
// the binary's hash matches its line in checksums.txt. install.sh checks the same two things
// with openssl.
package upgrade

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// releaseKey is the public half of the key that signs releases. install.sh embeds the same
// block; a test keeps the two equal.
//
//go:embed release.pub
var releaseKey []byte

// Limits on what a download may weigh, so a broken mirror cannot fill the disk.
const (
	maxBinary = 256 << 20
	maxSmall  = 1 << 20
)

// Source is where releases are published.
type Source struct {
	// Download is the base of release assets: <Download>/<version>/<file>.
	Download string
	// Latest answers with the newest release as GitHub's API does: {"tag_name": "v1.2.3"}.
	Latest string
	Key    ed25519.PublicKey
	Client *http.Client
}

// GitHub returns the official source: Kayly's GitHub releases, checked with the release key.
func GitHub() (Source, error) {
	key, err := ParseKey(releaseKey)
	if err != nil {
		return Source{}, fmt.Errorf("built-in release key: %w", err)
	}
	return Source{
		Download: "https://github.com/kotlovyim-dev/kayly/releases/download",
		Latest:   "https://api.github.com/repos/kotlovyim-dev/kayly/releases/latest",
		Key:      key,
		Client:   http.DefaultClient,
	}, nil
}

// ParseKey reads an ed25519 public key in PEM (PKIX), as `openssl pkey -pubout` writes it.
func ParseKey(b []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("no PUBLIC KEY block")
	}
	k, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("release key is %T, want ed25519", k)
	}
	return pub, nil
}

// LatestVersion asks the source for the newest release's version.
func (s Source) LatestVersion(ctx context.Context) (string, error) {
	b, err := s.get(ctx, s.Latest, maxSmall)
	if err != nil {
		return "", err
	}
	var r struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", fmt.Errorf("read latest release: %w", err)
	}
	if _, err := parse(r.Tag); err != nil {
		return "", fmt.Errorf("latest release: %w", err)
	}
	return r.Tag, nil
}

// Fetch downloads version's binary for arch (amd64, arm64) and returns it once its signature
// and checksum hold.
func (s Source) Fetch(ctx context.Context, version, arch string) ([]byte, error) {
	base := s.Download + "/" + version + "/"
	sums, err := s.get(ctx, base+"checksums.txt", maxSmall)
	if err != nil {
		return nil, err
	}
	sig, err := s.get(ctx, base+"checksums.txt.sig", maxSmall)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(s.Key, sums, sig) {
		return nil, fmt.Errorf("%s: checksums.txt is not signed by the release key", version)
	}
	name := "kayly-linux-" + arch
	want, err := checksum(sums, name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", version, err)
	}
	bin, err := s.get(ctx, base+name, maxBinary)
	if err != nil {
		return nil, err
	}
	if got := sha256.Sum256(bin); hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("%s: %s does not match its checksum", version, name)
	}
	return bin, nil
}

// checksum finds name's hash in sha256sum output.
func checksum(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		sum, file, ok := strings.Cut(sc.Text(), "  ")
		if ok && strings.TrimPrefix(file, "*") == name {
			return strings.ToLower(sum), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", name)
}

func (s Source) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("download %s: larger than %d bytes", url, limit)
	}
	return b, nil
}

// Install puts bin at path in one rename, so path is always a whole binary. The binary it
// replaces is kept next to it as <path>.previous, the way back if the new one will not start.
func Install(bin []byte, path string) error {
	old, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the installed binary: %w", err)
	}
	if err := writeFile(path+".previous", old); err != nil {
		return fmt.Errorf("keep the installed binary: %w", err)
	}
	if err := writeFile(path, bin); err != nil {
		return fmt.Errorf("install the new binary: %w", err)
	}
	return nil
}

// writeFile writes b to a temporary file beside path, syncs it and renames it over path.
func writeFile(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".kayly-upgrade-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once renamed
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Compare orders two versions (v1.2.3, optionally with a -pre suffix, which sorts before the
// release): -1 if a is older than b, 0 if equal, 1 if newer.
func Compare(a, b string) (int, error) {
	va, err := parse(a)
	if err != nil {
		return 0, err
	}
	vb, err := parse(b)
	if err != nil {
		return 0, err
	}
	for i := range 3 {
		if va.n[i] != vb.n[i] {
			if va.n[i] < vb.n[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	switch {
	case va.pre == vb.pre:
		return 0, nil
	case va.pre == "":
		return 1, nil
	case vb.pre == "":
		return -1, nil
	case va.pre < vb.pre:
		return -1, nil
	}
	return 1, nil
}

// Valid reports whether v is a release version like v1.2.3 or v1.2.3-rc.1.
func Valid(v string) error {
	_, err := parse(v)
	return err
}

type version struct {
	n   [3]int
	pre string
}

func parse(v string) (version, error) {
	s, ok := strings.CutPrefix(v, "v")
	if !ok {
		return version{}, fmt.Errorf("%q is not a version like v1.2.3", v)
	}
	s, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return version{}, fmt.Errorf("%q is not a version like v1.2.3", v)
	}
	var out version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, fmt.Errorf("%q is not a version like v1.2.3", v)
		}
		out.n[i] = n
	}
	out.pre = pre
	return out, nil
}
