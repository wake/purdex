package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/wake/purdex/internal/team"
)

// The workbook journal's read feed (plan J-0a): the Store answers team.JournalFeed, read-only. A member or an owner is
// found by spawn_op, the key a relay does not change, so the session, ref and title come out as they are now.

// tasksChangedAfterSQL pages by the row value (updated_at, team_id, seq), which tasks_updated serves in order: a tie on
// updated_at is broken by the key, so a page boundary inside a tie loses and repeats nothing.
const tasksChangedAfterSQL = `
	SELECT t.team_id, t.seq, t.subject, t.description, t.status, t.owner_key,
	       COALESCE(m.session_id, ''), COALESCE(m.ref, ''), COALESCE(m.title, ''),
	       t.created_by_ref, t.metadata_json, t.created_at, t.updated_at
	FROM tasks t LEFT JOIN team_members m ON m.spawn_op = t.owner_key AND t.owner_key != ''
	WHERE (t.updated_at, t.team_id, t.seq) > (?, ?, ?)
	ORDER BY t.updated_at, t.team_id, t.seq
	LIMIT ?`

// TasksChangedAfter implements team.JournalFeed.
func (s *Store) TasksChangedAfter(c team.TaskCursor, limit int) ([]team.TaskChange, error) {
	if limit < 1 {
		return nil, nil
	}
	rows, err := s.db.Query(tasksChangedAfterSQL, c.UpdatedAt, c.TeamID, c.Seq, limit)
	if err != nil {
		return nil, fmt.Errorf("journal feed: read tasks: %w", err)
	}
	defer rows.Close()
	var out []team.TaskChange
	for rows.Next() {
		var t team.TaskChange
		var metaJSON string
		if err := rows.Scan(&t.TeamID, &t.Seq, &t.Subject, &t.Description, &t.Status, &t.OwnerKey,
			&t.OwnerSessionID, &t.OwnerRef, &t.OwnerTitle, &t.CreatedByRef, &metaJSON, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("journal feed: read tasks: %w", err)
		}
		var meta team.TaskMetadata
		if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
			// An unreadable document must not stop the feed: the cursor cannot pass the row, so every later task would wait
			// behind it. The PRs read as none; the rest of the row is intact.
			log.Printf("[team] journal feed: metadata_json of task %s/%d is unreadable (%v); its PRs read as none", t.TeamID, t.Seq, err)
			meta = team.TaskMetadata{}
		}
		t.MetadataPRs = meta.PRs
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("journal feed: read tasks: %w", err)
	}
	return out, nil
}

// ReportsAfter implements team.JournalFeed. rowid is the reports table's implicit one: the table is insert-only, so it
// only grows, and the order is the order the reports were stored in.
func (s *Store) ReportsAfter(afterRowID, sinceMS int64, limit int) ([]team.ReportRow, error) {
	if limit < 1 {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT r.rowid, r.team_id, r.member_key, r.id, r.kind, r.summary, r.body, r.task_seq, r.fields_json,
		       COALESCE(m.session_id, ''), COALESCE(m.ref, ''), COALESCE(m.title, ''), r.created_at
		FROM reports r LEFT JOIN team_members m ON m.spawn_op = r.member_key
		WHERE r.rowid > ? AND r.created_at >= ?
		ORDER BY r.rowid
		LIMIT ?`, afterRowID, sinceMS, limit)
	if err != nil {
		return nil, fmt.Errorf("journal feed: read reports: %w", err)
	}
	defer rows.Close()
	var out []team.ReportRow
	for rows.Next() {
		var r team.ReportRow
		var fieldsJSON string
		if err := rows.Scan(&r.RowID, &r.TeamID, &r.MemberKey, &r.ID, &r.Kind, &r.Summary, &r.Body, &r.TaskSeq, &fieldsJSON,
			&r.MemberSessionID, &r.MemberRef, &r.MemberTitle, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("journal feed: read reports: %w", err)
		}
		var f reportFields
		if err := json.Unmarshal([]byte(fieldsJSON), &f); err != nil {
			// As for a task: needs and pr read as none, so one bad row does not hold back the reports behind it.
			log.Printf("[team] journal feed: fields_json of report %s is unreadable (%v); its needs and pr read as none", r.ID, err)
			f = reportFields{}
		}
		r.Needs, r.PR = f.Needs, f.PR
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("journal feed: read reports: %w", err)
	}
	return out, nil
}

// TeamInfo implements team.JournalFeed: the team's label and the session that leads it now.
func (s *Store) TeamInfo(teamID string) (team.TeamInfo, bool, error) {
	info := team.TeamInfo{TeamID: teamID}
	err := s.db.QueryRow(`SELECT team_label, lead_session_id, ended_at FROM teams WHERE id = ?`, teamID).
		Scan(&info.Label, &info.LeadSessionID, &info.EndedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return team.TeamInfo{}, false, nil
	}
	if err != nil {
		return team.TeamInfo{}, false, fmt.Errorf("journal feed: read team %s: %w", teamID, err)
	}
	return info, true, nil
}
