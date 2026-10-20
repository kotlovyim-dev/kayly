package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kotlovyim-dev/kayly/internal/secret"
	"github.com/kotlovyim-dev/kayly/internal/store"
)

// state creates a data directory as the daemon leaves it, with the given projects.
func state(t *testing.T, projects ...string) string {
	t.Helper()
	data := t.TempDir()
	key, err := secret.LoadOrCreateKey(filepath.Join(data, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	box, _ := secret.NewBox(key)
	st, err := store.Open(context.Background(), filepath.Join(data, "kayly.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		if _, err := st.CreateProject(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	return data
}

func TestRestoreSetsAsideAFreshInstall(t *testing.T) {
	data := state(t)
	if err := setAsideFreshState(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"kayly.db", "master.key"} {
		if _, err := os.Stat(filepath.Join(data, f)); !os.IsNotExist(err) {
			t.Errorf("%s is still in place: %v", f, err)
		}
	}
	aside, _ := filepath.Glob(filepath.Join(data, "pre-restore-*", "kayly.db"))
	keys, _ := filepath.Glob(filepath.Join(data, "pre-restore-*", "master.key"))
	if len(aside) != 1 || len(keys) != 1 {
		t.Fatalf("set aside: %v %v", aside, keys)
	}
}

func TestRestoreNeverReplacesAServerWithProjects(t *testing.T) {
	data := state(t, "leryo")
	err := setAsideFreshState(context.Background(), data)
	if err == nil || !strings.Contains(err.Error(), "holds 1 projects") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, "kayly.db")); err != nil {
		t.Fatalf("the working state was touched: %v", err)
	}
}

func TestRestoreOnAnEmptyDirectory(t *testing.T) {
	if err := setAsideFreshState(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
