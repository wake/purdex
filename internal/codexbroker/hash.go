package codexbroker

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// BrokerKey computes the canonical broker correlation key from a raw cwd
// argv value, per spec §3.4:
//
//	brokerKey = sha256(NFC(canonicalCase(EvalSymlinks(cwd))))[:16]
//
// On EvalSymlinks failure the raw cwd is hashed instead and an
// AnomalyCwdUnresolvable code is returned for the caller to attach to the
// BrokerRecord.
//
// Path operations go through the injected FS so tests can mock symlink
// resolution without touching the real filesystem.
//
// The caseInsensitive flag controls whether the canonical-case step
// lower-cases the path; on macOS APFS case-preserving + case-insensitive
// volumes this is true. See hash_darwin.go / hash_linux.go / hash_other.go
// for platform detection.
func BrokerKey(rawCwd string, fs FS, caseInsensitive bool) (key string, resolved string, anomaly *AnomalyCode) {
	target := rawCwd
	resolvedPath, err := fs.EvalSymlinks(rawCwd)
	if err != nil {
		anom := AnomalyCwdUnresolvable
		anomaly = &anom
		// Hash the raw cwd so duplicate-runtime detection still works.
		// Resolved stays empty.
	} else {
		target = resolvedPath
		resolved = resolvedPath
	}

	target = norm.NFC.String(target)
	if caseInsensitive {
		target = strings.ToLower(target)
	}
	sum := sha256.Sum256([]byte(target))
	key = hex.EncodeToString(sum[:])[:16]
	return key, resolved, anomaly
}
