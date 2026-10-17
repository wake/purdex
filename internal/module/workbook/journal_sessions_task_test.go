package workbook

import (
	"testing"
	"time"
)

// A team task entry carries the lead's session (the row's Lead). Mutation gate: drop SID in the task events -> red.
func TestTaskMirror_TaskEntriesCarryTheLeadSession(t *testing.T) {
	k := newMirrorKit(t)
	k.finishBaseline()
	k.feed.set(k.task(12, "RA-2", taskPending, time.Second, time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	e, ok := k.entry(entryID(mkTeam, 12, "new"))
	if !ok || e.SID != mkLead {
		t.Fatalf("task_new = %+v, %v", e, ok)
	}
}
