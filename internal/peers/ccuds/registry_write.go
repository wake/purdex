package ccuds

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/peers"
)

// RegistryEntry is what a virtual peer publishes about itself in Claude
// Code's session registry (~/.claude/sessions). Fields map 1:1 onto the
// real <pid>.json keys WriteRegistry emits.
type RegistryEntry struct {
	PID          int
	SessionID    string
	Name         string
	Cwd          string
	ProcStart    string // TZ=UTC ps -p <pid> -o lstart=, compared byte-for-byte by the harness
	Version      string
	Inbox        string // messagingSocketPath
	PidDomain    string // "" ⇒ runtime.GOOS
	PeerFeatures []string
}

// DefaultPeerFeatures is the list observed on 2.1.270 (spec §3.1 / spike),
// used only when no live Claude Code entry is available to copy from.
var DefaultPeerFeatures = []string{"notify_idle", "reply_across_default_dirs", "artifact_yield"}

// registryJSON is the on-disk <pid>.json layout of a 2.1.270 entry, in the
// order the harness writes it (minus tmux, which a virtual peer lacks).
type registryJSON struct {
	PID                 int      `json:"pid"`
	SessionID           string   `json:"sessionId"`
	Cwd                 string   `json:"cwd"`
	StartedAt           int64    `json:"startedAt"`
	ProcStart           string   `json:"procStart"`
	Version             string   `json:"version"`
	PeerProtocol        int      `json:"peerProtocol"`
	PeerFeatures        []string `json:"peerFeatures"`
	Kind                string   `json:"kind"`
	Entrypoint          string   `json:"entrypoint"`
	PidDomain           string   `json:"pidDomain"`
	MessagingSocketPath string   `json:"messagingSocketPath"`
	Name                string   `json:"name"`
	NameSource          string   `json:"nameSource"`
	NameSince           int64    `json:"nameSince"`
	UpdatedAt           int64    `json:"updatedAt"`
	Status              string   `json:"status"`
	StatusUpdatedAt     int64    `json:"statusUpdatedAt"`
}

// registryKey is the on-disk <pid>.<sha256(token)>.key layout.
type registryKey struct {
	PeerToken string `json:"peerToken"`
	ProcStart string `json:"procStart"`
	PidDomain string `json:"pidDomain"`
}

// RegistryFiles returns the two registry paths for pid: <dir>/<pid>.json
// and <dir>/<pid>.<sha256hex(peerToken)>.key.
func RegistryFiles(dir string, pid int, peerToken string) (jsonPath, keyPath string) {
	sum := sha256.Sum256([]byte(peerToken))
	jsonPath = filepath.Join(dir, strconv.Itoa(pid)+".json")
	keyPath = filepath.Join(dir, fmt.Sprintf("%d.%x.key", pid, sum))
	return jsonPath, keyPath
}

// WriteRegistry creates both registry files for e with
// O_CREATE|O_EXCL|O_WRONLY|O_NOFOLLOW (json 0644, key 0600), mirroring a
// real Claude Code entry. A nil PeerFeatures becomes DefaultPeerFeatures
// and an empty PidDomain becomes runtime.GOOS. If the key cannot be
// written the json is removed again; should that rollback itself fail, the
// returned error joins both and created names the json that is still on
// disk. Otherwise created lists the paths that exist on return, in the
// order [json, key].
func WriteRegistry(dir string, e RegistryEntry, peerToken string) (created []string, err error) {
	now := time.Now().UnixMilli()
	features := e.PeerFeatures
	if features == nil {
		features = DefaultPeerFeatures
	}
	domain := e.PidDomain
	if domain == "" {
		domain = runtime.GOOS
	}
	entry := registryJSON{
		PID:                 e.PID,
		SessionID:           e.SessionID,
		Cwd:                 e.Cwd,
		StartedAt:           now,
		ProcStart:           e.ProcStart,
		Version:             e.Version,
		PeerProtocol:        1,
		PeerFeatures:        features,
		Kind:                "interactive",
		Entrypoint:          "cli",
		PidDomain:           domain,
		MessagingSocketPath: e.Inbox,
		Name:                e.Name,
		NameSource:          "user",
		NameSince:           now,
		UpdatedAt:           now,
		Status:              "idle",
		StatusUpdatedAt:     now,
	}
	key := registryKey{PeerToken: peerToken, ProcStart: e.ProcStart, PidDomain: domain}

	jsonPath, keyPath := RegistryFiles(dir, e.PID, peerToken)
	if err := writeExclusive(jsonPath, entry, 0o644); err != nil {
		return nil, err
	}
	if err := writeExclusive(keyPath, key, 0o600); err != nil {
		if rmErr := os.Remove(jsonPath); rmErr != nil {
			return []string{jsonPath}, errors.Join(err, rmErr)
		}
		return nil, err
	}
	return []string{jsonPath, keyPath}, nil
}

