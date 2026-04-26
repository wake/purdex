package agent

import (
	"fmt"
	"log"
	"sort"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

// proxyMaxDepth caps the PPID ancestor walk during proxy subagent detection.
// 5 is enough to cover observed layouts like codex → codex-companion → cc
// (2 hops) with 3 hops of buffer for shell/tmux wrappers; beyond that we
// fall back to creating a new frame rather than paying unbounded syscall
// cost on a genuinely deep chain.
const proxyMaxDepth = 5

// proxyUpsertMaxAttempts caps optimistic-concurrency retries when two
// near-simultaneous proxy attaches (or detaches) race on the same parent
// frame's subagents list. Each attempt reloads the parent row, re-merges
// the ref through updateSubagents, and re-issues UpsertIfUnchanged. After
// exhausting attempts the caller surfaces an error — production scenarios
// that hit this limit are genuine hot loops, not ordinary concurrency.
const proxyUpsertMaxAttempts = 3

type FrameTraceMeta struct {
	FrameID       string
	ParentFrameID string
	Decision      string
	Reason        string
	Before        any
	After         any
	// MatchedAgentType is set on the daemon_restart_recovery path when
	// tryRebuildFromProcessTree confirms an alive agent in the pane process
	// tree. Empty otherwise. Carries the agent family that the live process
	// matched, which may differ from req.AgentType (the hook event's
	// declared agent_type) — divergence is the diagnostic signal Phase 3
	// rebuild path is meant to surface.
	//
	// PR #638 codex review round 2 #3 fix: previously the matched type was
	// dropped silently (`_ = matchedType`), making rebuild trace unable to
	// distinguish "rebuild confirmed hook owner" vs "rebuild only saw a
	// different agent in the same pane (e.g. cc parent of a codex hook)".
	MatchedAgentType string
}

func (m *Module) applyFrameEvent(req EventRequest, result agentpkg.DeriveResult, broadcastTs int64) (*SessionProjection, FrameTraceMeta, error) {
	if m.frames == nil {
		return nil, FrameTraceMeta{Decision: "skipped", Reason: "frame_store_unavailable", Before: map[string]any{}, After: map[string]any{}}, nil
	}
	if req.EventName != "SubagentStart" && req.EventName != "SubagentStop" && !result.Valid {
		projection, err := m.projectPane(req.TmuxPaneID)
		return projection, FrameTraceMeta{Decision: "skipped", Reason: "derive_invalid", Before: map[string]any{}, After: map[string]any{}}, err
	}

	frame, err := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
	if err != nil {
		return nil, FrameTraceMeta{}, err
	}
	before := summarizeFrame(frame)

	switch req.EventName {
	case "SessionEnd":
		if frame != nil {
			// Phase 3.5 §2.3 (v8 L1 fix, codex round PR-2 attack):
			// detach-first ordering. The previous delete-first +
			// best-effort detach left a permanent orphan whenever
			// removeProxyRefForSender failed (storage error, retry
			// exhaustion, daemon crash mid-handler) — the child row
			// was gone so projection_dedup could no longer hide the
			// ancestor's stale proxy ref, and PR-3.5a ships without
			// pruneDeadProxyRefs. By detaching first and propagating
			// the error before Delete, a hook handler failure leaves
			// the DB in a recoverable state (child row + parent ref
			// both still present; sweep canonicalize / next
			// SessionEnd retry can fix it).
			if _, _, _, _, derr := m.removeProxyRefForSender(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, broadcastTs); derr != nil {
				return nil, FrameTraceMeta{}, derr
			}
			if err := m.frames.Delete(frame.FrameID); err != nil {
				return nil, FrameTraceMeta{}, err
			}
			projection, err := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				FrameID:       frame.FrameID,
				ParentFrameID: frame.ParentFrameID,
				Decision:      "deleted_frame",
				Reason:        "session_end",
				Before:        before,
				After:         map[string]any{},
			}, err
		}
		// frame == nil: sender has no frame of its own. This is either a
		// genuine orphan SessionEnd, or the SessionEnd of a process that was
		// previously proxy-attached to another frame (Phase 2 PR-2b §1.5).
		// Probe the pane's frames for a matching proxy ref and detach it.
		removed, parentFrame, parentBefore, parentAfter, err := m.removeProxyRefForSender(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, broadcastTs)
		if err != nil {
			return nil, FrameTraceMeta{}, err
		}
		if removed {
			projection, perr := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				FrameID:       parentFrame.FrameID,
				ParentFrameID: parentFrame.ParentFrameID,
				Decision:      "updated_frame",
				Reason:        "proxy_subagent_detached",
				Before:        parentBefore,
				After:         parentAfter,
			}, perr
		}
		projection, err := m.projectPane(req.TmuxPaneID)
		return projection, FrameTraceMeta{
			Decision: "skipped",
			Reason:   "session_end_without_frame",
			Before:   before,
			After:    map[string]any{},
		}, err
	case "SubagentStart", "SubagentStop":
		if frame == nil {
			projection, err := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				Decision: "skipped",
				Reason:   "frame_missing",
				Before:   before,
				After:    map[string]any{},
			}, err
		}
		agentID, _ := result.Detail["agent_id"].(string)
		if agentID == "" {
			projection, err := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				FrameID:       frame.FrameID,
				ParentFrameID: frame.ParentFrameID,
				Decision:      "skipped",
				Reason:        "subagent_id_missing",
				Before:        before,
				After:         before,
			}, err
		}
		ref := agentpkg.SubagentRef{
			ID: agentID,
			// Type is the canonical agent family that owns this subagent (cc /
			// codex / opencode), not the payload's per-subagent sub-variant
			// (e.g. opencode's `agent_type: "Explore"`). Keeping Type aligned
			// to frame.AgentType lets the SPA use it for agent-family color
			// lookup without provider-specific special cases.
			Type:      frame.AgentType,
			StartedAt: broadcastTs,
			// SourcePID / SourceStartTime / IsProxy left zero: native SubagentStart
			// refs have no distinct source process identity. Proxy attaches (PR-2b)
			// set these explicitly.
		}
		// Optimistic-concurrency native mutation (#632 continuation):
		// two cc SubagentStarts from a concurrent Task-tool dispatch can
		// land in near-simultaneously; the same read-modify-write race
		// that motivated the proxy atomic fix applies here. Delegate to
		// the shared helper that reloads + re-merges on UpsertIfUnchanged
		// conflict.
		applied, stored, merr := m.mutateSubagentsWithRetry(*frame, req.EventName, ref, broadcastTs)
		if merr != nil {
			return nil, FrameTraceMeta{}, merr
		}
		if !applied {
			// Frame was deleted mid-flight (concurrent sweep / SessionEnd).
			// Report frame_missing so downstream treats this like the
			// frame == nil branch above.
			projection, perr := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				Decision: "skipped",
				Reason:   "frame_missing",
				Before:   before,
				After:    map[string]any{},
			}, perr
		}
		projection, err := m.projectPane(req.TmuxPaneID)
		return projection, FrameTraceMeta{
			FrameID:       stored.FrameID,
			ParentFrameID: stored.ParentFrameID,
			Decision:      "updated_frame",
			Reason:        "subagent_membership_changed",
			Before:        before,
			After:         summarizeFrame(&stored),
		}, err
	}

	// Proxy subagent fast-path (Phase 2 PR-2b, plan §1.4): when a SessionStart
	// arrives from a sender that has no existing frame of its own and a PPID
	// ancestor walk locates an alive cross-type parent in the same pane, we
	// collapse the event into a proxy ref attached to that parent rather than
	// creating a standalone frame. Observed in practice for codex spawned from
	// inside a cc session via codex-companion: cc owns the UX, codex should
	// show as a dot on cc's tab, not as a separate lit-up frame.
	if req.EventName == "SessionStart" && frame == nil {
		parent, perr := m.findProxyParent(req)
		if perr != nil {
			return nil, FrameTraceMeta{}, perr
		}
		if parent != nil {
			parentBefore := summarizeFrame(parent)
			ref := agentpkg.SubagentRef{
				ID:              fmt.Sprintf("proxy:%s:%d:%s", req.AgentType, req.SenderPID, req.SenderStartTime),
				Type:            req.AgentType,
				StartedAt:       broadcastTs,
				SourcePID:       req.SenderPID,
				SourceStartTime: req.SenderStartTime,
				IsProxy:         true,
			}
			// Optimistic-concurrency attach: read-modify-write on the parent
			// row is racy when two proxy SessionStarts land on the same
			// parent simultaneously (issue #632). UpsertIfUnchanged +
			// reload-on-conflict serializes the writes without a lock; the
			// retry loop merges both refs before persistence.
			attached, stored, aerr := m.attachProxyRefWithRetry(*parent, ref, broadcastTs)
			if aerr != nil {
				return nil, FrameTraceMeta{}, aerr
			}
			if attached {
				projection, err := m.projectPane(req.TmuxPaneID)
				return projection, FrameTraceMeta{
					FrameID:       stored.FrameID,
					ParentFrameID: stored.ParentFrameID,
					Decision:      "updated_frame",
					Reason:        "proxy_subagent_attached",
					Before:        parentBefore,
					After:         summarizeFrame(&stored),
				}, err
			}
			// Parent vanished mid-flight (swept or deleted by concurrent
			// SessionEnd). Fall through to the legacy create-new-frame path
			// so the sender still gets represented somewhere.
		}
	}

	info, err := readProcessInfoFn(req.SenderPID)
	if err != nil {
		return nil, FrameTraceMeta{}, err
	}

	startedAt := broadcastTs
	parentFrameID := ""
	if frame != nil {
		startedAt = frame.StartedAt
		parentFrameID = frame.ParentFrameID
	}
	if parentFrameID == "" {
		parent, err := m.frames.FindByPanePID(req.TmuxPaneID, info.PPID)
		if err != nil {
			return nil, FrameTraceMeta{}, err
		}
		if parent != nil && (frame == nil || parent.FrameID != frame.FrameID) {
			parentFrameID = parent.FrameID
		}
	}

	// Phase 3 Commit 3 — daemon-restart recovery (plan §1.2.4 / §1.4):
	// When the standard lookup chain (GetByIdentity → findProxyParent →
	// FindByPanePID) all miss on a hook event, fall back to inspecting the
	// pane's live process tree via the Prober. A hit means "some agent we
	// know is alive somewhere under this pane" — enough to recover frame
	// state lost to a daemon restart without trusting the hook event blindly
	// (the hook's AgentType remains the SOT; rebuild only marks the trace
	// reason so the Inspector can distinguish recovered frames from fresh
	// ones).
	//
	// Fail-soft: a probe error must not abort the hook. Log and fall through
	// to the降階 no-parent path (reason="no_parent_fallback", see plan §1.4).
	rebuiltMatched := false
	rebuiltAgentType := ""
	if frame == nil && parentFrameID == "" {
		matchedType, ok, rerr := m.tryRebuildFromProcessTree(req)
		if rerr != nil {
			log.Printf("[agent] rebuild_from_process_tree_failed: pane=%s err=%v", req.TmuxPaneID, rerr)
		}
		if ok {
			rebuiltMatched = true
			// req.AgentType remains the SOT for frame.AgentType (the hook
			// event is authoritative). matchedType is preserved separately
			// for trace diagnostics — divergence (e.g. cc pane with codex
			// hook) is the signal Inspector / Phase 5 reparent uses to
			// triage suspected proxy collapse failures. (PR #638 codex
			// review round 2 #3 fix.)
			rebuiltAgentType = matchedType
		}
	}

	status := result.Status
	if status == "" && frame != nil {
		status = frame.Status
	}
	if status == "" {
		status = agentpkg.StatusIdle
	}

	var stored store.Frame
	if frame != nil {
		// Existing frame: narrow column update (#632 R8). Writing the full
		// frame would round-trip a stale Subagents baseline and clobber
		// concurrent proxy/native attach/detach mutations on the same row.
		// Subagents changes flow through mutateSubagentsWithRetry /
		// attachProxyRefWithRetry / removeProxyRefForSender, not here.
		//
		// Exception: SessionStart on an existing frame must prune any
		// stale IsProxy refs (source process dead or PID-reused) while
		// preserving everything else — including concurrently-attached
		// native SubagentStart refs (IsProxy=false) that landed between
		// attempts. Phase 3.5 plan §2.2.2 (v6 J1, v8 M3) replaces the
		// old "snapshot → reset → re-attach" three-step pattern (race
		// window between steps could lose a concurrently-attached
		// proxy) with a single filter-merge-retry: each attempt
		// re-reads frame.Subagents (so any concurrent attach is
		// naturally preserved), prunes stale IsProxy via the live
		// identity gate, and writes via UpsertIfUnchanged. A conflict
		// reload picks up the newer ref before the next prune pass.
		if req.EventName == "SessionStart" {
			var success bool
			// initialNativeIDs is the BASELINE native ref ID set captured
			// before the retry loop. SessionStart's reset semantic drops
			// the old session's native subagent state, which the cc
			// hook saw at attempt 0 entry. Any native ref appearing in
			// the reloaded baseline on a later retry that is NOT in
			// initialNativeIDs is — by construction — a concurrent
			// SubagentStart that landed AFTER our baseline snapshot
			// (post our SessionStart). Such refs belong to the new
			// session and must be preserved across all retries.
			//
			// Codex round 2 #O1 fix: the previous prevWrittenNativeIDs
			// approach regressed across multiple conflicts — a native
			// preserved at attempt 1 was recorded in prevWrittenNativeIDs
			// itself, so attempt 2 dropped it as a "carried-forward
			// baseline native". Snapshotting an immutable baseline
			// before the loop sidesteps the regression entirely.
			initialNativeIDs := make(map[string]struct{}, len(frame.Subagents))
			for _, ref := range frame.Subagents {
				if !ref.IsProxy {
					initialNativeIDs[ref.ID] = struct{}{}
				}
			}
			for attempt := 0; attempt < proxyUpsertMaxAttempts; attempt++ {
				pruned := make([]agentpkg.SubagentRef, 0, len(frame.Subagents))
				for _, ref := range frame.Subagents {
					if ref.IsProxy {
						// IsProxy refs survive SessionStart reset when
						// their source process is alive + identity
						// matches (PR-2b proxy-collapse semantics).
						if !isPidAliveFn(ref.SourcePID) {
							continue
						}
						actualStart, sterr := processStartTimeFn(ref.SourcePID)
						if sterr != nil || actualStart != ref.SourceStartTime {
							continue
						}
						pruned = append(pruned, ref)
						continue
					}
					// Native ref: drop if it was in the initial
					// baseline (SessionStart reset semantic for old
					// session refs). Otherwise (appeared after baseline
					// snapshot via concurrent SubagentStart) preserve.
					if _, baseline := initialNativeIDs[ref.ID]; !baseline {
						pruned = append(pruned, ref)
					}
				}

				candidate := *frame
				candidate.AgentType = req.AgentType
				candidate.PPID = info.PPID
				candidate.ParentFrameID = parentFrameID
				candidate.Status = status
				candidate.LastSeenAt = broadcastTs
				candidate.Verified = true
				candidate.Subagents = pruned

				ok, written, uerr := m.frames.UpsertIfUnchanged(candidate, frame.LastSeenAt)
				if uerr != nil {
					return nil, FrameTraceMeta{}, uerr
				}
				if ok {
					stored = written
					success = true
					break
				}
				// Conflict — concurrent writer touched the row (proxy
				// attach, SubagentStart hook, probe status update).
				// Reload and retry; initialNativeIDs is UNCHANGED across
				// retries, so any native ID appearing in the reloaded
				// baseline that is not in initialNativeIDs is a true
				// concurrent attach and gets preserved by the next
				// filter pass.
				reloaded, rerr := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
				if rerr != nil {
					return nil, FrameTraceMeta{}, rerr
				}
				if reloaded == nil {
					// Frame deleted mid-flight. Treat as frame_missing.
					projection, perr := m.projectPane(req.TmuxPaneID)
					return projection, FrameTraceMeta{
						Decision: "skipped",
						Reason:   "frame_missing",
						Before:   before,
						After:    map[string]any{},
					}, perr
				}
				frame = reloaded
			}
			if !success {
				return nil, FrameTraceMeta{}, fmt.Errorf("session_start filter-merge: exceeded %d retries for frame %s", proxyUpsertMaxAttempts, frame.FrameID)
			}
		} else {
			// Non-SessionStart existing-frame path: original narrow
			// column update (#632 R8 — don't round-trip subagents).
			updated := *frame
			updated.AgentType = req.AgentType
			updated.PPID = info.PPID
			updated.ParentFrameID = parentFrameID
			updated.Status = status
			updated.LastSeenAt = broadcastTs
			updated.Verified = true
			if err := m.frames.UpdateHookPath(updated); err != nil {
				return nil, FrameTraceMeta{}, err
			}
			reloaded, rerr := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
			if rerr != nil {
				return nil, FrameTraceMeta{}, rerr
			}
			if reloaded == nil {
				return nil, FrameTraceMeta{}, nil
			}
			stored = *reloaded
		}

		// Phase 3.5 §2.2.2 — descendant scan after successful filter-
		// merge. Catches any cold-start race where a child's
		// SessionStart raced this existing-frame's SessionStart and
		// landed standalone instead of being collapsed via PR-2b's
		// pre-Upsert proxy fast-path.
		if req.EventName == "SessionStart" {
			updated, cerr := m.canonicalizeDescendantsAfterUpsert(stored, broadcastTs)
			if cerr != nil {
				return nil, FrameTraceMeta{}, cerr
			}
			stored = updated
		}
	} else {
		// New frame: insert via Upsert. No existing subagents to clobber;
		// start the row with an empty list.
		stored, err = m.frames.Upsert(store.Frame{
			PaneID:           req.TmuxPaneID,
			AgentType:        req.AgentType,
			PID:              req.SenderPID,
			PPID:             info.PPID,
			ProcessStartTime: req.SenderStartTime,
			ParentFrameID:    parentFrameID,
			Subagents:        []agentpkg.SubagentRef{},
			Status:           status,
			StartedAt:        startedAt,
			LastSeenAt:       broadcastTs,
			Verified:         true,
		})
		if err != nil {
			return nil, FrameTraceMeta{}, err
		}

		// Phase 3.5 §2.2.1 — bidirectional canonicalization (best-effort).
		// On a SessionStart we just created a standalone frame for, try
		// once more (post-Upsert) to fold either:
		//   (a) self into an alive cross-type ancestor (reconcile, plan
		//       §2.1.1), or
		//   (b) any standalone cross-type descendants whose SessionStart
		//       raced ours and landed before our PPID-walk could see us
		//       as an ancestor (descendant scan, plan §2.1.2).
		if req.EventName == "SessionStart" {
			canonicalized, parentStored, rerr := m.reconcileCreatedFrameAsProxy(stored, req, broadcastTs)
			if rerr != nil {
				return nil, FrameTraceMeta{}, rerr
			}
			if canonicalized {
				projection, perr := m.projectPane(req.TmuxPaneID)
				return projection, FrameTraceMeta{
					FrameID:       parentStored.FrameID,
					ParentFrameID: parentStored.ParentFrameID,
					Decision:      "updated_frame",
					Reason:        "post_upsert_canonicalization_self",
					Before:        before,
					After:         summarizeFrame(&parentStored),
				}, perr
			}
			// self stays standalone (or partial); try descendant scan.
			updated, cerr := m.canonicalizeDescendantsAfterUpsert(stored, broadcastTs)
			if cerr != nil {
				return nil, FrameTraceMeta{}, cerr
			}
			stored = updated
		}
	}
	projection, err := m.projectPane(req.TmuxPaneID)
	// Phase 3 — three-state reason (plan §1.4):
	//   parent_frame_found       → legacy lookup hit (line 220-228)
	//   daemon_restart_recovery  → rebuild via process tree hit (commit 3)
	//   no_parent_fallback       → all lookups + rebuild missed; frame still
	//                              created using req.AgentType as SOT (commit 4).
	// The Inspector uses these to distinguish recovered frames from降階 ones.
	reason := "no_parent_fallback"
	if stored.ParentFrameID != "" {
		reason = "parent_frame_found"
	} else if rebuiltMatched {
		reason = "daemon_restart_recovery"
	}
	decision := "created_frame"
	if frame != nil {
		decision = "updated_frame"
	}
	return projection, FrameTraceMeta{
		FrameID:          stored.FrameID,
		ParentFrameID:    stored.ParentFrameID,
		Decision:         decision,
		Reason:           reason,
		Before:           before,
		After:            summarizeFrame(&stored),
		MatchedAgentType: rebuiltAgentType,
	}, err
}

