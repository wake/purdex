package session

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// The no-agent launch panel's daemon side (spec docs/specs/2026-10-12-no-agent-launch-panel.md §2.2, §2.3): the plan a client
// renders and the launch it posts. The daemon owns every rule — which agent last ran, the project directory, the typed line,
// the busy check and the claim; the clients render the plan and send a choice.

// AgentRunningKey is the registry key of the agent module's launch facts (the agent module registers itself under it).
const AgentRunningKey = "agent.running"

// LaunchAgentFacts is what the plan needs from the agent module; it lives here because agent already depends on session.
type LaunchAgentFacts interface {
	// AgentRunning: an agent of any type runs in the session (a live root frame, or the hook state).
	AgentRunning(ctx context.Context, code string) (bool, error)
	// LastRun is the last root run recorded for the session code, or nil.
	LastRun(code string) (*store.LastRun, error)
}

// LaunchClaimReleaser is what the agent module calls on a root SessionStart: the launch that was waiting for it is done.
type LaunchClaimReleaser interface {
	ReleaseLaunchClaim(tmuxSessionName string)
}

// resumeIDPattern is the rebuild composer's rule for a session id that may be substituted into `{id}`
// (spa/src/lib/rebuild/composer.ts): anything else could change the shape of the typed line.
var resumeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// freshLines is the start table: the agent a session last ran starts again with one word.
var freshLines = map[string]string{"cc": "claude", "codex": "codex", "opencode": "opencode"}

const defaultFreshLine = "claude"

// 409 codes, stable: clients map them to their own text and never show an HTTP status text.
const (
	launchGenerationConflict = "generation_conflict"
	launchBusy               = "busy"
	launchAlreadyLaunching   = "already_launching"
	launchAgentRunning       = "agent_running"
)

