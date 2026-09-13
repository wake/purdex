// Package buildinfo holds the identity baked into a pdx binary at link time.
// It is a leaf package (stdlib only) so both core and the dev module can read
// it without an import cycle.
//
// Set via:
//
//	-ldflags "-X github.com/wake/purdex/internal/buildinfo.Hash=<short sha> \
//	          -X github.com/wake/purdex/internal/buildinfo.Version=<VERSION>"
package buildinfo

var (
	// Hash is the short git commit hash the binary was built from.
	Hash = "unknown"
	// Version is the contents of the VERSION file at build time.
	Version = "unknown"
)