func (m *Module) projectPane(paneID string) (*SessionProjection, error) {
	if m.frames == nil {
		return nil, nil
	}
	frames, err := m.frames.ListByPane(paneID)
	if err != nil {
		return nil, err
	}
	projection := buildPaneProjection(paneID, frames)
	return &projection, nil
}

func frameID(frame *store.Frame) string {
	if frame == nil {
		return ""
	}
	return frame.FrameID
}

func summarizeFrame(frame *store.Frame) map[string]any {
	if frame == nil {
		return map[string]any{}
	}
	return map[string]any{
		"frame_id":         frame.FrameID,
		"pane_id":          frame.PaneID,
		"agent_type":       frame.AgentType,
		"pid":              frame.PID,
		"ppid":             frame.PPID,
		"parent_frame_id":  frame.ParentFrameID,
		"process_start_at": frame.ProcessStartTime,
		"status":           string(frame.Status),
		"subagents":        append([]agentpkg.SubagentRef(nil), frame.Subagents...),
	}
}

// updateSubagents mutates a frame's subagent list in response to a
// SubagentStart / SubagentStop event. On SubagentStart the first-write wins;
// an existing ref keeps its StartedAt/SourcePID/SourceStartTime/IsProxy.
//
// Identity key is kind-aware (R2 fix): proxy refs identify by
// (SourcePID, SourceStartTime) — the sender process — while native refs
// identify by ID (the agent_id string supplied by the provider). Cross-kind
// refs (one proxy, one native) never match even if ID strings coincide.
// This isolates namespaces so a native ref whose agent_id happens to collide
// with a synthesized proxy ID cannot shadow or evict a proxy ref, and vice
// versa.
func updateSubagents(current []agentpkg.SubagentRef, eventName string, ref agentpkg.SubagentRef) []agentpkg.SubagentRef {
	if current == nil {
		current = []agentpkg.SubagentRef{}
	}
	switch eventName {
	case "SubagentStart":
		for _, existing := range current {
			if subagentRefMatches(existing, ref) {
				return current
			}
		}
		return append(current, ref)
	case "SubagentStop":
		filtered := make([]agentpkg.SubagentRef, 0, len(current))
		for _, existing := range current {
			if !subagentRefMatches(existing, ref) {
				filtered = append(filtered, existing)
			}
		}
		return filtered
	default:
		return current
	}
}