type launchLast struct {
	AgentType string `json:"agent_type"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	EndedAt   int64  `json:"ended_at"`
}

type launchDir struct {
	Path  string `json:"path"`
	State string `json:"state"` // "move" | "here"
}

type launchResume struct {
	Line   *string `json:"line"`
	Reason string  `json:"reason,omitempty"` // no_record | no_template | empty_command, only when line is null
}

type launchFresh struct {
	Line string `json:"line"`
}

type launchBusyInfo struct {
	Command string `json:"command"`
}

// launchPlan is the GET answer, field names and shape as the spec fixes them (both clients are written to it).
type launchPlan struct {
	AgentRunning bool            `json:"agent_running"`
	TmuxInstance string          `json:"tmux_instance"`
	Last         *launchLast     `json:"last"`
	Dir          *launchDir      `json:"dir"`
	Resume       launchResume    `json:"resume"`
	Fresh        launchFresh     `json:"fresh"`
	Busy         *launchBusyInfo `json:"busy"`
}

type launchRequest struct {
	Kind                 string `json:"kind"`
	Cd                   bool   `json:"cd"`
	ExpectedTmuxInstance string `json:"expected_tmux_instance"`
}

// shellQuote single-quotes a path for the typed `cd`, ALWAYS — never "when needed": the clients build their button preview with
// the same rule, so the two must agree on every path. `'` becomes `'\”`.
func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

func writeLaunchJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeLaunchCode(w http.ResponseWriter, status int, code string) {
	writeLaunchJSON(w, status, map[string]string{"code": code})
}

func (m *SessionModule) launchFacts() (LaunchAgentFacts, error) {
	if m.core == nil || m.core.Registry == nil {
		return nil, errors.New("launch: no service registry")
	}
	svc, ok := m.core.Registry.Get(AgentRunningKey)
	if !ok {
		return nil, errors.New("launch: " + AgentRunningKey + " is not registered")
	}
	facts, ok := svc.(LaunchAgentFacts)
	if !ok {
		return nil, errors.New("launch: " + AgentRunningKey + " does not implement LaunchAgentFacts")
	}
	return facts, nil
}

// resumeTemplate is the host's template for the agent type; absent when the reader is not mounted or has none.
func (m *SessionModule) resumeTemplate(agentType string) (hostconfig.ResumeTemplatePair, bool, error) {
	if m.core == nil || m.core.Registry == nil {
		return hostconfig.ResumeTemplatePair{}, false, nil
	}
	svc, ok := m.core.Registry.Get(hostconfig.ResumeTemplatesKey)
	if !ok {
		return hostconfig.ResumeTemplatePair{}, false, nil
	}
	reader, ok := svc.(hostconfig.ResumeTemplateReader)
	if !ok {
		return hostconfig.ResumeTemplatePair{}, false, nil
	}
	all, err := reader.ResumeTemplates()
	if err != nil {
		return hostconfig.ResumeTemplatePair{}, false, err
	}
	pair, ok := all[agentType]
	return pair, ok, nil
}

// resumeFor renders the resume line from the template, or says why there is none.
func resumeFor(last *store.LastRun, pair hostconfig.ResumeTemplatePair, haveTemplate bool) launchResume {
	if last == nil {
		return launchResume{Reason: "no_record"}
	}
	if !haveTemplate {
		return launchResume{Reason: "no_template"}
	}
	line := pair.Fallback
	if resumeIDPattern.MatchString(last.SessionID) {
		line = strings.ReplaceAll(pair.Exact, "{id}", last.SessionID)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return launchResume{Reason: "empty_command"}
	}
	return launchResume{Line: &line}
}

// planFor derives the plan from live data only. A session with an agent running gets the short plan: the clients show no panel.
func (m *SessionModule) planFor(ctx context.Context, info *SessionInfo) (launchPlan, error) {
	plan := launchPlan{TmuxInstance: info.TmuxInstance, Fresh: launchFresh{Line: defaultFreshLine}, Resume: launchResume{Reason: "no_record"}}
	facts, err := m.launchFacts()
	if err != nil {
		return plan, err
	}
	running, err := facts.AgentRunning(ctx, info.Code)
	if err != nil {
		return plan, err
	}
	if running {
		plan.AgentRunning = true
		return plan, nil
	}

	// `last` is a record of THIS tmux generation only (both non-empty and equal): a reused session code never lends one.
	rec, err := facts.LastRun(info.Code)
	if err != nil {
		return plan, err
	}
	if rec != nil && (rec.TmuxInstance == "" || info.TmuxInstance == "" || rec.TmuxInstance != info.TmuxInstance) {
		rec = nil
	}
	var pair hostconfig.ResumeTemplatePair
	haveTemplate := false
	if rec != nil {
		plan.Last = &launchLast{AgentType: rec.AgentType, SessionID: rec.SessionID, Cwd: rec.Cwd, EndedAt: rec.EndedAt}
		if line, ok := freshLines[rec.AgentType]; ok {
			plan.Fresh.Line = line
		}
		if rec.Cwd != "" {
			state := "move"
			if here, perr := m.tmux.PaneCurrentPath(info.TmuxID + ":"); perr == nil && strings.TrimSuffix(here, "/") == strings.TrimSuffix(rec.Cwd, "/") {
				state = "here"
			}
			plan.Dir = &launchDir{Path: rec.Cwd, State: state}
		}
		if pair, haveTemplate, err = m.resumeTemplate(rec.AgentType); err != nil {
			return plan, err
		}
	}
	plan.Resume = resumeFor(rec, pair, haveTemplate)

	// The pane's foreground command as the session read it; the guard on the send decides again, on the server, at send time.
	if cmd := strings.TrimSpace(info.CurrentCommand); cmd != "" && !tmux.IsShellCommand(cmd) {
		plan.Busy = &launchBusyInfo{Command: cmd}
	}
	return plan, nil
}

// handleLaunchPlan serves GET /api/sessions/{code}/launch-plan. Query parameters are ignored — expected_tmux_instance included:
// a GET never answers 400 for it.
func (m *SessionModule) handleLaunchPlan(w http.ResponseWriter, r *http.Request) {
	info, err := m.GetSessionContext(r.Context(), r.PathValue("code"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	plan, err := m.planFor(r.Context(), info)
	if err != nil {
		log.Printf("[session] launch-plan %s: %v", info.Code, err)
		http.Error(w, "launch plan unavailable", http.StatusInternalServerError)
		return
	}
	writeLaunchJSON(w, http.StatusOK, plan)
}

// handleLaunch serves POST /api/sessions/{code}/launch. The daemon re-derives the plan and never takes a line from the client.
func (m *SessionModule) handleLaunch(w http.ResponseWriter, r *http.Request) {
	var req launchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Kind != "resume" && req.Kind != "fresh") || req.ExpectedTmuxInstance == "" {
		writeLaunchCode(w, http.StatusBadRequest, "bad_request")
		return
	}
	info, err := m.GetSessionContext(r.Context(), r.PathValue("code"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	// The format of the expectation is not policed (clients echo the list's tmux_instance as is): one that is not the current
	// generation is a conflict, and the server that types re-checks it anyway.
	if req.ExpectedTmuxInstance != info.TmuxInstance || !tmux.ValidInstance(req.ExpectedTmuxInstance) {
		writeLaunchCode(w, http.StatusConflict, launchGenerationConflict)
		return
	}

	key := launchClaimKey(info.Code, info.TmuxInstance)
	nonce, ok := m.claims.take(key)
	if !ok {
		writeLaunchCode(w, http.StatusConflict, launchAlreadyLaunching)
		return
	}
	sent := false
	defer func() {
		if !sent { // a launch that did not go out frees the session at once; one that did keeps it until SessionStart or the TTL
			m.claims.release(key, nonce)
		}
	}()

	plan, err := m.planFor(r.Context(), info)
	if err != nil {
		log.Printf("[session] launch %s: %v", info.Code, err)
		http.Error(w, "launch plan unavailable", http.StatusInternalServerError)
		return
	}
	if plan.AgentRunning {
		writeLaunchCode(w, http.StatusConflict, launchAgentRunning)
		return
	}
	if plan.Busy != nil {
		writeLaunchCode(w, http.StatusConflict, launchBusy)
		return
	}
	line := plan.Fresh.Line
	if req.Kind == "resume" {
		if plan.Resume.Line == nil {
			writeLaunchCode(w, http.StatusBadRequest, "bad_request")
			return
		}
		line = *plan.Resume.Line
	}
	if req.Cd && plan.Dir != nil && plan.Dir.State == "move" {
		line = "cd " + shellQuote(plan.Dir.Path) + " && " + line
	}

	switch err := m.tmux.SendKeysIfInstanceAndShell(info.TmuxID, info.TmuxInstance, line, tmux.KeyEnter); {
	case err == nil:
		sent = true
		writeLaunchJSON(w, http.StatusOK, map[string]string{"line": line})
	case errors.Is(err, tmux.ErrGenerationMismatch):
		writeLaunchCode(w, http.StatusConflict, launchGenerationConflict)
	case errors.Is(err, tmux.ErrPaneBusy):
		writeLaunchCode(w, http.StatusConflict, launchBusy)
	default:
		log.Printf("[session] launch %s: send: %v", info.Code, err)
		http.Error(w, "launch failed", http.StatusInternalServerError)
	}
}

// ReleaseLaunchClaim is the agent module's call on a root SessionStart: the claim of the session's CURRENT generation (the
// name re-resolved now to its code and instance) is released. A SessionStart of another session, or of an old generation,
// finds no matching claim.
//
// Most SessionStarts happen with no launch in flight: they return before asking tmux anything. (A SessionStart that arrives long
// after its launch's claim timed out can release a later launch's claim; the later launch is still guarded — the agent is running
// or the pane is busy — so the worst case is a refused second launch, not a keystroke into the wrong program.)
func (m *SessionModule) ReleaseLaunchClaim(tmuxSessionName string) {
	if !m.claims.any() {
		return
	}
	code, ok := m.LookupCodeByName(tmuxSessionName)
	if !ok {
		return
	}
	instance := m.TmuxInstance()
	if instance == "" {
		return
	}
	m.claims.releaseKey(launchClaimKey(code, instance))
}
