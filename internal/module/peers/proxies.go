package peers

// proxies.json (spec §4.5): the helper manager's durable ownership record.
// This file owns the record type and its read/write; helpers.go decides
// WHEN it is written and sweep.go what to do with what it finds.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	ipeers "github.com/wake/purdex/internal/peers"
)

// proxyRecord is one entry of proxies.json (spec §4.5): enough to prove,
// after a restart, which process and which files were ours.
type proxyRecord struct {
	PID       int              `json:"pid"`
	ProcStart string           `json:"proc_start"`
	Sock      string           `json:"sock"`
	Files     []string         `json:"files"`
	Origin    ipeers.OriginKey `json:"origin"`
}

// recordOf is h's proxies.json entry. Caller holds mu (or owns h).
func recordOf(h *helper) proxyRecord {
	return proxyRecord{
		PID:       h.pid,
		ProcStart: h.procStart,
		Sock:      h.sock,
		Files:     append([]string(nil), h.files...),
		Origin:    h.key,
	}
}

// readProxies loads proxiesPath: missing ⇒ none; unparsable ⇒ logged and
// treated as none (nothing in it can prove ownership of anything).
func (m *helperManager) readProxies() []proxyRecord {
	data, err := os.ReadFile(m.proxiesPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			m.log("peers: read %s: %v (treating as empty)", m.proxiesPath, err)
		}
		return nil
	}
	var records []proxyRecord
	if err := json.Unmarshal(data, &records); err != nil {
		m.log("peers: %s is unparsable: %v (treating as empty; nothing in it can be proven ours)", m.proxiesPath, err)
		return nil
	}
	return records
}

// writeProxiesLocked writes the record of every instance with a pid plus
// every unresolved record to proxiesPath atomically: temp file in the
// same directory, fsync, rename. Caller holds mu.
func (m *helperManager) writeProxiesLocked() error {
	records := make([]proxyRecord, 0, len(m.helpers)+len(m.unresolved))
	for _, h := range m.helpers {
		if h.pid != 0 {
			records = append(records, recordOf(h))
		}
	}
	for _, u := range m.unresolved {
		records = append(records, u.proxyRecord)
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].PID < records[j].PID })
	return writeProxiesFile(m.proxiesPath, records)
}

// writeProxiesFile is the atomic write behind writeProxiesLocked.
func writeProxiesFile(path string, records []proxyRecord) error {
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