// subagentRefMatches returns true when two refs identify the same subagent.
// Proxy refs compare by (SourcePID, SourceStartTime); native refs compare by
// ID. Cross-kind (one proxy, one native) is never a match — preserves the
// isolation between the two namespaces (see updateSubagents doc).
func subagentRefMatches(a, b agentpkg.SubagentRef) bool {
	if a.IsProxy != b.IsProxy {
		return false
	}
	if a.IsProxy {
		return a.SourcePID == b.SourcePID && a.SourceStartTime == b.SourceStartTime
	}
	return a.ID == b.ID
}

func syncProjectionState(currentStatus map[string]agentpkg.Status, subagents map[string][]agentpkg.SubagentRef, tmuxSession string, projection *SessionProjection) {
	if projection == nil || projection.TopFrame == nil {
		delete(currentStatus, tmuxSession)
		delete(subagents, tmuxSession)
		return
	}
	currentStatus[tmuxSession] = projection.TopFrame.Status
	subagents[tmuxSession] = append([]agentpkg.SubagentRef(nil), projection.Subagents...)
}

func buildProjectionNormalized(projection *SessionProjection, fallbackAgentType, eventName string, broadcastTs int64, result agentpkg.DeriveResult) agentpkg.NormalizedEvent {
	normalized := agentpkg.NormalizedEvent{
		AgentType:    fallbackAgentType,
		Status:       string(result.Status),
		Model:        result.Model,
		Subagents:    []agentpkg.SubagentRef{},
		RawEventName: eventName,
		BroadcastTs:  broadcastTs,
		Detail:       result.Detail,
	}
	if projection == nil {
		return normalized
	}
	normalized.Subagents = append([]agentpkg.SubagentRef(nil), projection.Subagents...)
	if projection.TopFrame == nil {
		normalized.Status = string(agentpkg.StatusClear)
		return normalized
	}
	normalized.AgentType = projection.TopFrame.AgentType
	normalized.Status = string(projection.TopFrame.Status)
	return normalized
}