// writeExclusive marshals v (no HTML escaping, no trailing newline — the
// shape JSON.stringify produces) into a brand-new file at path. On a write
// failure after creation the partial file is removed.
func writeExclusive(path string, v any, mode fs.FileMode) error {
	data, err := marshalCompact(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

func marshalCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// RewriteRegistryName updates only the "name" and "nameSince" fields of
// <dir>/<pid>.json in place. Every other field's bytes pass through
// byte-identical (decoded as json.RawMessage, not map[string]any, so a
// large integer is never round-tripped through float64). The new content
// is written to <dir>/.<pid>.json.tmp (0644, O_EXCL — the leading dot
// keeps it out of ReadRegistryDiag's "^([0-9]+)\.json$" candidate
// pattern), fsynced, then renamed over <pid>.json. Any failure removes the
// temp file and returns the error; the original file is left untouched.
func RewriteRegistryName(dir string, pid int, name string, nameSince int64) error {
	jsonPath := filepath.Join(dir, strconv.Itoa(pid)+".json")
	data, ok := peers.ReadRegistryCandidate(jsonPath)
	if !ok {
		return fmt.Errorf("ccuds: cannot read registry file %s", jsonPath)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("ccuds: parse %s: %w", jsonPath, err)
	}

	nameJSON, err := marshalCompact(name)
	if err != nil {
		return err
	}
	fields["name"] = nameJSON
	fields["nameSince"] = json.RawMessage(strconv.FormatInt(nameSince, 10))

	out, err := marshalCompact(fields)
	if err != nil {
		return err
	}

	tmpPath := filepath.Join(dir, "."+strconv.Itoa(pid)+".json.tmp")
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(out); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, jsonPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// RemoveRegistry unlinks every path. A path that is already gone is not
// an error; the first other error is returned after every path has been
// attempted.
func RemoveRegistry(paths []string) error {
	var first error
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) && first == nil {
			first = err
		}
	}
	return first
}

// ReadPeerFeatures returns the peerFeatures list of <dir>/<pid>.json, read
// under peers.ReadRegistryCandidate's contract (O_NOFOLLOW, regular file,
// 64 KiB cap). ok is false when the file cannot be read that way, is
// unparsable, or has no (or a null) peerFeatures field.
func ReadPeerFeatures(dir string, pid int) (features []string, ok bool) {
	data, ok := peers.ReadRegistryCandidate(filepath.Join(dir, strconv.Itoa(pid)+".json"))
	if !ok {
		return nil, false
	}
	var wire struct {
		PeerFeatures *[]string `json:"peerFeatures"`
	}
	if err := json.Unmarshal(data, &wire); err != nil || wire.PeerFeatures == nil {
		return nil, false
	}
	return *wire.PeerFeatures, true
}

// RegistryProcStart returns the "procStart" recorded in a registry file
// (either the <pid>.json or the key file, which both carry it at top
// level), or "" when the file cannot be read or parsed. The daemon's sweep
// uses it to prove a file belongs to the process it is about to unlink.
func RegistryProcStart(path string) string {
	data, ok := peers.ReadRegistryCandidate(path)
	if !ok {
		return ""
	}
	var wire struct {
		ProcStart string `json:"procStart"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return ""
	}
	return wire.ProcStart
}
