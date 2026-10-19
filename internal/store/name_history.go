package store

import (
	"context"
	"database/sql"
	"errors"
)

// Name history (name pool v2 spec §6.1): who was a lead and who worked for whom, by persona code. It is written only through
// the name service (a lead approval, a registered spawn, an adopt) and read by the picking rules and the familiar set.

// NameLead is one code that has been a lead: how many times, and when last.
type NameLead struct {
	Code   string
	Count  int
	LastAt int64
}

// NamePair is one lead code and member code that worked together.
type NamePair struct {
	LeadCode, MemberCode string
	Count                int
	LastAt               int64
}

// NameHistoryStore is the history behind a MetaStore.
type NameHistoryStore struct{ db *sql.DB }

// NameHistory returns the name-history store backed by this MetaStore's DB.
func (m *MetaStore) NameHistory() *NameHistoryStore { return &NameHistoryStore{db: m.db} }

func migrateNameHistory(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS name_leads (
			code    TEXT PRIMARY KEY,
			count   INTEGER NOT NULL,
			last_at INTEGER NOT NULL
		)`); err != nil {
		return err
	}
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS name_pairs (
			lead_code   TEXT NOT NULL,
			member_code TEXT NOT NULL,
			count       INTEGER NOT NULL,
			last_at     INTEGER NOT NULL,
			PRIMARY KEY (lead_code, member_code)
		)`)
	return err
}

// RecordLead counts one more time that code was a lead.
func (h *NameHistoryStore) RecordLead(ctx context.Context, code string, nowMs int64) error {
	if code == "" {
		return errors.New("name history: empty lead code")
	}
	_, err := h.db.ExecContext(ctx, `
		INSERT INTO name_leads (code, count, last_at) VALUES (?, 1, ?)
		ON CONFLICT(code) DO UPDATE SET count = count + 1, last_at = excluded.last_at`, code, nowMs)
	return err
}

// RecordPair counts one more time that memberCode worked for leadCode.
func (h *NameHistoryStore) RecordPair(ctx context.Context, leadCode, memberCode string, nowMs int64) error {
	if leadCode == "" || memberCode == "" {
		return errors.New("name history: empty lead or member code")
	}
	_, err := h.db.ExecContext(ctx, `
		INSERT INTO name_pairs (lead_code, member_code, count, last_at) VALUES (?, ?, 1, ?)
		ON CONFLICT(lead_code, member_code) DO UPDATE SET count = count + 1, last_at = excluded.last_at`,
		leadCode, memberCode, nowMs)
	return err
}

// Leads is every lead code, most recent first.
func (h *NameHistoryStore) Leads(ctx context.Context) ([]NameLead, error) {
	rows, err := h.db.QueryContext(ctx, `SELECT code, count, last_at FROM name_leads ORDER BY last_at DESC, code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NameLead
	for rows.Next() {
		var l NameLead
		if err := rows.Scan(&l.Code, &l.Count, &l.LastAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Pairs is every lead/member pair.
func (h *NameHistoryStore) Pairs(ctx context.Context) ([]NamePair, error) {
	rows, err := h.db.QueryContext(ctx, `SELECT lead_code, member_code, count, last_at FROM name_pairs ORDER BY last_at DESC, lead_code, member_code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NamePair
	for rows.Next() {
		var p NamePair
		if err := rows.Scan(&p.LeadCode, &p.MemberCode, &p.Count, &p.LastAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Familiar is the codes this host has seen as a lead or as a member, most recent first, each once, at most limit of them.
func (h *NameHistoryStore) Familiar(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := h.db.QueryContext(ctx, `
		SELECT code, last_at FROM (
			SELECT code AS code, last_at FROM name_leads
			UNION ALL
			SELECT member_code AS code, last_at FROM name_pairs
		) ORDER BY last_at DESC, code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []string
	for rows.Next() {
		var code string
		var at int64
		if err := rows.Scan(&code, &at); err != nil {
			return nil, err
		}
		if seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code)
		if len(out) == limit {
			break
		}
	}
	return out, rows.Err()
}