// strFromDetail looks up a string field in a DeriveResult.Detail map.
// Returns "" when the key is missing or the value is not a string.
func strFromDetail(detail map[string]any, key string) string {
	if detail == nil {
		return ""
	}
	if v, ok := detail[key].(string); ok {
		return v
	}
	return ""
}

func (m *Module) projectionForSession(sessionName string) (*SessionProjection, error) {
	projections, err := m.liveFrameProjections()
	if err != nil {
		return nil, err
	}
	return m.selectSessionProjection(sessionName, projections), nil
}

func (m *Module) setProjectionTopStatus(sessionName string, status agentpkg.Status) (*SessionProjection, error) {
	projection, err := m.projectionForSession(sessionName)
	if err != nil || projection == nil || projection.TopFrame == nil {
		return projection, err
	}
	// Narrow update only status + last_seen_at (#632 R7): a whole-frame
	// Upsert from this path would round-trip a stale Subagents baseline
	// and clobber concurrent proxy/native attach/detach mutations on the
	// same row. Probe-driven status transitions have no business changing
	// Subagents — decouple the columns at the SQL layer.
	if err := m.frames.UpdateStatusAndLastSeen(projection.TopFrame.FrameID, status, time.Now().UnixNano()); err != nil {
		return nil, err
	}
	return m.projectionForSession(sessionName)
}

