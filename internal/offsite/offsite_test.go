package offsite

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{9}, KeySize)
	for _, n := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 3*chunkSize + 5} {
		plain := make([]byte, n)
		rand.Read(plain)
		var enc, dec bytes.Buffer
		if err := Encrypt(&enc, bytes.NewReader(plain), key); err != nil {
			t.Fatal(err)
		}
		if int64(enc.Len()) != EncryptedSize(int64(n)) {
			t.Errorf("n=%d: size %d, EncryptedSize %d", n, enc.Len(), EncryptedSize(int64(n)))
		}
		if err := Decrypt(&dec, bytes.NewReader(enc.Bytes()), key); err != nil || !bytes.Equal(dec.Bytes(), plain) {
			t.Errorf("n=%d: round trip failed: %v", n, err)
		}
	}
}

func TestDecryptRejectsTampering(t *testing.T) {
	key := bytes.Repeat([]byte{9}, KeySize)
	plain := make([]byte, 2*chunkSize+10)
	var enc bytes.Buffer
	Encrypt(&enc, bytes.NewReader(plain), key)
	good := enc.Bytes()
	chunk := chunkSize + overhead
	flipped := bytes.Clone(good)
	flipped[len(magic)+5] ^= 1
	swapped := append(bytes.Clone(good[:len(magic)]), good[len(magic)+chunk:len(magic)+2*chunk]...)
	swapped = append(swapped, good[len(magic):len(magic)+chunk]...)
	swapped = append(swapped, good[len(magic)+2*chunk:]...)
	for name, data := range map[string][]byte{
		"flipped bit":    flipped,
		"chunks swapped": swapped,
		"cut at a chunk": good[:len(magic)+chunk],
		"cut mid-chunk":  good[:len(good)-3],
		"no header":      good[len(magic):],
	} {
		if err := Decrypt(&bytes.Buffer{}, bytes.NewReader(data), key); err == nil {
			t.Errorf("%s: decrypted", name)
		}
	}
	if err := Decrypt(&bytes.Buffer{}, bytes.NewReader(good), bytes.Repeat([]byte{8}, KeySize)); err == nil {
		t.Error("wrong key decrypted")
	}
}

func TestExpired(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	var objs []Object
	add := func(project, kind string, age time.Duration) {
		ts := now.Add(-age)
		name := fmt.Sprintf("%s/%s-daily.%s", project, ts.Format("20060102T150405Z"), kind)
		objs = append(objs, Object{Key: name + ".enc", Name: name, Time: ts})
	}
	for d := range 50 { // a daily backup for 50 days, two kinds
		add("app", "dump", time.Duration(d)*day)
		add("app", "rdb", time.Duration(d)*day)
	}
	kept := map[string]int{}
	gone := map[string]bool{}
	for _, o := range Expired(objs, now) {
		gone[o.Key] = true
	}
	for _, o := range objs {
		if !gone[o.Key] {
			kept[o.kind()]++
		}
	}
	// 7 recent days + one per week for 4 weeks.
	if kept["app/dump"] != 11 || kept["app/rdb"] != 11 {
		t.Fatalf("kept = %v, want 11 of each kind", kept)
	}
	for _, o := range objs {
		age := now.Sub(o.Time)
		if age < 7*day && gone[o.Key] {
			t.Errorf("%s: recent backup expired", o.Key)
		}
		if age >= 35*day && !gone[o.Key] {
			t.Errorf("%s: old backup kept", o.Key)
		}
	}
}

type memBucket struct {
	mu       sync.Mutex
	objs     map[string][]byte
	failPuts bool
}

func (b *memBucket) Put(_ context.Context, key string, r io.Reader, size int64) error {
	if b.failPuts {
		return errors.New("bucket unreachable")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return fmt.Errorf("declared %d bytes, got %d", size, len(data))
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objs[key] = data
	return nil
}

func (b *memBucket) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return io.NopCloser(bytes.NewReader(b.objs[key])), nil
}

func (b *memBucket) Delete(_ context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objs, key)
	return nil
}

func (b *memBucket) List(context.Context) ([]ObjectInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []ObjectInfo
	for _, k := range slices.Sorted(maps.Keys(b.objs)) {
		out = append(out, ObjectInfo{Key: k, Size: int64(len(b.objs[k]))})
	}
	return out, nil
}

func (b *memBucket) keys() []string {
	infos, _ := b.List(context.Background())
	var out []string
	for _, o := range infos {
		out = append(out, o.Key)
	}
	return out
}

type memSettings map[string]string

func (s memSettings) Setting(_ context.Context, k string) (string, error) { return s[k], nil }
func (s memSettings) SetSetting(_ context.Context, k, v string) error     { s[k] = v; return nil }

