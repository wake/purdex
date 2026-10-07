package teammod

// POST /api/team/spawns (spec §7.2 steps 1–2, plan v3 deviation 11):
// create-or-join a spawn op and answer it.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// errInternal is the 500 code of a programming error (outside the wire codes).
const errInternal = "internal"

// handleSpawn creates the op, or joins the one this id already names, and
// answers 200 with it once it leaves running, or after spawnWait while it
// still runs; the CLI then posts the same body again.
func (m *Module) handleSpawn(w http.ResponseWriter, r *http.Request) {
	if m.stopping() || m.tmux == nil {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping or has no tmux", nil)
		return
	}
	var req team.SpawnRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if why := normaliseSpawn(&req); why != "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, why, nil)
		return
	}
	origin, ok, err := m.origins.ResolveOrigin(req.OriginInbox)
	if err != nil {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	}
	if !ok {
		m.writeErr(w, http.StatusBadRequest, team.ErrOriginUnknown, "origin_inbox is not a live Claude Code session on this host", nil)
		return
	}
	row, ok := m.acceptSpawn(w, req, origin)
	if !ok {
		return
	}
	row, err = m.awaitSpawn(r.Context(), row.ID)
	var op team.SpawnOp
	if err == nil {
		op, err = m.spawnView(row, origin.Address)
	}
	if err != nil {
		m.logf("[team] spawn %s: %v", row.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, op)
}

// normaliseSpawn checks the body's shape (U20 (a): model and effort again,
// after the CLI) and makes id and cwd canonical. It returns why the body is
// a 400, or "".
func normaliseSpawn(req *team.SpawnRequest) string {
	u, err := uuid.Parse(req.ID)
	switch {
	case err != nil || u.Version() != 4 || u.Variant() != uuid.RFC4122:
		return "id must be a UUID v4"
	case req.Model != "" && !team.ValidModel(req.Model):
		return "invalid model " + strconv.Quote(req.Model)
	case req.Effort != "" && !team.ValidEffort(req.Effort):
		return "effort must be one of " + strings.Join(team.Efforts, ", ")
	case req.Title != "" && ipeers.ValidateTitle(req.Title) != nil:
		return ipeers.ValidateTitle(req.Title).Error()
	case !filepath.IsAbs(req.Cwd):
		return "cwd must be an absolute path"
	}
	req.ID, req.Cwd = u.String(), filepath.Clean(req.Cwd)
	return ""
}

