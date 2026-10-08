package teammod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// ErrReportIDReused: the report id is already stored with different content.
// The id is the CLI's idempotency key; reusing it for another report is a
// mistake, never a replay.
var ErrReportIDReused = errors.New("report id is already used by a different report")

// reportSchema is the T-1a2 reports table (plan "Tables"). Idempotent. A
// report is always about a task; the id is the CLI's UUID, unique within its
// team (an id is never looked up without the team id). Rows are never
// updated or deleted.
const reportSchema = `
	CREATE TABLE IF NOT EXISTS reports (
		id          TEXT    NOT NULL,
		team_id     TEXT    NOT NULL,
		task_seq    INTEGER NOT NULL,
		member_key  TEXT    NOT NULL,
		kind        TEXT    NOT NULL,
		summary     TEXT    NOT NULL,
		fields_json TEXT    NOT NULL DEFAULT '{}',
		body        TEXT    NOT NULL DEFAULT '',
		created_at  INTEGER NOT NULL,
		PRIMARY KEY (team_id, id)
	);
	CREATE INDEX IF NOT EXISTS reports_task ON reports (team_id, task_seq, created_at);`

// ReportRow is one reports row. fields_json holds Needs, PR, Reviews and SHA
// (zero values omitted); SHA is stored lower-case. As with tasks the store
// never reads the clock: the caller passes CreatedAt.
type ReportRow struct {
	TeamID    string
	TaskSeq   int
	ID        string
	MemberKey string // team_members.spawn_op of the reporting member
	Kind      team.ReportKind
	Summary   string
	Needs     string
	PR        int
	Reviews   []string
	SHA       string
	Body      string
	CreatedAt int64
}

// reportFields is the fields_json document.
type reportFields struct {
	Needs   string   `json:"needs,omitempty"`
	PR      int      `json:"pr,omitempty"`
	Reviews []string `json:"reviews,omitempty"`
	SHA     string   `json:"sha,omitempty"`
}

// normalize returns r with the SHA lower-cased and an empty Reviews as nil,
// the form the row is stored and compared in.
func (r ReportRow) normalize() ReportRow {
	r.SHA = strings.ToLower(r.SHA)
	if len(r.Reviews) == 0 {
		r.Reviews = nil
	}
	return r
}

func (r ReportRow) fieldsJSON() string {
	b, _ := json.Marshal(reportFields{Needs: r.Needs, PR: r.PR, Reviews: r.Reviews, SHA: r.SHA})
	return string(b)
}

// request is the row as the request that would have produced it, for
// team.ValidateReport.
func (r ReportRow) request() team.ReportRequest {
	return team.ReportRequest{ID: r.ID, Kind: r.Kind, Summary: r.Summary, Needs: r.Needs, PR: r.PR,
		Reviews: r.Reviews, SHA: r.SHA, Body: r.Body}
}

const reportCols = `id, team_id, task_seq, member_key, kind, summary, fields_json, body, created_at`

func scanReport(r rowScanner) (ReportRow, error) {
	var out ReportRow
	var kind, fieldsJSON string
	if err := r.Scan(&out.ID, &out.TeamID, &out.TaskSeq, &out.MemberKey, &kind, &out.Summary, &fieldsJSON, &out.Body, &out.CreatedAt); err != nil {
		return ReportRow{}, err
	}
	out.Kind = team.ReportKind(kind)
	var f reportFields
	if err := json.Unmarshal([]byte(fieldsJSON), &f); err != nil {
		return ReportRow{}, fmt.Errorf("decode fields_json of report %s: %w", out.ID, err)
	}
	out.Needs, out.PR, out.Reviews, out.SHA = f.Needs, f.PR, f.Reviews, f.SHA
	return out.normalize(), nil
}

// sameReport: the stored row and a new one say the same thing (created_at is
// the only thing a retry may differ in).
func sameReport(stored, in ReportRow) bool {
	return stored.TeamID == in.TeamID && stored.TaskSeq == in.TaskSeq && stored.MemberKey == in.MemberKey &&
		stored.Kind == in.Kind && stored.Summary == in.Summary && stored.Body == in.Body &&
		stored.fieldsJSON() == in.fieldsJSON()
}

