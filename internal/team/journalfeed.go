package team

// The journal's read feed of the team module (workbook journal spec §4–§5, plan J-0a): the tasks and reports of every
// team of this host, in an order a cursor can follow, for a consumer that cannot import the team module.

// JournalFeedKey is the service-registry key of the JournalFeed.
const JournalFeedKey = "team.journal-feed"

// TaskCursor is where a reader of TasksChangedAfter stopped: the last row it took, in (updated_at, team_id, seq) order.
// The zero value is before every task.
type TaskCursor struct {
	UpdatedAt int64
	TeamID    string
	Seq       int
}

// TaskChange is one task as it is now. OwnerKey is the owner's team_members.spawn_op, which a relay does not change; the
// owner's session, ref and title are read from team_members at the time of the query, so they follow a relay.
type TaskChange struct {
	TeamID                               string
	Seq                                  int
	Subject, Description, Status         string // status pending|in_progress|completed|deleted
	OwnerKey                             string // team_members.spawn_op; "" when unowned
	OwnerSessionID, OwnerRef, OwnerTitle string // resolved now from team_members; empty when unowned or unknown
	CreatedByRef                         string
	MetadataPRs                          []int
	CreatedAt, UpdatedAt                 int64
}

// ReportRow is one report. MemberKey is the reporting member's team_members.spawn_op: with TeamID and ID it is the
// report's stable identity (the member's session changes on a relay, the key does not).
type ReportRow struct {
	RowID                                   int64
	TeamID, MemberKey, ID                   string
	Kind, Summary, Body                     string // kind ack|progress|question|ready|merged|blocked|done
	TaskSeq                                 int
	Needs                                   string // fields_json.needs: "" | "user" | "lead"
	PR                                      int
	MemberSessionID, MemberRef, MemberTitle string // resolved now from team_members; empty when unknown
	CreatedAt                               int64
}

// TeamInfo is one team's label and its lead's current session.
type TeamInfo struct {
	TeamID, Label, LeadSessionID string
	EndedAt                      int64
}

// MemberInfo is one current member of a team: its key (team_members.spawn_op, which a relay does not change) and the
// session, ref and title it has now.
type MemberInfo struct {
	MemberKey, Ref, SessionID, Title string
	State                            MemberState
}

// MemberTaskInfo is a member's current task: Seq is the team-local number, Status pending|in_progress.
type MemberTaskInfo struct {
	Seq     int
	Subject string
	Status  TaskStatus
}

// JournalFeed is the read-only view of tasks, reports and teams the journal ingests from. A store error is an error,
// never an empty answer.
type JournalFeed interface {
	// TasksChangedAfter answers the tasks changed after c, ordered (updated_at, team_id, seq) ascending; at most limit
	// (a limit below 1 answers none). It is NOT a gap-free tail: updated_at is taken by the caller before the team's write
	// lock and ties on it are common, so a row can commit behind a cursor already returned (its (updated_at, team_id, seq)
	// sorts before c). A reader rewinds c in time (the journal plan's §5.0: {UpdatedAt − 10 s, "", 0} each tick, − 1 h once
	// an hour) and dedupes by its own ids. Reports have no such gap: ReportsAfter is exact.
	TasksChangedAfter(c TaskCursor, limit int) ([]TaskChange, error)
	// ReportsAfter answers the reports with rowid > afterRowID and created_at >= sinceMS, rowid ascending; at most limit
	// (a limit below 1 answers none). The first-start backfill passes sinceMS = now − 30 days and pages by rowid;
	// afterwards sinceMS = 0.
	ReportsAfter(afterRowID, sinceMS int64, limit int) ([]ReportRow, error)
	// TeamInfo answers one team's lead (the current session) and label; false when the id is unknown.
	TeamInfo(teamID string) (TeamInfo, bool, error)
	// LastReports answers, for every member_key that has reported in the team (whatever its state now), its newest report:
	// the greatest created_at, a tie to the greatest rowid (ReportsAfter's order). No reports answers none.
	LastReports(teamID string) ([]ReportRow, error)
	// Members answers the team's current members (state active; released, killed and gone ones are not listed), oldest
	// first. An unknown team answers none.
	Members(teamID string) ([]MemberInfo, error)
	// CurrentTask answers the task the member shows as its current one: the team module's own rule (in progress before
	// pending, the newest of each), the same one /api/team shows. false when it has none.
	CurrentTask(teamID, memberKey string) (MemberTaskInfo, bool, error)
}
