package resourcesmod

import (
	"context"
	"time"

	iagent "github.com/wake/purdex/internal/agent"
	ipeers "github.com/wake/purdex/internal/peers"
)

// Session for a process-scope lease (lease v2 spec D-4′ "Session for scope
// process"). `pdx lease run` sends no session id: its holder is the pdx process.
// To park a request under the session that made it, and to find it again when
// the same session asks again, the daemon walks the holder's ancestors to the
// agent process the registry knows.

// maxAncestryHops bounds the walk: pdx -> shell -> agent is 2; anything much
// deeper is not a session's own command.
const maxAncestryHops = 16

// ancestry is the part of the process table the walk needs: the parent and the
// start time of a pid. *iagent.ProcessSnapshot is the production value (no
// fork); tests substitute a table.
type ancestry interface {
	PPID(pid int) (int, error)
	Start(pid int) (time.Time, error)
}

var _ ancestry = (*iagent.ProcessSnapshot)(nil)

// sessionOfProcess is the session whose agent process is pid or one of its
// ancestors within maxAncestryHops, with that agent's pid and start time (as
// the registry recorded it); the session is "" when there is none. An agent pid
// counts only when its start time is the one the registry recorded (at second
// precision): a pid that was reused is another process.
func (m *Module) sessionOfProcess(ctx context.Context, pid int) (session string, agentPID int, agentStart string) {
	session, agentPID, agentStart, _ = m.sessionOfProcessChecked(ctx, pid)
	return session, agentPID, agentStart
}

// sessionOfProcessChecked is sessionOfProcess that also says whether the answer
// "none" is a failure to look (the process table or the registry could not be
// read) rather than a finding.
func (m *Module) sessionOfProcessChecked(ctx context.Context, pid int) (session string, agentPID int, agentStart string, failed bool) {
	if m.roots == nil || pid <= 0 {
		return "", 0, "", false
	}
	ctx, cancel := context.WithTimeout(ctx, sampleBudget)
	defer cancel()
	snap, err := m.procSnapshot(ctx)
	if err != nil {
		return "", 0, "", true
	}
	roots, err := m.roots.ProcessRoots(snap)
	if err != nil {
		return "", 0, "", true
	}
	byPID := make(map[int]string, len(roots))
	startOf := make(map[int]string, len(roots))
	for _, r := range roots {
		byPID[r.PID] = r.SessionID
		startOf[r.PID] = r.ProcStart
	}
	var view ancestry = snap
	if m.ancestryOf != nil {
		view = m.ancestryOf(snap)
	}
	for hop := 0; hop <= maxAncestryHops; hop++ {
		if sid, ok := byPID[pid]; ok {
			want, err := ipeers.ParseProcStart(startOf[pid])
			got, gerr := view.Start(pid)
			if err != nil || gerr != nil || got.IsZero() || !got.Truncate(time.Second).Equal(want.Truncate(time.Second)) {
				return "", 0, "", false
			}
			return sid, pid, startOf[pid], false
		}
		parent, err := view.PPID(pid)
		if err != nil || parent <= 1 || parent == pid {
			return "", 0, "", false
		}
		pid = parent
	}
	return "", 0, "", false
}
