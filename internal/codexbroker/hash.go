package codexbroker

import (
	"crypto/sha256"
	"encoding/hex"
)

// BrokerKey computes the broker correlation key from a raw cwd argv value.
//
// The algorithm is byte-identical to codex CLI's own state-dir naming
// (`scripts/lib/state.mjs::resolveStateDir`), which is:
//
//	brokerKey = sha256(realpath(cwd) || cwd)[:16]
//
// where `realpath || cwd` means: try to evaluate symlinks; on failure use
// the raw value verbatim. NO NFC normalisation, NO case-fold — codex does
// not do them, so we MUST not, otherwise our key won't match the state-dir
// suffix codex created.
//
// On EvalSymlinks failure the raw cwd is hashed and an
// AnomalyCwdUnresolvable code is returned for the caller to attach to the
// BrokerRecord. Path operations go through the injected FS so tests can
// mock symlink resolution without touching the real filesystem.
//
// P2 may reintroduce case/Unicode-fold for collision *detection* (separate
// anomaly), but never as part of the primary key.
func BrokerKey(rawCwd string, fs FS) (key string, resolved string, anomaly *AnomalyCode) {
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

	sum := sha256.Sum256([]byte(target))
	key = hex.EncodeToString(sum[:])[:16]
	return key, resolved, anomaly
}
