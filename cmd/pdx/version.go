// cmd/pdx/version.go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"

	"github.com/wake/purdex/internal/buildinfo"
)

// versionInfo is the identity of this binary. The JSON form is consumed by
// the Electron app to inspect an on-disk daemon without starting it.
type versionInfo struct {
	Version string `json:"version"`
	Hash    string `json:"hash"`
	GOOS    string `json:"goos"`
	GOARCH  string `json:"goarch"`
}

// runVersion prints the binary identity. `--json` selects the machine form.
// It never fails: an unknown identity prints "unknown".
func runVersion(args []string, w io.Writer) {
	info := versionInfo{
		Version: buildinfo.Version,
		Hash:    buildinfo.Hash,
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
	}
	for _, a := range args {
		if a == "--json" {
			_ = json.NewEncoder(w).Encode(info)
			return
		}
	}
	fmt.Fprintf(w, "pdx %s (%s) %s/%s\n", info.Version, info.Hash, info.GOOS, info.GOARCH)
}