func (m *Module) selectSessionProjection(sessionName string, projections []SessionProjection) *SessionProjection {
	var selected *SessionProjection
	for i := range projections {
		name, _ := m.resolvePaneSession(projections[i].PaneID)
		if name != sessionName {
			continue
		}
		if selected == nil || projectionSortGreater(projections[i], *selected) {
			projection := projections[i]
			selected = &projection
		}
	}
	return selected
}

type namedProjection struct {
	SessionName string
	SessionCode string
	Projection  SessionProjection
}

func (m *Module) liveSessionProjections() ([]namedProjection, error) {
	projections, err := m.liveFrameProjections()
	if err != nil {
		return nil, err
	}
	if len(projections) == 0 {
		return nil, nil
	}
	selected := make(map[string]namedProjection)
	for _, projection := range projections {
		sessionName, sessionCode := m.resolvePaneSession(projection.PaneID)
		if sessionName == "" {
			continue
		}
		current, ok := selected[sessionName]
		if !ok || projectionSortGreater(projection, current.Projection) {
			selected[sessionName] = namedProjection{
				SessionName: sessionName,
				SessionCode: sessionCode,
				Projection:  projection,
			}
		}
	}
	sessionNames := make([]string, 0, len(selected))
	for sessionName := range selected {
		sessionNames = append(sessionNames, sessionName)
	}
	sort.Strings(sessionNames)
	out := make([]namedProjection, 0, len(sessionNames))
	for _, sessionName := range sessionNames {
		out = append(out, selected[sessionName])
	}
	return out, nil
}

func projectionSortGreater(candidate, current SessionProjection) bool {
	if candidate.TopFrame == nil {
		return false
	}
	if current.TopFrame == nil {
		return true
	}
	if candidate.TopFrame.StartedAt != current.TopFrame.StartedAt {
		return candidate.TopFrame.StartedAt > current.TopFrame.StartedAt
	}
	return candidate.TopFrame.FrameID > current.TopFrame.FrameID
}

// removeProxyRefForSender scans the pane's frames for a proxy SubagentRef
// whose SourcePID+SourceStartTime match the sender that is emitting SessionEnd,
// filters it out, refreshes the owning frame's LastSeenAt and persists via
// Upsert. Matching is by identity fields (SourcePID+SourceStartTime), not by
// ref.ID string, so the detach remains robust across potential future ID
// format changes.
//
// Returns (removed, ownerFrameAfter, ownerBefore, ownerAfter, err):
//   - removed=true with ownerFrameAfter populated when a ref was detached.
//   - removed=false, zeroFrame, nil, nil, nil when nothing matched.
func (m *Module) removeProxyRefForSender(paneID string, senderPID int, senderStartTime string, broadcastTs int64) (bool, store.Frame, any, any, error) {
	if m.frames == nil {
		return false, store.Frame{}, nil, nil, nil
	}
	frames, err := m.frames.ListByPane(paneID)
	if err != nil {
		return false, store.Frame{}, nil, nil, err
	}
	for _, frame := range frames {
		if !subagentsContainProxySender(frame.Subagents, senderPID, senderStartTime) {
			continue
		}
		before := summarizeFrame(&frame)
		// Optimistic-concurrency detach: see attachProxyRefWithRetry doc.
		// #632 — the scan/list/update triplet is racy if two SessionEnds (or
		// a SessionEnd concurrent with a SessionStart) land on the same
		// parent. Reload + filter + UpsertIfUnchanged with retry.
		detached, stored, derr := m.detachProxyRefWithRetry(frame, senderPID, senderStartTime, broadcastTs)
		if derr != nil {
			return false, store.Frame{}, nil, nil, derr
		}
		if detached {
			return true, stored, before, summarizeFrame(&stored), nil
		}
		// Frame vanished or its ref was already removed by a concurrent
		// writer. Continue scanning other frames in the pane — a different
		// owner may still carry our sender's proxy ref.
	}
	return false, store.Frame{}, nil, nil, nil
}

// subagentsContainProxySender is a side-effect-free check used to short-
// circuit frames that don't need the read-modify-write retry loop.
func subagentsContainProxySender(refs []agentpkg.SubagentRef, senderPID int, senderStartTime string) bool {
	for _, ref := range refs {
		if ref.IsProxy && ref.SourcePID == senderPID && ref.SourceStartTime == senderStartTime {
			return true
		}
	}
	return false
}

