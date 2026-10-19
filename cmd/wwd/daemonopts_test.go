package main

import (
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/wwd/store"
)

// The daemon is given the earned-label table and the result cache the quarantine acts on: without them a disagreement read from an
// origin, and pdx wwd quarantine|release, would silently do nothing.
func TestTheDaemonIsGivenWhatTheQuarantineActsOn(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	o := daemonOptions(daemonParts{Store: st, Alias: "mserv"})
	if o.Labels == nil || o.CacheDrop == nil {
		t.Fatalf("Labels %v CacheDrop %v", o.Labels, o.CacheDrop)
	}
}