// InsertReport stores report r and applies its effect on the task (r.TeamID,
// r.TaskSeq) in ONE write transaction, and returns the stored row, the task
// as it is afterwards and whether this was a replay.
//
// The caller fills every field (CreatedAt included). The store validates the
// id and the fields (team.ValidReportID / team.ValidateReport) so nothing is
// stored that could not be sent as a peer message.
//
// The store does not check that the task is the reporting member's own: that
// is the route's job (T-1b2: "the task is the member's own"), which knows who
// the caller is. Here MemberKey is only recorded.
//
//   - Task missing in that team: ErrTaskNotFound (another team's task with the
//     same seq is a different task).
//   - r.ID already stored IN THIS TEAM with the same content: replay=true, the
//     stored row and the CURRENT task come back, nothing is written and no
//     effect runs again. With different content: ErrReportIDReused. The id is
//     scoped to the team (primary key (team_id, id)), so another team's
//     report with the same id is invisible here and never an error: the
//     answer cannot tell a team whether an id exists elsewhere.
//   - Otherwise the row is inserted and the effect applied (spec D-3): ack
//     moves pending to in_progress; ready appends pr to metadata.prs; merged
//     appends the lower-cased sha to metadata.shas; done completes a pending
//     or in_progress task. Every report sets last_report_* when r.CreatedAt is
//     not older than the task's last_report_at (a tie goes to the later
//     write) and raises updated_at to r.CreatedAt when that is newer, so a
//     late or out-of-order report never moves either stamp backwards; its
//     status effect and metadata append apply regardless of the stamps.
//     Appends skip a value already present and keep the order. A completed or
//     deleted task still gets the stamps and the appends, but its status
//     never changes.
func (s *Store) InsertReport(r ReportRow) (ReportRow, TaskRow, bool, error) {
	r = r.normalize()
	if r.TeamID == "" || r.MemberKey == "" || r.TaskSeq <= 0 {
		return ReportRow{}, TaskRow{}, false, errors.New("insert report: team, task and member must be set")
	}
	if err := team.ValidReportID(r.ID); err != nil {
		return ReportRow{}, TaskRow{}, false, fmt.Errorf("insert report: %w", err)
	}
	if err := team.ValidateReport(r.request()); err != nil {
		return ReportRow{}, TaskRow{}, false, fmt.Errorf("insert report: %w", err)
	}

	var stored ReportRow
	var task TaskRow
	var replay bool
	err := s.immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		cur, ok, err := getTaskIn(ctx, conn, r.TeamID, r.TaskSeq)
		if err != nil {
			return fmt.Errorf("read task %s/%d: %w", r.TeamID, r.TaskSeq, err)
		}
		if !ok {
			return ErrTaskNotFound
		}

		prev, err := scanReport(conn.QueryRowContext(ctx, `SELECT `+reportCols+` FROM reports WHERE team_id = ? AND id = ?`, r.TeamID, r.ID))
		switch {
		case err == nil:
			if !sameReport(prev, r) {
				return ErrReportIDReused
			}
			stored, task, replay = prev, cur, true
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("read report %s: %w", r.ID, err)
		}

		if _, err := conn.ExecContext(ctx, `INSERT INTO reports (`+reportCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.TeamID, r.TaskSeq, r.MemberKey, string(r.Kind), r.Summary, r.fieldsJSON(), r.Body, r.CreatedAt); err != nil {
			return fmt.Errorf("insert report %s: %w", r.ID, err)
		}
		cur = applyReport(cur, r)
		metaJSON, _ := json.Marshal(cur.Metadata)
		if _, err := conn.ExecContext(ctx, `UPDATE tasks SET status = ?, metadata_json = ?, last_report_kind = ?,
			last_report_summary = ?, last_report_at = ?, updated_at = ? WHERE team_id = ? AND seq = ?`,
			string(cur.Status), string(metaJSON), cur.LastReportKind, cur.LastReportSummary, cur.LastReportAt, cur.UpdatedAt,
			cur.TeamID, cur.Seq); err != nil {
			return fmt.Errorf("apply report %s to task %s/%d: %w", r.ID, cur.TeamID, cur.Seq, err)
		}
		if s.afterReportInsert != nil {
			if err := s.afterReportInsert(); err != nil {
				return err
			}
		}
		stored, task = r, cur
		return nil
	})
	if err != nil {
		return ReportRow{}, TaskRow{}, false, err
	}
	return stored, task, replay, nil
}