// mutateSubagentsWithRetry applies an updateSubagents mutation
// (SubagentStart add or SubagentStop remove) to frame.Subagents under
// optimistic concurrency. Each attempt re-merges via updateSubagents on top
// of the current baseline and issues UpsertIfUnchanged; on conflict it
// reloads via GetByIdentity and retries, bounded by proxyUpsertMaxAttempts.
//
// Returns (true, stored, nil) when the mutation is persisted;
// (false, zeroFrame, nil) when the frame was deleted mid-flight (caller
// decides whether to fall back, continue scanning, or report frame_missing).
// A non-nil error is only returned for storage failures or exhausted retries.
//
// Serves both native SubagentStart/Stop (from hook events) and proxy
// attach (via attachProxyRefWithRetry). All three paths share the same
// subagents_json read-modify-write cycle and would race without this helper.
func (m *Module) mutateSubagentsWithRetry(frame store.Frame, eventName string, ref agentpkg.SubagentRef, broadcastTs int64) (bool, store.Frame, error) {
	current := frame
	for attempt := 0; attempt < proxyUpsertMaxAttempts; attempt++ {
		expected := current.LastSeenAt
		current.Subagents = updateSubagents(current.Subagents, eventName, ref)
		current.LastSeenAt = broadcastTs
		ok, stored, err := m.frames.UpsertIfUnchanged(current, expected)
		if err != nil {
			return false, store.Frame{}, err
		}
		if ok {
			return true, stored, nil
		}
		reloaded, err := m.frames.GetByIdentity(frame.PaneID, frame.PID, frame.ProcessStartTime)
		if err != nil {
			return false, store.Frame{}, err
		}
		if reloaded == nil {
			return false, store.Frame{}, nil
		}
		current = *reloaded
	}
	return false, store.Frame{}, fmt.Errorf("subagent mutation %s: exceeded %d retries for frame %s", eventName, proxyUpsertMaxAttempts, frame.FrameID)
}

// attachProxyRefWithRetry is a thin wrapper over mutateSubagentsWithRetry
// specialized for the proxy-attach path. Kept as a separate helper so the
// caller at applyFrameEvent reads as "attach proxy ref" rather than
// "SubagentStart mutation on the parent".
func (m *Module) attachProxyRefWithRetry(parent store.Frame, ref agentpkg.SubagentRef, broadcastTs int64) (bool, store.Frame, error) {
	return m.mutateSubagentsWithRetry(parent, "SubagentStart", ref, broadcastTs)
}

// detachProxyRefWithRetry mirrors attachProxyRefWithRetry for the SessionEnd
// cleanup path: reload parent, filter out the matching proxy ref, persist
// via UpsertIfUnchanged, retry on conflict. Returns (false, zeroFrame, nil)
// when the frame was already gone or its ref was already cleaned up by a
// concurrent writer (caller continues scanning other frames in the pane).
func (m *Module) detachProxyRefWithRetry(owner store.Frame, senderPID int, senderStartTime string, broadcastTs int64) (bool, store.Frame, error) {
	current := owner
	for attempt := 0; attempt < proxyUpsertMaxAttempts; attempt++ {
		hit := -1
		for i, ref := range current.Subagents {
			if ref.IsProxy && ref.SourcePID == senderPID && ref.SourceStartTime == senderStartTime {
				hit = i
				break
			}
		}
		if hit < 0 {
			// Ref already removed by a concurrent writer.
			return false, store.Frame{}, nil
		}
		expected := current.LastSeenAt
		filtered := make([]agentpkg.SubagentRef, 0, len(current.Subagents)-1)
		filtered = append(filtered, current.Subagents[:hit]...)
		filtered = append(filtered, current.Subagents[hit+1:]...)
		current.Subagents = filtered
		current.LastSeenAt = broadcastTs
		ok, stored, err := m.frames.UpsertIfUnchanged(current, expected)
		if err != nil {
			return false, store.Frame{}, err
		}
		if ok {
			return true, stored, nil
		}
		reloaded, err := m.frames.GetByIdentity(owner.PaneID, owner.PID, owner.ProcessStartTime)
		if err != nil {
			return false, store.Frame{}, err
		}
		if reloaded == nil {
			return false, store.Frame{}, nil
		}
		current = *reloaded
	}
	return false, store.Frame{}, fmt.Errorf("proxy detach: exceeded %d retries for frame %s", proxyUpsertMaxAttempts, owner.FrameID)
}

// firstAliveAgentInTreeFn is the test seam that indirects
// Prober.FirstAliveAgentInTree so tryRebuildFromProcessTree can be exercised
// without a wired-up prober (m.prober is nil in newTestModule). Mirrors the
// pattern of readProcessInfoFn / isPidAliveFn / processStartTimeFn declared in
// verify.go — unit tests override the package-level variable and restore it
// via t.Cleanup.
var firstAliveAgentInTreeFn = func(m *Module, target string) (string, int, error) {
	if m == nil || m.prober == nil {
		return "", 0, nil
	}
	return m.prober.FirstAliveAgentInTree(target)
}

// tryRebuildFromProcessTree attempts to recover a frame after daemon restart
// by inspecting the pane's live process tree via the Prober. Triggered when
// the standard lookup chain (GetByIdentity → findProxyParent → FindByPanePID)
// all miss on a SessionStart (Phase 3 plan §1.2.2).
//
// Behavior:
//   - Delegates to prober.FirstAliveAgentInTree(req.TmuxPaneID)
//   - Match → returns (agentType, true, nil)
//   - No match → returns ("", false, nil)
//   - Any error → returns ("", false, err); caller is expected to fail-soft
//
// Unwired in Commit 2: applyFrameEvent does not yet call this helper — that
// wiring lands in Commit 3, which also introduces the reason_code contract
// for the rebuild path.
func (m *Module) tryRebuildFromProcessTree(req EventRequest) (string, bool, error) {
	agentType, _, err := firstAliveAgentInTreeFn(m, req.TmuxPaneID)
	if err != nil {
		return "", false, err
	}
	if agentType == "" {
		return "", false, nil
	}
	return agentType, true, nil
}

