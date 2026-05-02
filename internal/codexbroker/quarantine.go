package codexbroker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// QuarantineStore implements atomic load/save and helper mutators for
// audit/quarantine.json. The file path is supplied per call so tests and
// production can use distinct locations without singleton state.
type QuarantineStore struct{}

// Load reads the JSON file at path and decodes it into a QuarantineFile.
//
// On os.IsNotExist (file absent on disk), Load returns a fresh empty file
// with version=1 so Module.Init can keep starting up. Genuine I/O errors
// or malformed JSON propagate; the caller (Module.Init in task R) decides
// whether to rename the corrupt file aside and fall through.
func (s *QuarantineStore) Load(path string) (*QuarantineFile, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &QuarantineFile{Version: 1}, nil
		}
		return nil, err
	}
	var qf QuarantineFile
	if err := json.Unmarshal(body, &qf); err != nil {
		return nil, err
	}
	if qf.Version == 0 {
		qf.Version = 1
	}
	return &qf, nil
}

// Save writes qf to path atomically: write to path.tmp then os.Rename.
// On any I/O failure the original file (if present) is left untouched.
func (s *QuarantineStore) Save(path string, qf *QuarantineFile) error {
	body, err := json.MarshalIndent(qf, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		// Best-effort cleanup of the tmp file so we don't leave litter.
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// AddEntry appends entry to qf.Entries. If an entry with the same
// BrokerKey already exists, it is overwritten (the latest fingerprint
// wins — a re-spawned broker shares brokerKey with its predecessor).
func (s *QuarantineStore) AddEntry(qf *QuarantineFile, entry QuarantineEntry) *QuarantineFile {
	if qf == nil {
		qf = &QuarantineFile{Version: 1}
	}
	for i, existing := range qf.Entries {
		if existing.BrokerKey == entry.BrokerKey {
			qf.Entries[i] = entry
			return qf
		}
	}
	qf.Entries = append(qf.Entries, entry)
	return qf
}

// PurgeExpired returns a new QuarantineFile with any entry whose
// ExpiresAt ≤ now removed. The input qf is not mutated.
func (s *QuarantineStore) PurgeExpired(qf *QuarantineFile, now time.Time) *QuarantineFile {
	if qf == nil {
		return &QuarantineFile{Version: 1}
	}
	out := &QuarantineFile{Version: qf.Version}
	for _, e := range qf.Entries {
		if e.ExpiresAt.IsZero() || e.ExpiresAt.After(now) {
			out.Entries = append(out.Entries, e)
		}
	}
	return out
}

// IsQuarantined returns true if any entry in qf has the matching BrokerKey.
// Defensive: a nil qf returns false (no entries).
func IsQuarantined(qf *QuarantineFile, brokerKey string) bool {
	if qf == nil {
		return false
	}
	for _, e := range qf.Entries {
		if e.BrokerKey == brokerKey {
			return true
		}
	}
	return false
}

// quarantinePath is a small helper used by Module wiring to compute the
// canonical file location under the plugin data root.
func quarantinePath(pluginDataRoot string) string {
	return filepath.Join(pluginDataRoot, "audit", "quarantine.json")
}
