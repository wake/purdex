package codexbroker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// LaunchRegistry implements atomic load/save and helper mutators for
// launch-registry.json. The file path is supplied per call so tests and
// production can use distinct locations without singleton state.
//
// Per plan §11 promise #1: P2 ships the read API only — the spawn-time
// write is deferred to PR-G (Lights spawn-hook integration). Mlab will
// initially load an empty registry, which causes every visible broker to
// be classified as foreign_quarantine in EvalDecision (task F).
type LaunchRegistry struct{}

// Load reads the JSON file at path and decodes it into a LaunchRegistryFile.
//
// On os.IsNotExist (file absent), Load returns a fresh empty file with
// version=1 so Module.Init can keep starting up. Genuine I/O errors and
// malformed JSON propagate.
func (s *LaunchRegistry) Load(path string) (*LaunchRegistryFile, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &LaunchRegistryFile{Version: 1}, nil
		}
		return nil, err
	}
	var f LaunchRegistryFile
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, err
	}
	if f.Version == 0 {
		f.Version = 1
	}
	return &f, nil
}

// Save writes f to path atomically (write-to-tmp + os.Rename). On any I/O
// failure the original file is left untouched.
//
// PR review finding H: Save now ensures filepath.Dir(path) exists via
// os.MkdirAll before writing the temp file. The canonical path under
// pluginDataRoot exists today, but the API should own its own first-write
// behaviour so fresh profiles or relocated data roots work without an
// out-of-band MkdirAll dance from the caller.
func (s *LaunchRegistry) Save(path string, f *LaunchRegistryFile) error {
	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Register inserts entry into f.Entries. If an entry with the same
// BrokerKey already exists, it is overwritten — matches the upsert
// semantic QuarantineStore uses for the same reason (re-spawned broker
// shares brokerKey with predecessor).
func (s *LaunchRegistry) Register(f *LaunchRegistryFile, entry LaunchEntry) *LaunchRegistryFile {
	if f == nil {
		f = &LaunchRegistryFile{Version: 1}
	}
	for i, existing := range f.Entries {
		if existing.BrokerKey == entry.BrokerKey {
			f.Entries[i] = entry
			return f
		}
	}
	f.Entries = append(f.Entries, entry)
	return f
}

// Remove drops the entry matching (brokerKey, pid). Both must match — the
// pid filter defends against removing a re-spawned broker's record when
// observing the predecessor's death.
func (s *LaunchRegistry) Remove(f *LaunchRegistryFile, brokerKey string, pid int) *LaunchRegistryFile {
	if f == nil {
		return &LaunchRegistryFile{Version: 1}
	}
	out := make([]LaunchEntry, 0, len(f.Entries))
	for _, e := range f.Entries {
		if e.BrokerKey == brokerKey && e.Pid == pid {
			continue
		}
		out = append(out, e)
	}
	f.Entries = out
	return f
}

// Lookup returns the entry for the given brokerKey, or (nil, false) if no
// entry is found. Implements LaunchRegistryReader.
func (f *LaunchRegistryFile) Lookup(brokerKey string) (*LaunchEntry, bool) {
	if f == nil {
		return nil, false
	}
	for i := range f.Entries {
		if f.Entries[i].BrokerKey == brokerKey {
			// Return pointer into the slice so callers can read all
			// fields without an extra copy. The registry is treated as
			// read-only by predicate C; mutation goes through Register/
			// Remove.
			return &f.Entries[i], true
		}
	}
	return nil, false
}

// Empty reports whether the registry has no entries. Implements
// LaunchRegistryReader.
//
// Used by EvalDecision to distinguish "no purdex broker tracked yet"
// (P2 steady state on mlab) from "this specific brokerKey is foreign"
// (post-spawn-hook). Both feed AnomalyForeignOwner classification but
// the distinction matters for the operator dashboard / debug surface.
func (f *LaunchRegistryFile) Empty() bool {
	if f == nil {
		return true
	}
	return len(f.Entries) == 0
}

// launchRegistryPath is the canonical file location under the plugin data
// root; used by Module wiring (task R).
func launchRegistryPath(pluginDataRoot string) string {
	return filepath.Join(pluginDataRoot, "launch-registry.json")
}