// reconcileCreatedFrameAsProxy attempts to canonicalize a freshly-created
// SessionStart frame against an alive cross-type ancestor. If
// findProxyParent locates a candidate, attaches a proxy ref to the
// ancestor + best-effort deletes self's standalone row.
//
// Best-effort delete: if DeleteIfUnchanged fails because a concurrent
// writer touched the child row (sweep tick, SubagentStart hook, etc.),
// MetricPartialCanonicalizationCreated +1 and we return — sweep
// canonicalize will retry within sweepInterval=2s and projection-layer
// dedup hides the partial state from SPA in the meantime. Plan §2.1.1.
//
// Returns:
//
//	(true, parentStored, nil)  — attach + delete both succeeded; caller
//	                             should project the pane and emit a
//	                             post_upsert_canonicalization_self trace.
//	(false, zero, nil)         — no ancestor / parent vanished
//	                             mid-attach / partial state (attach
//	                             succeeded, delete failed; counted in
//	                             metrics, repaired by sweep).
//	(false, zero, err)         — storage failure; caller propagates.
//
// Unlike v2/v3 of this design there is no rollback path: partial state
// is treated as a legal transient (see plan §0.2 evidence points).
func (m *Module) reconcileCreatedFrameAsProxy(stored store.Frame, req EventRequest, broadcastTs int64) (bool, store.Frame, error) {
	parent, err := m.findProxyParent(req)
	if err != nil {
		return false, store.Frame{}, err
	}
	if parent == nil {
		return false, store.Frame{}, nil
	}
	ref := agentpkg.SubagentRef{
		ID:              fmt.Sprintf("proxy:%s:%d:%s", req.AgentType, req.SenderPID, req.SenderStartTime),
		Type:            req.AgentType,
		StartedAt:       broadcastTs,
		SourcePID:       req.SenderPID,
		SourceStartTime: req.SenderStartTime,
		IsProxy:         true,
	}
	attached, parentStored, aerr := m.attachProxyRefWithRetry(*parent, ref, broadcastTs)
	if aerr != nil {
		return false, store.Frame{}, aerr
	}
	if !attached {
		// Parent vanished mid-flight (concurrent SessionEnd / sweep).
		// Caller falls back to leaving self standalone.
		return false, store.Frame{}, nil
	}
	deleted, derr := m.frames.DeleteIfUnchanged(stored.FrameID, stored.LastSeenAt)
	if derr != nil {
		return false, store.Frame{}, derr
	}
	if !deleted {
		// Partial state: parent has the proxy ref, self still
		// standalone. Acceptable transient — projection dedup hides
		// the orphan child from SPA, sweep canonicalize repairs
		// within 2s. v8 M4 fix (codex round PR-2 attack): we still
		// report canonicalized=true here so the caller emits an
		// updated_frame / post_upsert_canonicalization_self trace
		// that matches what the SPA actually sees (parent + proxy
		// ref). Reporting canonicalized=false would have routed the
		// caller to the standalone created_frame trace, diverging
		// from projection (trace ≠ projection). Metric still +1 to
		// surface partial-state rate via expvar.
		agentpkg.MetricPartialCanonicalizationCreated.Add(1)
		log.Printf("phase3.5: reconcile partial state child %s parent %s (sweep will repair within 2s)", stored.FrameID, parentStored.FrameID)
	}
	return true, parentStored, nil
}

// pidIsAncestorOfWithCap walks descendantPID's PPID chain (capped at
// maxDepth) looking for ancestorPID. Returns true on hit; false on miss,
// depth exhaustion, readProcessInfoFn error, or self-loop (info.PPID ==
// info.PID).
//
// Used by canonicalizeDescendantsAfterUpsert to confirm a candidate
// descendant frame's process actually descends from self.PID before
// folding it into a proxy ref. Mirrors findProxyParent's PPID walk
// semantics but starts from descendant↑ rather than self↑.
//
// Phase 3.5 plan §2.1.2.
func pidIsAncestorOfWithCap(descendantPID, ancestorPID, maxDepth int) bool {
	current := descendantPID
	for depth := 0; depth < maxDepth; depth++ {
		info, err := readProcessInfoFn(current)
		if err != nil {
			return false
		}
		if info.PPID == ancestorPID {
			return true
		}
		if info.PPID <= 1 || info.PPID == current {
			return false
		}
		current = info.PPID
	}
	return false
}