type rig struct {
	m      *Manager
	bucket *memBucket
	dir    string
	said   []string
	now    time.Time
}

func newRig(t *testing.T) *rig {
	r := &rig{bucket: &memBucket{objs: map[string][]byte{}}, dir: t.TempDir(), now: time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)}
	r.m = New(memSettings{}, r.dir, func(_ context.Context, s string) { r.said = append(r.said, s) },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.m.open = func(Config) (Bucket, error) { return r.bucket, nil }
	r.m.now = func() time.Time { return r.now }
	return r
}

func (r *rig) configure(t *testing.T) string {
	t.Helper()
	key, err := r.m.Configure(context.Background(), Config{Endpoint: "e", Bucket: "b", AccessKey: "a", SecretKey: "s"})
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func (r *rig) backup(t *testing.T, project, name, body string) {
	t.Helper()
	dir := filepath.Join(r.dir, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSyncUploadsEncryptedOnce(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if err := r.m.Sync(ctx); err != nil || len(r.said) != 0 {
		t.Fatalf("unconfigured sync: %v %q", err, r.said)
	}
	keyB64 := r.configure(t)
	if again, _ := r.m.Configure(ctx, Config{Endpoint: "e", Bucket: "b", AccessKey: "a", SecretKey: "s"}); again != "" {
		t.Fatal("the recovery key was shown twice")
	}
	r.backup(t, "app", "20260630T100000Z-manual.dump", "database contents")
	r.backup(t, "app", ".partial-123", "half written")
	if err := r.m.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	keys := r.bucket.keys()
	if !slices.Equal(keys, []string{"app/20260630T100000Z-manual.dump.enc"}) {
		t.Fatalf("keys = %v", keys)
	}
	stored := r.bucket.objs[keys[0]]
	if bytes.Contains(stored, []byte("database contents")) {
		t.Fatal("uploaded in the clear")
	}
	key, _ := base64.StdEncoding.DecodeString(keyB64)
	var plain bytes.Buffer
	if err := Decrypt(&plain, bytes.NewReader(stored), key); err != nil || plain.String() != "database contents" {
		t.Fatalf("decrypt: %q, %v", plain.String(), err)
	}
	// A second sync uploads nothing: the object keeps its bytes (a re-upload would re-encrypt).
	if err := r.m.Sync(ctx); err != nil || !bytes.Equal(r.bucket.objs[keys[0]], stored) {
		t.Fatalf("second sync changed the object: %v", err)
	}
}

func TestSyncAppliesRetentionOnlyToBackups(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.configure(t)
	r.backup(t, "app", "20260101T000000Z-daily.rdb", "old")
	r.bucket.objs["app/notes.txt"] = []byte("not ours")
	if err := r.m.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	keys := r.bucket.keys()
	if !slices.Equal(keys, []string{"app/notes.txt"}) {
		t.Fatalf("keys = %v: the old backup should be gone and the stranger kept", keys)
	}
}

func TestOwnerHearsOnceAboutAnOutage(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.configure(t)
	r.backup(t, "app", "20260630T100000Z-manual.dump", "x")
	r.bucket.failPuts = true
	for range 3 {
		if err := r.m.Sync(ctx); err == nil {
			t.Fatal("sync succeeded against a dead bucket")
		}
	}
	if len(r.said) != 1 || !strings.Contains(r.said[0], "failed") {
		t.Fatalf("said = %q, want one failure message", r.said)
	}
	if st, _ := r.m.Status(ctx); st.LastError == "" || !st.Configured {
		t.Fatalf("status = %+v", st)
	}
	r.bucket.failPuts = false
	if err := r.m.Sync(ctx); err != nil || len(r.said) != 2 || !strings.Contains(r.said[1], "again") {
		t.Fatalf("recovery: %v %q", err, r.said)
	}
	r.m.Sync(ctx)
	if len(r.said) != 2 {
		t.Fatalf("said = %q", r.said)
	}
}

func TestConfigureRejectsAnUnwritableBucket(t *testing.T) {
	r := newRig(t)
	r.bucket.failPuts = true
	if _, err := r.m.Configure(context.Background(), Config{Endpoint: "e", Bucket: "b", AccessKey: "a", SecretKey: "s"}); err == nil {
		t.Fatal("configured a bucket that rejects writes")
	}
	if st, _ := r.m.Status(context.Background()); st.Configured {
		t.Fatal("a failed setup was saved")
	}
}

func TestListAndOpenReadWhatSyncUploaded(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.configure(t)
	r.backup(t, "app", "20260630T090000Z-daily.dump", "older")
	r.backup(t, "app", "20260630T100000Z-manual.dump", "newer")
	r.backup(t, "other", "20260630T100000Z-manual.dump", "not app's")
	if err := r.m.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.m.Enabled(ctx); !ok {
		t.Fatal("not enabled after setup")
	}
	list, err := r.m.List(ctx, "app")
	if err != nil || len(list) != 2 || list[0].Name != "20260630T100000Z-manual.dump" || list[0].Size == 0 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	rc, err := r.m.Open(ctx, "app", list[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if body, err := io.ReadAll(rc); err != nil || string(body) != "newer" {
		t.Fatalf("open = %q, %v", body, err)
	}
	// A different recovery key is an error at the end of the stream, not silent garbage.
	bad, err := OpenObject(ctx, r.bucket, "app/"+list[0].Name+".enc", bytes.Repeat([]byte{1}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(bad); err == nil {
		t.Fatal("a wrong key was accepted")
	}
}

func TestRestoreServer(t *testing.T) {
	ctx := context.Background()
	key := bytes.Repeat([]byte{4}, KeySize)
	b := &memBucket{objs: map[string][]byte{}}
	put := func(name string, body []byte) {
		var enc bytes.Buffer
		if err := Encrypt(&enc, bytes.NewReader(body), key); err != nil {
			t.Fatal(err)
		}
		b.objs[name+".enc"] = enc.Bytes()
	}
	archive := func(db string) []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, f := range [][2]string{{"kayly.db", db}, {"master.key", "KEY"}} {
			tw.WriteHeader(&tar.Header{Name: f[0], Mode: 0o600, Size: int64(len(f[1]))})
			tw.Write([]byte(f[1]))
		}
		tw.Close()
		return buf.Bytes()
	}
	put("_kayly/20260101T000000Z-daily.kayly.tar", archive("old db"))
	put("_kayly/20260102T000000Z-daily.kayly.tar", archive("new db"))
	put("leryo/20260101T000000Z-daily.dump", []byte("old dump"))
	put("leryo/20260102T000000Z-daily.dump", []byte("new dump"))
	put("leryo/20260102T000000Z-daily.vol-uploads.tar", []byte("uploads"))
	b.objs["leryo/readme.txt"] = []byte("not a backup")

	data, backups := t.TempDir(), t.TempDir()
	var said []string
	if err := RestoreServer(ctx, b, key, data, backups, func(f string, a ...any) { said = append(said, fmt.Sprintf(f, a...)) }); err != nil {
		t.Fatal(err)
	}
	read := func(parts ...string) string {
		body, err := os.ReadFile(filepath.Join(parts...))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	if read(data, "kayly.db") != "new db" || read(data, "master.key") != "KEY" {
		t.Fatal("the newest Kayly backup was not unpacked")
	}
	if read(backups, "leryo", "20260102T000000Z-daily.dump") != "new dump" ||
		read(backups, "leryo", "20260102T000000Z-daily.vol-uploads.tar") != "uploads" {
		t.Fatal("the newest backup of each kind was not downloaded")
	}
	if _, err := os.Stat(filepath.Join(backups, "leryo", "20260101T000000Z-daily.dump")); err == nil {
		t.Error("an older dump was downloaded")
	}
	if fi, _ := os.Stat(filepath.Join(data, "master.key")); fi.Mode().Perm() != 0o600 {
		t.Errorf("master.key mode = %v", fi.Mode())
	}
	// A second run must not overwrite the restored database.
	if err := RestoreServer(ctx, b, key, data, backups, func(string, ...any) {}); err == nil {
		t.Fatal("restored over an existing database")
	}
}

func TestRestoreServerNeedsAKaylyBackupAndTheRightKey(t *testing.T) {
	ctx := context.Background()
	key := bytes.Repeat([]byte{4}, KeySize)
	empty := &memBucket{objs: map[string][]byte{}}
	if err := RestoreServer(ctx, empty, key, t.TempDir(), t.TempDir(), func(string, ...any) {}); err == nil {
		t.Fatal("restored from an empty bucket")
	}
	var enc bytes.Buffer
	Encrypt(&enc, bytes.NewReader([]byte("x")), key)
	b := &memBucket{objs: map[string][]byte{"_kayly/20260102T000000Z-daily.kayly.tar.enc": enc.Bytes()}}
	data := t.TempDir()
	if err := RestoreServer(ctx, b, bytes.Repeat([]byte{5}, KeySize), data, t.TempDir(), func(string, ...any) {}); err == nil {
		t.Fatal("restored with the wrong recovery key")
	}
	if _, err := os.Stat(filepath.Join(data, "kayly.db")); err == nil {
		t.Fatal("a failed restore left a database behind")
	}
}
