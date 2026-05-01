package codexbroker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// socketCandidate is the per-cxc-* intermediate produced by the socket
// scanner. Reconcile (Task I) joins these with process candidates to
// classify socket_orphan and to recover the brokerKey via state-dir lookup.
type socketCandidate struct {
	// SyntheticKey is a deterministic 16-hex value derived from the socket
	// directory path. Reconcile may replace this with the canonical
	// brokerKey from a matching state dir; the prefix marks it as
	// "unknown:" until that resolution succeeds.
	SyntheticKey string

	SockDir     string
	PIDFromFile int  // 0 if broker.pid missing or unparseable.
	SockExists  bool // true iff broker.sock stat succeeded.

	Anomalies []Anomaly
}

// scanSockets enumerates every `cxc-*` directory under the configured roots
// and produces one socketCandidate per dir. **Pure read of fs**: glob,
// ReadFile (broker.pid), Stat (broker.sock). No process or network calls.
//
// Live/dead pid classification — i.e. matching PIDFromFile against the
// process scan — happens in reconcile (Task I); the socket scanner stays
// independent of the process layer.
func scanSockets(ctx context.Context, fs FS, roots []string, perDirBudget time.Duration) ([]socketCandidate, []Anomaly) {
	if err := ctx.Err(); err != nil {
		return nil, nil
	}
	var (
		out      []socketCandidate
		topAnoms []Anomaly
	)
	seen := make(map[string]bool)
	for _, root := range roots {
		matches, err := fs.Glob(filepath.Join(root, "cxc-*"))
		if err != nil {
			continue
		}
		for _, m := range matches {
			if seen[m] {
				continue
			}
			seen[m] = true
			if err := ctx.Err(); err != nil {
				return out, topAnoms
			}
			c := scanOneSocketDir(fs, m, perDirBudget)
			if c != nil {
				out = append(out, *c)
			}
		}
	}
	return out, topAnoms
}

// scanOneSocketDir reads broker.pid + Stat(broker.sock) under the per-dir
// budget. Returns a candidate even on error (with anomaly attached) so the
// directory is visible to operators.
func scanOneSocketDir(fs FS, dirPath string, budget time.Duration) *socketCandidate {
	pidPath := filepath.Join(dirPath, "broker.pid")
	sockPath := filepath.Join(dirPath, "broker.sock")

	type result struct {
		pidBytes []byte
		pidErr   error
		sockErr  error
	}
	ch := make(chan result, 1)
	go func() {
		var r result
		if rc, err := fs.Open(pidPath); err == nil {
			r.pidBytes, r.pidErr = io.ReadAll(rc)
			_ = rc.Close()
		} else {
			r.pidErr = err
		}
		_, r.sockErr = fs.Stat(sockPath)
		ch <- r
	}()

	var r result
	select {
	case r = <-ch:
	case <-time.After(budget):
		// Budget exceeded — emit a minimal candidate with anomaly so the
		// dir is visible.
		return &socketCandidate{
			SyntheticKey: synthSocketKey(dirPath),
			SockDir:      dirPath,
			Anomalies: []Anomaly{{
				Code:   AnomalySocketOrphan,
				Detail: fmt.Sprintf("per-dir budget %v exceeded", budget),
			}},
		}
	}

	c := &socketCandidate{
		SyntheticKey: synthSocketKey(dirPath),
		SockDir:      dirPath,
		SockExists:   r.sockErr == nil,
	}

	pidStr := strings.TrimSpace(string(r.pidBytes))
	if r.pidErr == nil && pidStr != "" {
		if pid, perr := strconv.Atoi(pidStr); perr == nil {
			c.PIDFromFile = pid
		} else {
			c.Anomalies = append(c.Anomalies, Anomaly{
				Code:   AnomalySocketOrphan,
				Detail: fmt.Sprintf("broker.pid not int: %q", pidStr),
			})
		}
	} else if r.pidErr != nil {
		c.Anomalies = append(c.Anomalies, Anomaly{
			Code:   AnomalySocketOrphan,
			Detail: fmt.Sprintf("broker.pid read: %s", r.pidErr),
		})
	}

	return c
}

// synthSocketKey returns a deterministic 16-hex synthetic key derived from
// the absolute socket directory path. Reconcile may replace this with the
// canonical brokerKey from a matching state dir.
func synthSocketKey(dirPath string) string {
	sum := sha256.Sum256([]byte(dirPath))
	return hex.EncodeToString(sum[:])[:16]
}
