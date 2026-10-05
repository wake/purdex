// cmd/pdx/lastshutdown_test.go
package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLastShutdown_RoundTripAndConsume(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	if err := writeLastShutdown(dir, []string{"stop modules: nex: timeout"}, at); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "last-shutdown.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v, %v; want 0600 file", st, err)
	}
	r, err := takeLastShutdown(dir)
	if err != nil || r == nil || !r.At.Equal(at) || len(r.Errors) != 1 || r.Errors[0] != "stop modules: nex: timeout" {
		t.Fatalf("take = %+v, %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "last-shutdown.json")); !os.IsNotExist(err) {
		t.Fatal("take must delete the file")
	}
	if r, err := takeLastShutdown(dir); r != nil || err != nil {
		t.Fatalf("second take = %+v, %v; want nil, nil", r, err)
	}
}

func TestLastShutdown_CorruptIsDeleted(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "last-shutdown.json"), []byte("{not json"), 0o600)
	if _, err := takeLastShutdown(dir); err == nil {
		t.Fatal("want error for a corrupt record")
	}
	if _, err := os.Stat(filepath.Join(dir, "last-shutdown.json")); !os.IsNotExist(err) {
		t.Fatal("a corrupt record must be deleted")
	}
}
