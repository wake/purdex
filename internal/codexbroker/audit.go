package codexbroker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultBrokerLogTailLines is the spec §5.5 default — last 200 lines of
// broker.log are captured into the audit preimage to give operators
// forensic context without bloating the dump.
const DefaultBrokerLogTailLines = 200

// WritePreimage writes audit/orphan-<brokerKey>-<unixTs>.json containing
// the BrokerRecord, the DecisionResult, and a tail of broker.log.
//
// Atomic write: tmp file + os.Rename. The directory is created if absent
// (audit dir may not yet exist on first kill of daemon lifetime).
//
// Per spec §5.4 line 465 (Step 1): the preimage MUST be committed before
// any signal reaches the broker. KillSequence.Run aborts the entire
// sequence on WritePreimage error so no signal can outrun the audit.
func WritePreimage(auditDir string, rec BrokerRecord, decision DecisionResult, logLines []string) (string, error) {
	if auditDir == "" {
		return "", fmt.Errorf("audit dir is empty")
	}
	if err := os.MkdirAll(auditDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir audit dir: %w", err)
	}
	now := time.Now().UTC()
	dump := AuditDump{
		KilledAt:      now,
		Reason:        decision.Reason,
		Broker:        rec,
		Decision:      decision,
		BrokerLogTail: logLines,
	}
	if dump.BrokerLogTail == nil {
		dump.BrokerLogTail = []string{}
	}
	body, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return "", err
	}
	fname := fmt.Sprintf("orphan-%s-%d.json", rec.Key, now.Unix())
	path := filepath.Join(auditDir, fname)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// AppendPostscript opens an existing audit preimage file, merges the kill
// sequence postscript, and atomically rewrites the file.
//
// Best-effort per spec §5.4 line 465 — the preimage is the durable record;
// postscript failure is logged by KillSequence.Run (task P) but does not
// fail the kill.
func AppendPostscript(filePath string, result KillResult) error {
	body, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	var dump AuditDump
	if err := json.Unmarshal(body, &dump); err != nil {
		return err
	}
	dump.KillSequence = AuditKillSequence{
		GracefulOk:    result.GracefulOk,
		TermOk:        result.TermOk,
		KillOk:        result.KillOk,
		StepLatencyMs: result.StepLatencyMs,
	}
	out, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return err
	}
	tmp := filePath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filePath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ReadBrokerLogTail reads <stateDir>/broker.log and returns up to `lines`
// lines from the tail. Returns an empty slice (no error) if the file is
// absent or unreadable — best-effort only; the audit dump still proceeds
// without log context.
func ReadBrokerLogTail(stateDir string, fs FS, lines int) []string {
	if lines <= 0 {
		lines = DefaultBrokerLogTailLines
	}
	if stateDir == "" || fs == nil {
		return nil
	}
	rc, err := fs.Open(filepath.Join(stateDir, "broker.log"))
	if err != nil {
		return nil
	}
	defer rc.Close()
	// Streaming ring buffer: keep at most `lines` entries in memory so
	// large broker.log files don't blow heap.
	ring := make([]string, 0, lines)
	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 1<<16), 1<<20)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if len(ring) < lines {
			ring = append(ring, line)
			continue
		}
		// Shift left then append (ring buffer at capacity).
		copy(ring, ring[1:])
		ring[len(ring)-1] = line
	}
	return ring
}
