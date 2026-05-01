package codexbroker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// brokerJSONFile is the schema of `<state-dir>/broker.json` per spec §3.2.
type brokerJSONFile struct {
	Endpoint   string `json:"endpoint"`
	PidFile    string `json:"pidFile"`
	LogFile    string `json:"logFile"`
	SessionDir string `json:"sessionDir"`
	PID        int    `json:"pid"`
}

// stateJSONJob is a relevant subset of `<state-dir>/state.json.jobs[]` per spec §3.2.
type stateJSONJob struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	PID       *int       `json:"pid"`
	Phase     string     `json:"phase"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	Completed *time.Time `json:"completedAt,omitempty"`
}

// stateJSONFile is the relevant root subset.
type stateJSONFile struct {
	Version int            `json:"version"`
	Jobs    []stateJSONJob `json:"jobs"`
}

// stateCandidate is the per-state-dir intermediate; reconcile (Task I) merges
// these with process / socket candidates into the final BrokerRecord.
type stateCandidate struct {
	Key               string
	StateDir          string
	HasBrokerJSON     bool
	BrokerJSON        brokerJSONFile
	StateJSONReadable bool
	JobCounts         JobCounts
	LastJobUpdatedAt  *time.Time
	Anomalies         []Anomaly
}

// scanStateDirs enumerates every directory under root containing a
// `broker.json`, parses both `broker.json` and `state.json` (best-effort),
// and rolls up `JobCounts` plus `LastJobUpdatedAt`.
//
// Per-directory operations honour a perDirBudget wall-clock cap. If reads
// exceed the budget the directory is skipped with an anomaly. Directories
// without `broker.json` are silently skipped (historical, out of scope).
//
// Whole-scan failures (root missing, root unreadable) are non-fatal: an
// empty candidate slice is returned; orchestrator logs as a partial scan.
func scanStateDirs(ctx context.Context, fs FS, root string, perDirBudget time.Duration) ([]stateCandidate, []Anomaly) {
	var (
		cands    []stateCandidate
		topAnoms []Anomaly
	)
	if err := ctx.Err(); err != nil {
		return nil, nil
	}

	entries, err := fs.ReadDir(root)
	if err != nil {
		// Root missing or unreadable: gracefully empty. Caller may want to
		// surface as scanSourceTimeouts but we don't tag a per-record
		// anomaly because there's no record.
		return nil, nil
	}
	for _, ent := range entries {
		if err := ctx.Err(); err != nil {
			return cands, topAnoms
		}
		if !ent.IsDir() {
			continue
		}
		dirPath := filepath.Join(root, ent.Name())
		c, anoms := scanOneStateDir(fs, dirPath, ent.Name(), perDirBudget)
		topAnoms = append(topAnoms, anoms...)
		if c != nil {
			cands = append(cands, *c)
		}
	}
	return cands, topAnoms
}

// scanOneStateDir handles a single dir; returns nil if dir has no broker.json
// or if it should otherwise be skipped from the result set.
//
// budget is the wall-clock cap for reading broker.json + state.json. We
// enforce by deadline goroutine: if reads exceed the budget, the candidate
// is emitted with an anomaly tagged with timeout context.
func scanOneStateDir(fs FS, dirPath, dirName string, budget time.Duration) (*stateCandidate, []Anomaly) {
	brokerPath := filepath.Join(dirPath, "broker.json")
	statePath := filepath.Join(dirPath, "state.json")

	// Run both reads under a single per-dir budget.
	type result struct {
		brokerBytes []byte
		brokerErr   error
		stateBytes  []byte
		stateErr    error
	}
	ch := make(chan result, 1)
	go func() {
		var r result
		// broker.json read.
		if rc, err := fs.Open(brokerPath); err == nil {
			r.brokerBytes, r.brokerErr = io.ReadAll(rc)
			_ = rc.Close()
		} else {
			r.brokerErr = err
		}
		// state.json read.
		if rc, err := fs.Open(statePath); err == nil {
			r.stateBytes, r.stateErr = io.ReadAll(rc)
			_ = rc.Close()
		} else {
			r.stateErr = err
		}
		ch <- r
	}()

	var r result
	select {
	case r = <-ch:
	case <-time.After(budget):
		// Per-dir timeout. Skip the dir entirely; we cannot tell whether
		// broker.json exists. Emit a top-level anomaly so the operator
		// knows.
		return nil, []Anomaly{{
			Code:   AnomalyBrokerJSONUnreadable,
			Detail: fmt.Sprintf("per-dir budget %v exceeded for %s", budget, dirPath),
		}}
	}

	// No broker.json → historical, skip.
	if r.brokerErr != nil {
		// Distinguish "not present" (historical) from "unreadable".
		// If the file simply doesn't exist we silently skip; any other
		// open error we surface.
		if isNotExistError(r.brokerErr) {
			return nil, nil
		}
		return nil, []Anomaly{{
			Code:   AnomalyBrokerJSONUnreadable,
			Detail: fmt.Sprintf("%s: %s", brokerPath, r.brokerErr),
		}}
	}

	// Parse broker.json.
	var brokerJSON brokerJSONFile
	if err := json.Unmarshal(r.brokerBytes, &brokerJSON); err != nil {
		return nil, []Anomaly{{
			Code:   AnomalyBrokerJSONUnreadable,
			Detail: fmt.Sprintf("%s: parse: %s", brokerPath, err),
		}}
	}

	c := &stateCandidate{
		Key:           extractKeyFromDirName(dirName),
		StateDir:      dirPath,
		HasBrokerJSON: true,
		BrokerJSON:    brokerJSON,
	}

	// Parse state.json (best-effort).
	if r.stateErr != nil || len(r.stateBytes) == 0 {
		c.Anomalies = append(c.Anomalies, Anomaly{
			Code:   AnomalyStateJSONUnreadable,
			Detail: errString(r.stateErr, "missing"),
		})
	} else {
		var stateJSON stateJSONFile
		if err := json.Unmarshal(r.stateBytes, &stateJSON); err != nil {
			c.Anomalies = append(c.Anomalies, Anomaly{
				Code:   AnomalyStateJSONUnreadable,
				Detail: fmt.Sprintf("%s: parse: %s", statePath, err),
			})
		} else {
			c.StateJSONReadable = true
			c.JobCounts = rollUpJobs(stateJSON.Jobs)
			c.LastJobUpdatedAt = maxUpdatedAt(stateJSON.Jobs)
		}
	}

	return c, nil
}

// extractKeyFromDirName returns the trailing 16-hex suffix of a state-dir
// name, e.g. "purdex-1f4dd4d978bb1234" → "1f4dd4d978bb1234". If the suffix
// is not 16 hex, returns the whole name as a fallback (caller may attach
// an anomaly upstream).
func extractKeyFromDirName(name string) string {
	if i := strings.LastIndex(name, "-"); i >= 0 {
		suffix := name[i+1:]
		if len(suffix) == 16 && isHex(suffix) {
			return suffix
		}
	}
	return name
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// isNotExistError returns true when err is or wraps os.ErrNotExist. Used to
// silently skip historical state directories where broker.json is absent.
func isNotExistError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrNotExist)
}

func errString(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	return err.Error()
}

// rollUpJobs counts jobs per status bucket. Unknown statuses (schema drift)
// land in JobCounts.Unknown.
func rollUpJobs(jobs []stateJSONJob) JobCounts {
	var c JobCounts
	for _, j := range jobs {
		switch j.Status {
		case "queued":
			c.Queued++
		case "running":
			c.Running++
		case "completed":
			c.Completed++
		case "failed":
			c.Failed++
		case "cancelled":
			c.Cancelled++
		default:
			c.Unknown++
		}
	}
	return c
}

// maxUpdatedAt returns a pointer to the maximum updatedAt across jobs[],
// or nil when no job has updatedAt set or jobs is empty.
func maxUpdatedAt(jobs []stateJSONJob) *time.Time {
	var max time.Time
	any := false
	for _, j := range jobs {
		if j.UpdatedAt == nil {
			continue
		}
		if !any || j.UpdatedAt.After(max) {
			max = *j.UpdatedAt
			any = true
		}
	}
	if !any {
		return nil
	}
	return &max
}