// applyReport returns t with report r's effect applied (spec D-3). It is a
// pure function of the two rows.
func applyReport(t TaskRow, r ReportRow) TaskRow {
	// Status moves only out of pending / in_progress: a completed or deleted
	// task keeps its status (it still gets the stamp and the appends below).
	switch r.Kind {
	case team.ReportAck:
		if t.Status == team.TaskPending {
			t.Status = team.TaskInProgress
		}
	case team.ReportReady:
		t.Metadata.PRs = appendMissing(t.Metadata.PRs, r.PR)
	case team.ReportMerged:
		t.Metadata.SHAs = appendMissing(t.Metadata.SHAs, r.SHA)
	case team.ReportDone:
		// A report is the owner's authoritative statement that the done-when
		// is met, so pending -> completed is allowed here even though
		// TaskTransitionAllowed(..., TaskByOwner) refuses it for a manual
		// status change (a member closing a task it never started).
		if t.Status == team.TaskPending || t.Status == team.TaskInProgress {
			t.Status = team.TaskCompleted
		}
	}
	if r.CreatedAt >= t.LastReportAt { // a tie goes to the later write
		t.LastReportKind, t.LastReportSummary, t.LastReportAt = string(r.Kind), r.Summary, r.CreatedAt
	}
	if r.CreatedAt > t.UpdatedAt {
		t.UpdatedAt = r.CreatedAt
	}
	return t
}

// appendMissing appends v to list unless it is already there.
func appendMissing[T comparable](list []T, v T) []T {
	for _, have := range list {
		if have == v {
			return list
		}
	}
	return append(list, v)
}

// GetReport returns the report with the given id.
func (s *Store) GetReport(teamID, id string) (ReportRow, bool, error) {
	r, err := scanReport(s.db.QueryRow(`SELECT `+reportCols+` FROM reports WHERE team_id = ? AND id = ?`, teamID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ReportRow{}, false, nil
	}
	if err != nil {
		return ReportRow{}, false, fmt.Errorf("get report %s/%s: %w", teamID, id, err)
	}
	return r, true, nil
}

// Report list limits.
const maxListReports = 200

// ListReports lists the reports of a team, newest first (created_at DESC,
// then insertion order DESC). taskSeq 0 is every task of the team; sinceMS
// keeps reports with created_at >= sinceMS; limit is clamped to 1..200 (0 or
// less means 200). The slice is never nil.
func (s *Store) ListReports(teamID string, taskSeq int, sinceMS int64, limit int) ([]ReportRow, error) {
	return listReportsIn(context.Background(), s.db, teamID, taskSeq, sinceMS, limit)
}

// rowsQuerier is what *sql.DB and *sql.Tx share for a multi-row read.
type rowsQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// listReportsIn is ListReports on a database or on an open transaction.
func listReportsIn(ctx context.Context, db rowsQuerier, teamID string, taskSeq int, sinceMS int64, limit int) ([]ReportRow, error) {
	if limit <= 0 || limit > maxListReports {
		limit = maxListReports
	}
	q := `SELECT ` + reportCols + ` FROM reports WHERE team_id = ? AND created_at >= ?`
	args := []any{teamID, sinceMS}
	if taskSeq != 0 {
		q += ` AND task_seq = ?`
		args = append(args, taskSeq)
	}
	q += ` ORDER BY created_at DESC, rowid DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list reports %s: %w", teamID, err)
	}
	defer rows.Close()
	out := []ReportRow{}
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, fmt.Errorf("list reports %s: %w", teamID, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list reports %s: %w", teamID, err)
	}
	return out, nil
}
