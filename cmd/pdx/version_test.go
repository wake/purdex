// cmd/pdx/version_test.go
package main

import (
	"bytes"
	"encoding/json"
	"runtime"
	"testing"

	"github.com/wake/purdex/internal/buildinfo"
)

func TestRunVersion_Plain(t *testing.T) {
	oldH, oldV := buildinfo.Hash, buildinfo.Version
	t.Cleanup(func() { buildinfo.Hash, buildinfo.Version = oldH, oldV })
	buildinfo.Hash, buildinfo.Version = "d5147bc", "1.0.0-alpha.334"

	var out bytes.Buffer
	runVersion(nil, &out)
	want := "pdx 1.0.0-alpha.334 (d5147bc) " + runtime.GOOS + "/" + runtime.GOARCH + "\n"
	if out.String() != want {
		t.Fatalf("plain output = %q, want %q", out.String(), want)
	}
}

func TestRunVersion_JSON(t *testing.T) {
	oldH, oldV := buildinfo.Hash, buildinfo.Version
	t.Cleanup(func() { buildinfo.Hash, buildinfo.Version = oldH, oldV })
	buildinfo.Hash, buildinfo.Version = "d5147bc", "1.0.0-alpha.334"

	var out bytes.Buffer
	runVersion([]string{"--json"}, &out)
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v (%q)", err, out.String())
	}
	want := map[string]string{
		"version": "1.0.0-alpha.334", "hash": "d5147bc",
		"goos": runtime.GOOS, "goarch": runtime.GOARCH,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != 4 {
		t.Errorf("unexpected keys: %v", got)
	}
}
