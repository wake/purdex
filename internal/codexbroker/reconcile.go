package codexbroker

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// reconcile merges processCandidates, stateCandidates, and socketCandidates
// into a flat slice of BrokerRecord per spec §4.2.
//
// Reconcile rules (summary; see spec for full text):
//   - Runtime identity is (Key, PID, Lstart). Each processCandidate produces
//     a distinct record.
//   - State-dir candidates merge into a process record when keys match AND
//     (a) state has no broker.json.pid, OR (b) state.broker.json.pid == process.PID.
//     Otherwise emit broker_json_pid_mismatch on the process record (but still
//     merge layers — the operator wants both views).
//   - Multiple process candidates with the same Key → multiple records, each
//     tagged duplicate_runtime.
//   - Multiple process candidates with the same Key but distinct CwdResolved
//     → broker_key_collision on each record.
//   - State-dir candidate with no matching process → standalone record tagged
//     state_dir_orphan.
//   - Socket candidate whose PIDFromFile is in the process scan → merges into
//     that process record (Sources |= SourceSocket).
//   - Socket candidate whose PIDFromFile is NOT in the process scan →
//     standalone record tagged socket_orphan.
//   - Process candidate with no matching state-dir → tagged process_orphan
//     AND state_dir_no_match.
//
// For each emitted record, we additionally populate CwdExists / CwdStatErr
// via fs.Stat (best-effort; classification per spec §4.2: ENOENT vs other).
func reconcile(
	ctx context.Context,
	fs FS,
	processC []processCandidate,
	stateC []stateCandidate,
	socketC []socketCandidate,
) ([]BrokerRecord, []Anomaly) {
	if err := ctx.Err(); err != nil {
		return nil, nil
	}

	// Index state candidates by Key. A given key may map to one state dir.
	stateByKey := make(map[string]*stateCandidate, len(stateC))
	for i := range stateC {
		stateByKey[stateC[i].Key] = &stateC[i]
	}

	// Index process candidates by PID for socket lookup. Identity is
	// (key, pid, lstart) but socket join is by PID alone.
	processByPID := make(map[int]*processCandidate, len(processC))
	for i := range processC {
		processByPID[processC[i].PID] = &processC[i]
	}

	// Detect duplicate-runtime and broker-key-collision groups by Key.
	processByKey := make(map[string][]*processCandidate, len(processC))
	for i := range processC {
		processByKey[processC[i].Key] = append(processByKey[processC[i].Key], &processC[i])
	}

	used := make(map[string]bool) // state keys consumed by a process record.
	usedPIDs := make(map[int]bool) // process PIDs whose socket was merged.

	// Track which sockets we've already merged into a process record.
	mergedSockets := make(map[string]bool) // SockDir → merged

	out := make([]BrokerRecord, 0, len(processC)+len(stateC)+len(socketC))
	var topAnoms []Anomaly

	// 1. Emit one record per process candidate.
	for i := range processC {
		pc := &processC[i]
		rec := BrokerRecord{
			Key:         pc.Key,
			PID:         pc.PID,
			Lstart:      pc.Lstart,
			PPID:        pc.PPID,
			RSSBytes:    pc.RSSKB * 1024,
			Cwd:         pc.RawCwd,
			CwdResolved: pc.CwdResolved,
			Endpoint:    pc.Endpoint,
			SocketDir:   socketDirFromEndpoint(pc.Endpoint),
			PidFile:     pc.PidFile,
			Sources:     SourceProcess,
			Anomalies:   append([]Anomaly{}, pc.Anomalies...),
		}

		// Duplicate-runtime detection: more than one process for the same Key.
		if len(processByKey[pc.Key]) > 1 {
			// Among the group, check whether any pair has differing canonical
			// cwd → that's broker_key_collision instead.
			distinctCwd := make(map[string]bool)
			for _, p := range processByKey[pc.Key] {
				distinctCwd[canonicalCwdForCollision(p)] = true
			}
			if len(distinctCwd) > 1 {
				rec.Anomalies = append(rec.Anomalies, Anomaly{
					Code:   AnomalyBrokerKeyCollision,
					Detail: fmt.Sprintf("Key=%s shared across %d distinct cwd", pc.Key, len(distinctCwd)),
				})
			} else {
				rec.Anomalies = append(rec.Anomalies, Anomaly{
					Code:   AnomalyDuplicateRuntime,
					Detail: fmt.Sprintf("%d processes share Key=%s", len(processByKey[pc.Key]), pc.Key),
				})
			}
		}

		// Merge state-dir layer.
		if sc, ok := stateByKey[pc.Key]; ok {
			rec.Sources |= SourceStateDir
			rec.StateDir = sc.StateDir
			rec.HasBrokerJSON = sc.HasBrokerJSON
			rec.BrokerJSONPID = sc.BrokerJSON.PID
			rec.StateJSONReadable = sc.StateJSONReadable
			rec.JobCounts = sc.JobCounts
			rec.LastJobUpdatedAt = sc.LastJobUpdatedAt
			rec.Anomalies = append(rec.Anomalies, sc.Anomalies...)
			if sc.BrokerJSON.PID != 0 && sc.BrokerJSON.PID != pc.PID {
				rec.Anomalies = append(rec.Anomalies, Anomaly{
					Code:   AnomalyBrokerJSONPidMismatch,
					Detail: fmt.Sprintf("process.pid=%d ≠ broker.json.pid=%d", pc.PID, sc.BrokerJSON.PID),
				})
			}
			used[pc.Key] = true
		} else {
			rec.Anomalies = append(rec.Anomalies, Anomaly{
				Code:   AnomalyProcessOrphan,
				Detail: fmt.Sprintf("no state dir for Key=%s", pc.Key),
			})
			rec.Anomalies = append(rec.Anomalies, Anomaly{
				Code:   AnomalyStateDirNoMatch,
				Detail: fmt.Sprintf("process record could not be matched to any state dir"),
			})
		}

		// Merge socket layer (lookup by PID).
		for j := range socketC {
			s := &socketC[j]
			if s.PIDFromFile != 0 && s.PIDFromFile == pc.PID {
				rec.Sources |= SourceSocket
				if rec.SocketDir == "" {
					rec.SocketDir = s.SockDir
				}
				rec.Anomalies = append(rec.Anomalies, s.Anomalies...)
				mergedSockets[s.SockDir] = true
				usedPIDs[pc.PID] = true
				break
			}
		}

		// Cwd-existence classification. Skip when we don't have a meaningful
		// path (e.g. argv truncated, no cwd at all).
		classifyCwd(fs, &rec)

		out = append(out, rec)
	}

	// 2. Emit standalone records for state candidates with no matching process.
	for _, sc := range stateC {
		if used[sc.Key] {
			continue
		}
		rec := BrokerRecord{
			Key:               sc.Key,
			StateDir:          sc.StateDir,
			HasBrokerJSON:     sc.HasBrokerJSON,
			BrokerJSONPID:     sc.BrokerJSON.PID,
			StateJSONReadable: sc.StateJSONReadable,
			JobCounts:         sc.JobCounts,
			LastJobUpdatedAt:  sc.LastJobUpdatedAt,
			Sources:           SourceStateDir,
			Anomalies: append([]Anomaly{
				{Code: AnomalyStateDirOrphan, Detail: fmt.Sprintf("state dir %s has broker.json (pid=%d) but no live process", sc.StateDir, sc.BrokerJSON.PID)},
			}, sc.Anomalies...),
		}
		out = append(out, rec)
	}

	// 3. Emit standalone records for socket candidates not merged into a process record.
	for _, s := range socketC {
		if mergedSockets[s.SockDir] {
			continue
		}
		key := s.SyntheticKey
		if key == "" {
			key = "unknown:" + synthSocketKey(s.SockDir)
		} else {
			// Mark as unknown unless a state dir resolved to the same key.
			key = "unknown:" + key
		}
		rec := BrokerRecord{
			Key:       key,
			SocketDir: s.SockDir,
			Sources:   SourceSocket,
			Anomalies: append([]Anomaly{
				{Code: AnomalySocketOrphan, Detail: fmt.Sprintf("socket dir %s has broker.pid=%d not in process scan", s.SockDir, s.PIDFromFile)},
			}, s.Anomalies...),
		}
		out = append(out, rec)
	}

	return out, topAnoms
}