// spawnHash is the idempotency fingerprint of a spawn: who asked, and for what.
func spawnHash(sessionID string, req team.SpawnRequest) string {
	h := sha256.New()
	for _, s := range []string{sessionID, req.Cwd, req.Title, req.Model, req.Effort} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// acceptSpawn is create-or-join under createMu (spec §7.2 step 2, §9.1). A
// replay of an id joins its op with no further check. A new id must come
// from a live team's lead and name an existing cwd under a granted root;
// then the op is written by AcceptSpawnOp, which checks the lead and the
// limit again in its own transaction (review H1), and its runner starts.
// false means an error was written.
func (m *Module) acceptSpawn(w http.ResponseWriter, req team.SpawnRequest, origin team.Origin) (spawnRow, bool) {
	fail := func(status int, code, detail string) (spawnRow, bool) {
		m.writeErr(w, status, code, detail, nil)
		return spawnRow{}, false
	}
	failStore := func(err error) (spawnRow, bool) {
		m.logf("[team] spawn %s: %v", req.ID, err)
		return fail(http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log")
	}
	hash := spawnHash(origin.SessionID, req)
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		return fail(http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping")
	}
	if row, stored, found, err := m.store.spawnOpWithHash(req.ID); err != nil {
		return failStore(err)
	} else if found {
		if stored != hash {
			return fail(http.StatusConflict, team.ErrIDConflict, "id already used by a different spawn")
		}
		return row, true
	}
	t, found, err := m.store.LiveTeamByLead(origin.SessionID)
	if err != nil {
		return failStore(err)
	}
	if !found {
		return fail(http.StatusConflict, team.ErrNotLead, "this session leads no live team")
	}
	cwd, err := filepath.EvalSymlinks(req.Cwd)
	if err == nil {
		err = m.sessions.ValidateCwd(cwd)
	}
	if err != nil {
		return fail(http.StatusBadRequest, team.ErrBadRequest, "cwd does not exist or is not a directory: "+req.Cwd)
	}
	if !underRoots(cwd, t.Grant.Roots) {
		return fail(http.StatusConflict, team.ErrCwdOutsideGrant, cwd+" is under none of the team's roots")
	}
	name, err := team.SpawnTmuxName(req.ID)
	if err != nil { // unreachable: normaliseSpawn made the id a canonical UUID v4
		m.logf("[team] spawn: %v", err)
		return fail(http.StatusInternalServerError, errInternal, err.Error())
	}
	if m.afterSpawnTeamRead != nil {
		m.afterSpawnTeamRead()
	}
	now := m.now()
	row, _, inserted, err := m.store.AcceptSpawnOp(spawnRow{ID: req.ID, TeamID: t.ID, HostID: m.hostID(),
		OriginSessionID: origin.SessionID, Cwd: cwd, Title: req.Title, Model: req.Model, Effort: req.Effort,
		TmuxName: name, Step: team.StepAccepted, State: team.SpawnRunning, CreatedAt: now, UpdatedAt: now}, hash)
	switch {
	case errors.Is(err, ErrSpawnNotLead):
		return fail(http.StatusConflict, team.ErrNotLead, "this session no longer leads team "+t.ID)
	case errors.Is(err, ErrSpawnTeamFull):
		return fail(http.StatusConflict, team.ErrTeamFull, fmt.Sprintf("team %s has its %d active or starting members", t.ID, t.Grant.MaxMembers))
	case err != nil:
		return failStore(err)
	case !inserted: // unreachable under createMu after the read above
		return fail(http.StatusConflict, team.ErrIDConflict, "id already used by a different spawn")
	}
	m.logf("[team] spawn %s accepted: team %s, %s in %s", row.ID, t.ID, row.TmuxName, row.Cwd)
	m.startSpawn(row.ID)
	return row, true
}

// awaitSpawn waits up to spawnWait for op id to leave running (the runner
// wakes it), the client to go or Stop, then reads it.
func (m *Module) awaitSpawn(ctx context.Context, id string) (spawnRow, error) {
	ch := m.addWaiter(id)
	defer m.removeWaiter(id, ch)
	row, ok, err := m.store.GetSpawnOp(id)
	if err == nil && ok && row.State == team.SpawnRunning {
		t := time.NewTimer(m.spawnWait)
		defer t.Stop()
		select {
		case <-ch:
		case <-t.C:
		case <-ctx.Done():
		case <-m.stopCtx.Done():
		}
		row, ok, err = m.store.GetSpawnOp(id)
	}
	if err == nil && !ok {
		err = fmt.Errorf("spawn op %s vanished", id)
	}
	return row, err
}

// spawnView is the wire form of an op; a done op carries its member with the
// member's live address (spec §7.2 step 6: ref, address, tmux session,
// session id).
func (m *Module) spawnView(r spawnRow, leadAddress string) (team.SpawnOp, error) {
	op := team.SpawnOp{ID: r.ID, TeamID: r.TeamID, HostID: r.HostID, State: r.State, Step: r.Step, Reason: r.Reason,
		Cwd: r.Cwd, Title: r.Title, Model: r.Model, Effort: r.Effort, TmuxSession: r.TmuxName,
		LeadAddress: leadAddress, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if r.State != team.SpawnDone {
		return op, nil
	}
	rows, err := m.store.MembersOf(r.TeamID)
	if err != nil {
		return team.SpawnOp{}, err
	}
	for _, mr := range rows {
		if mr.SpawnOp != r.ID {
			continue
		}
		addr := ""
		if o, ok, err := m.origins.ResolveOriginBySession(mr.SessionID); err == nil && ok {
			addr = o.Address
		}
		op.Member = &team.Member{SessionID: mr.SessionID, Ref: mr.Ref, Address: addr, TeamID: mr.TeamID, HostID: mr.HostID,
			Title: mr.Title, Cwd: mr.Cwd, TmuxSession: mr.TmuxSession, State: mr.State, Model: mr.Model, Effort: mr.Effort,
			SpawnOp: mr.SpawnOp, CreatedAt: mr.CreatedAt}
		return op, nil
	}
	return team.SpawnOp{}, fmt.Errorf("spawn op %s is done but has no member row", r.ID)
}