// canonicalizeDescendantsAfterUpsert scans self.PaneID for standalone
// cross-type descendants whose live PPID chain passes through self.PID and
// whose stored ProcessStartTime matches the live process's actual start
// time, folding each as a proxy ref on self. Plan §2.1.2.
//
// Best-effort delete: when DeleteIfUnchanged returns (false, nil) due to a
// concurrent writer touching the candidate row, the partial state (ref
// attached on self + standalone candidate still present) is acceptable —
// projection-layer dedup hides it from SPA, sweep canonicalizePane
// retries within 2s. Increments MetricPartialCanonicalizationCreated.
//
// Identity gate (alive + processStartTimeFn match) prevents PID-reuse
// stale rows from polluting self.Subagents (codex round 2 G2; same logic
// landed in PR-2b findProxyParent). Same-AgentType candidates are skipped
// — proxy refs are cross-type-only by definition.
//
// Returns the latest stored self frame (carrying all successful folds);
// callers replace their local copy with this value because each
// attachProxyRefWithRetry can bump self.LastSeenAt. Storage errors
// propagate as a non-nil error and abort further folds — the partial
// progress is left for sweep.
func (m *Module) canonicalizeDescendantsAfterUpsert(self store.Frame, broadcastTs int64) (store.Frame, error) {
	if m.frames == nil {
		return self, nil
	}
	frames, err := m.frames.ListByPane(self.PaneID)
	if err != nil {
		return self, err
	}
	current := self
	for _, candidate := range frames {
		if candidate.FrameID == current.FrameID {
			continue
		}
		if candidate.AgentType == current.AgentType {
			continue
		}
		if !pidIsAncestorOfWithCap(candidate.PID, current.PID, proxyMaxDepth) {
			continue
		}
		// Codex round 2 #O2 refinement: protect candidates that own
		// LIVE state (native refs or live identity-verified IsProxy
		// refs) from being folded. The original v8 M2 guard (len > 0)
		// over-protected — a candidate carrying ONLY stale dead/PID-
		// reused IsProxy refs is still a race-window artifact safe
		// to fold (the stale ref would be reaped by sweep prune
		// anyway, and dropping it along with the candidate row is
		// equivalent). Distinguishing these classes lets the hot-path
		// fold race-window standalones even when sweep prune has not
		// yet cleared their stale leftovers.
		hasOwnedState := false
		for _, ref := range candidate.Subagents {
			if !ref.IsProxy {
				// Native ref → real owned state.
				hasOwnedState = true
				break
			}
			// IsProxy: live + identity-verified counts as owned;
			// dead/PID-reused is stale and safe to drop with the fold.
			if !isPidAliveFn(ref.SourcePID) {
				continue
			}
			actualStart, sterr := processStartTimeFn(ref.SourcePID)
			if sterr != nil {
				// Read error → defensive; treat as owned (don't drop
				// state on uncertainty). Mirrors findProxyParent's
				// "lookup error → don't infer" convention.
				hasOwnedState = true
				break
			}
			if actualStart != ref.SourceStartTime {
				continue // PID reuse; stale
			}
			// Live + identity-verified IsProxy → owned.
			hasOwnedState = true
			break
		}
		if hasOwnedState {
			continue
		}
		// Identity gate (PR-2b alignment + plan §2.1.2). Mirrors
		// findProxyParent's "live + start_time match" check on the
		// other end of the walk — keeps both sides of canonicalization
		// honest about which candidates count as alive descendants.
		if !isPidAliveFn(candidate.PID) {
			continue
		}
		actualStart, sterr := processStartTimeFn(candidate.PID)
		if sterr != nil || actualStart != candidate.ProcessStartTime {
			continue
		}
		ref := agentpkg.SubagentRef{
			ID:              fmt.Sprintf("proxy:%s:%d:%s", candidate.AgentType, candidate.PID, candidate.ProcessStartTime),
			Type:            candidate.AgentType,
			StartedAt:       broadcastTs,
			SourcePID:       candidate.PID,
			SourceStartTime: candidate.ProcessStartTime,
			IsProxy:         true,
		}
		attached, parentStored, aerr := m.attachProxyRefWithRetry(current, ref, broadcastTs)
		if aerr != nil {
			return current, aerr
		}
		if !attached {
			// Self frame vanished mid-flight (sweep / SessionEnd).
			// Stop folding — caller will project the pane and surface
			// frame_missing.
			return current, nil
		}
		deleted, derr := m.frames.DeleteIfUnchanged(candidate.FrameID, candidate.LastSeenAt)
		if derr != nil {
			return parentStored, derr
		}
		if !deleted {
			// Partial state: proxy attached on self + candidate row
			// still standalone. Acceptable transient — projection
			// dedup hides from SPA, sweep canonicalize repairs within
			// 2s. Continue scanning other candidates.
			agentpkg.MetricPartialCanonicalizationCreated.Add(1)
			log.Printf("phase3.5: descendant scan partial state child %s parent %s (sweep will repair within 2s)", candidate.FrameID, parentStored.FrameID)
		}
		current = parentStored
	}
	return current, nil
}

// findProxyParent walks the sender's PPID ancestor chain (capped at
// proxyMaxDepth) looking for an alive, identity-verified, cross-type frame in
// the same pane. See plan §1.4 for full contract.
//
// Returns (parent, nil) when a proxy candidate is found; (nil, nil) when the
// walk should not proxy-attach (no ancestor has a frame / same-type hard
// stop / all cross-type candidates stale or dead / depth exceeded / proc info
// or start_time read errors that make identity unverifiable). Non-nil error
// is returned only when the frames store fails.
func (m *Module) findProxyParent(req EventRequest) (*store.Frame, error) {
	if m.frames == nil {
		return nil, nil
	}
	info, err := readProcessInfoFn(req.SenderPID)
	if err != nil {
		return nil, nil
	}
	ppid := info.PPID
	for depth := 0; depth < proxyMaxDepth; depth++ {
		if ppid <= 1 {
			return nil, nil
		}
		candidate, err := m.frames.FindByPanePID(req.TmuxPaneID, ppid)
		if err != nil {
			return nil, err
		}
		if candidate != nil {
			// Liveness + identity gating applies to BOTH same-type and
			// cross-type candidates (R3 fix). A stale same-type frame (PID
			// reused, or process dead) is leftover data, not a real
			// "re-session of an existing live sibling"; it must not
			// hard-stop the walk or we'd strand a legitimate proxy attach
			// to a live cross-type ancestor above it.
			if isPidAliveFn(candidate.PID) {
				actualStart, serr := processStartTimeFn(candidate.PID)
				if serr != nil {
					// v5 rule: identity unverifiable → abort walk (consistent
					// with verify.go's "lookup error → don't infer" convention).
					// Prevents mis-attaching to an outer cross-type ancestor
					// when the immediate candidate's start_time is transiently
					// unreadable.
					return nil, nil
				}
				if actualStart == candidate.ProcessStartTime {
					// Live + identity-verified candidate.
					if candidate.AgentType == req.AgentType {
						// Same-type live ancestor: pane already owns a live
						// frame of our agent_type, so this SessionStart is a
						// re-session / update of that frame — not a cross-type
						// proxy. Hard-stop the walk here (don't continue to a
						// cross-type grandparent that would be semantically wrong).
						return nil, nil
					}
					// Cross-type live ancestor: this is our proxy parent.
					return candidate, nil
				}
				// Identity mismatch (PID reused) → stale frame; continue walk
				// to look for a real parent further up. Applies to both
				// same-type and cross-type.
			}
			// Dead candidate: also continue walk; sweep will clear it.
		}
		// No frame at this PID — walk one more level up.
		ancestorInfo, err := readProcessInfoFn(ppid)
		if err != nil {
			return nil, nil
		}
		if ancestorInfo.PPID == ppid {
			return nil, nil
		}
		ppid = ancestorInfo.PPID
	}
	return nil, nil
}