// canonicalCwdForCollision returns the value used to detect broker_key_collision.
// We prefer CwdResolved; if missing (e.g. unresolvable), fall back to RawCwd.
func canonicalCwdForCollision(p *processCandidate) string {
	if p.CwdResolved != "" {
		return p.CwdResolved
	}
	return p.RawCwd
}

// classifyCwd populates rec.CwdExists / rec.CwdStatErr / cwd_* anomalies via
// fs.Stat. ENOENT → cwd_missing; everything else → cwd_transient_stat_error.
func classifyCwd(fs FS, rec *BrokerRecord) {
	target := rec.CwdResolved
	if target == "" {
		target = rec.Cwd
	}
	if target == "" {
		// No cwd to classify — leave CwdExists false, CwdStatErr empty.
		return
	}
	_, err := fs.Stat(target)
	if err == nil {
		rec.CwdExists = true
		return
	}
	rec.CwdExists = false
	switch {
	case errors.Is(err, os.ErrNotExist):
		rec.CwdStatErr = "ENOENT"
		rec.Anomalies = append(rec.Anomalies, Anomaly{
			Code:   AnomalyCwdMissing,
			Detail: fmt.Sprintf("os.Stat(%q): %s", target, err),
		})
	case errors.Is(err, os.ErrPermission):
		rec.CwdStatErr = "EACCES"
		rec.Anomalies = append(rec.Anomalies, Anomaly{
			Code:   AnomalyCwdTransientStatError,
			Detail: fmt.Sprintf("os.Stat(%q): %s", target, err),
		})
	default:
		rec.CwdStatErr = "other"
		rec.Anomalies = append(rec.Anomalies, Anomaly{
			Code:   AnomalyCwdTransientStatError,
			Detail: fmt.Sprintf("os.Stat(%q): %s", target, err),
		})
	}
}

// socketDirFromEndpoint extracts the socket directory from a `unix:<path>`
// endpoint, e.g. "unix:/var/folders/.../T/cxc-XXXX/broker.sock" →
// "/var/folders/.../T/cxc-XXXX". Returns "" when the endpoint is not the
// expected shape.
func socketDirFromEndpoint(ep string) string {
	const prefix = "unix:"
	if len(ep) <= len(prefix) || ep[:len(prefix)] != prefix {
		return ""
	}
	sock := ep[len(prefix):]
	for i := len(sock) - 1; i >= 0; i-- {
		if sock[i] == '/' {
			return sock[:i]
		}
	}
	return ""
}
