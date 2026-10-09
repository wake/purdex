package workbook

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Entry states (spec §6).
const (
	StatePending = "pending"
	StateOK      = "ok"
	StateFailed  = "failed"
	StateSkipped = "skipped"
)

// ReasonStopped: the daemon stopped or restarted while the entry was pending (plan D9).
const ReasonStopped = "stopped"

const (
	dbFileMode    = 0o600
	schemaVersion = 2
)

// Entry is one summarised turn (spec §6). Every time is unix milliseconds.
type Entry struct {
	ID          int64
	ConvKey     string
	HostID      string
	Provider    string
	SessionID   string
	TurnID      string
	TurnAt      int64
	TurnSeq     int64
	State       string
	Reason      string
	Thing       string
	Push        string
	Entry       string
	ThingDone   bool
	PushReadyAt int64
	TeamID      string
	Role        string
	Ref         string
	PromptVer   int
	LatencyMS   int64
	CreatedAt   int64
	UpdatedAt   int64

	// v2: the kind (turn | refresh) and the tokens of the call(s); 0 = none recorded.
	Kind           string
	UsageIn        int64
	UsageOut       int64
	UsageCacheRead int64
}

// Entry kinds.
const (
	KindTurn    = "turn"
	KindRefresh = "refresh"
)

// Output is what a finished call leaves on its entry. An ok entry takes every field; a failed or skipped one only its
// latency (its thing / push, if the push line was already written, stay as they are).
type Output struct {
	Thing, Push, Entry string
	ThingDone          bool
	LatencyMS          int64
	Usage              Usage // tokens of the call(s); zero leaves what the push line recorded
}

// StatusRow is a conversation's current one-line status.
type StatusRow struct {
	ConvKey   string
	Status    string
	EntryID   int64
	SessionID string
	UpdatedAt int64
}

// Store is workbook.db: the summaries, owner-only, one file.
type Store struct {
	db          *sql.DB
	now         func() int64 // unix ms; injectable for tests
	obs         atomic.Pointer[func(Event)]
	failFinish  func() error // test seam: makes Finish fail; nil in production
	failRepoint func() error // test seam: makes RepointSession fail; nil in production
	afterCommit func()       // test seam: between InsertPending's commit and its event; nil in production
	wmu         sync.Mutex   // serialises the writes together with their events; an observer must not write to the store
}

// OpenStore opens (or creates) the store at path. The file and its WAL siblings are owner-only.
func OpenStore(path string) (*Store, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, dbFileMode)
	if err != nil {
		return nil, errors.New("open workbook db: cannot create the file")
	}
	f.Close()
	if err := os.Chmod(path, dbFileMode); err != nil {
		return nil, errors.New("open workbook db: cannot restrict the file")
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open workbook db: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Chmod(path+suffix, dbFileMode); err != nil && !errors.Is(err, os.ErrNotExist) {
			db.Close()
			return nil, errors.New("open workbook db: cannot restrict a sidecar file")
		}
	}
	return &Store{db: db, now: func() int64 { return time.Now().UnixMilli() }}, nil
}

func (s *Store) Close() error { return s.db.Close() }

const entryCols = `id, conv_key, host_id, provider, session_id, turn_id, turn_at, turn_seq, state, reason,
	COALESCE(thing, ''), COALESCE(push, ''), COALESCE(entry, ''), thing_done, push_ready_at,
	COALESCE(team_id, ''), COALESCE(role, ''), COALESCE(ref, ''), prompt_ver, COALESCE(latency_ms, 0), created_at, updated_at,
	kind, COALESCE(usage_in, 0), COALESCE(usage_out, 0), COALESCE(usage_cache_read, 0)`

type scanner interface{ Scan(dest ...any) error }

func scanEntry(r scanner) (Entry, error) {
	var e Entry
	var done int
	err := r.Scan(&e.ID, &e.ConvKey, &e.HostID, &e.Provider, &e.SessionID, &e.TurnID, &e.TurnAt, &e.TurnSeq, &e.State, &e.Reason,
		&e.Thing, &e.Push, &e.Entry, &done, &e.PushReadyAt, &e.TeamID, &e.Role, &e.Ref, &e.PromptVer, &e.LatencyMS, &e.CreatedAt, &e.UpdatedAt,
		&e.Kind, &e.UsageIn, &e.UsageOut, &e.UsageCacheRead)
	e.ThingDone = done != 0
	return e, err
}

func (s *Store) queryEntries(query string, args ...any) ([]Entry, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("read workbook entries: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("read workbook entries: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// InsertPending records a turn as pending. It is idempotent on (session_id, turn_id): a turn that is already recorded
// inserts nothing and answers the existing row's id with inserted false.
func (s *Store) InsertPending(e Entry) (id int64, inserted bool, err error) {
	s.wmu.Lock() // one writer at a time, and its event goes out before the next write starts: events follow commit order
	defer s.wmu.Unlock()
	if e.ConvKey == "" || e.SessionID == "" || e.TurnID == "" {
		return 0, false, errors.New("insert workbook entry: conversation key, session and turn id are required")
	}
	now := s.now()
	kind := e.Kind
	if kind == "" {
		kind = KindTurn
	}
	res, err := s.db.Exec(`INSERT INTO wb_entries (conv_key, host_id, provider, session_id, turn_id, turn_at, turn_seq, state,
			team_id, role, ref, prompt_ver, created_at, updated_at, kind)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (session_id, turn_id) DO NOTHING`,
		e.ConvKey, e.HostID, e.Provider, e.SessionID, e.TurnID, e.TurnAt, e.TurnSeq, e.TeamID, e.Role, e.Ref, e.PromptVer, now, now, kind)
	if err != nil {
		return 0, false, fmt.Errorf("insert workbook entry: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		id, err = res.LastInsertId()
		if err == nil {
			if s.afterCommit != nil {
				s.afterCommit()
			}
			s.emitEntry(id)
		}
		return id, true, err
	}
	if err := s.db.QueryRow(`SELECT id FROM wb_entries WHERE session_id = ? AND turn_id = ?`, e.SessionID, e.TurnID).Scan(&id); err != nil {
		return 0, false, fmt.Errorf("insert workbook entry: %w", err)
	}
	return id, false, nil
}

// SetPushLine writes the final thing and push of a pending entry and stamps push_ready_at; the entry stays pending
// (spec §5.4). push may be "" when validation dropped it. false: the entry is not pending any more.
func (s *Store) SetPushLine(id int64, thing, push string) (bool, error) {
	s.wmu.Lock() // one writer at a time, and its event goes out before the next write starts: events follow commit order
	defer s.wmu.Unlock()
	now := s.now()
	res, err := s.db.Exec(`UPDATE wb_entries SET thing = ?, push = ?, push_ready_at = ?, updated_at = ? WHERE id = ? AND state = 'pending'`,
		thing, push, now, now, id)
	if err != nil {
		return false, fmt.Errorf("set workbook push line: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Finish moves a pending entry to ok, failed or skipped. false: it was not pending (a final entry never moves again).
func (s *Store) Finish(id int64, state, reason string, out Output) (bool, error) {
	s.wmu.Lock() // one writer at a time, and its event goes out before the next write starts: events follow commit order
	defer s.wmu.Unlock()
	if s.failFinish != nil { // test seam
		if err := s.failFinish(); err != nil {
			return false, err
		}
	}
	now := s.now()
	var res sql.Result
	var err error
	switch state {
	case StateOK:
		res, err = s.db.Exec(`UPDATE wb_entries SET state = 'ok', reason = '', thing = ?, push = ?, entry = ?, thing_done = ?,
				latency_ms = ?, usage_in = COALESCE(?, usage_in), usage_out = COALESCE(?, usage_out),
				usage_cache_read = COALESCE(?, usage_cache_read), updated_at = ? WHERE id = ? AND state = 'pending'`,
			out.Thing, out.Push, out.Entry, boolInt(out.ThingDone), out.LatencyMS,
			nullInt(out.Usage.In), nullInt(out.Usage.Out), nullInt(out.Usage.CacheRead), now, id)
	case StateFailed, StateSkipped:
		res, err = s.db.Exec(`UPDATE wb_entries SET state = ?, reason = ?, latency_ms = ?, updated_at = ? WHERE id = ? AND state = 'pending'`,
			state, reason, out.LatencyMS, now, id)
	default:
		return false, fmt.Errorf("finish workbook entry: %q is not a final state", state)
	}
	if err != nil {
		return false, fmt.Errorf("finish workbook entry: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		s.emitEntry(id)
	}
	return n == 1, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// FailPending turns every pending entry into failed:stopped (a crash or restart left them; plan D9). It returns how many.
func (s *Store) FailPending() (int, error) {
	s.wmu.Lock() // one writer at a time, and its event goes out before the next write starts: events follow commit order
	defer s.wmu.Unlock()
	res, err := s.db.Exec(`UPDATE wb_entries SET state = 'failed', reason = ?, updated_at = ? WHERE state = 'pending'`, ReasonStopped, s.now())
	if err != nil {
		return 0, fmt.Errorf("fail pending workbook entries: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Entry reads one entry by id.
func (s *Store) Entry(id int64) (Entry, error) {
	e, err := scanEntry(s.db.QueryRow(`SELECT `+entryCols+` FROM wb_entries WHERE id = ?`, id))
	if err != nil {
		return Entry{}, fmt.Errorf("read workbook entry %d: %w", id, err)
	}
	return e, nil
}

// NewestTurn is the newest recorded turn of a session (the catch-up's cursor); ok is false when it has none.
func (s *Store) NewestTurn(sessionID string) (turnID string, turnAt int64, ok bool, err error) {
	err = s.db.QueryRow(`SELECT turn_id, turn_at FROM wb_entries WHERE session_id = ? ORDER BY id DESC LIMIT 1`, sessionID).Scan(&turnID, &turnAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("read newest workbook turn: %w", err)
	}
	return turnID, turnAt, true, nil
}

// ClosestTurn is the session's entry whose turn_at is nearest to `at` within [at-before, at+after] (the push hold's match: a
// Stop's own entry carries the event's time); ok is false when there is none. Two Stops a moment apart each find their own.
func (s *Store) ClosestTurn(sessionID string, at, before, after int64) (Entry, bool, error) {
	e, err := scanEntry(s.db.QueryRow(`SELECT `+entryCols+` FROM wb_entries WHERE session_id = ? AND turn_at BETWEEN ? AND ?
		ORDER BY ABS(turn_at - ?) ASC, id DESC LIMIT 1`, sessionID, at-before, at+after, at))
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("read workbook entry near a stop: %w", err)
	}
	return e, true, nil
}

// Conversation lists a conversation's entries newest first; beforeID > 0 returns the entries with a smaller id.
func (s *Store) Conversation(convKey string, limit int, beforeID int64) ([]Entry, error) {
	if beforeID <= 0 {
		return s.queryEntries(`SELECT `+entryCols+` FROM wb_entries WHERE conv_key = ? ORDER BY id DESC LIMIT ?`, convKey, limit)
	}
	return s.queryEntries(`SELECT `+entryCols+` FROM wb_entries WHERE conv_key = ? AND id < ? ORDER BY id DESC LIMIT ?`, convKey, beforeID, limit)
}

// HasEntries reports whether the conversation has any entry (whatever its state).
func (s *Store) HasEntries(convKey string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM wb_entries WHERE conv_key = ? LIMIT 1`, convKey).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read workbook entries: %w", err)
	}
	return true, nil
}

// Known reports whether the workbook has ever written anything for the conversation: an entry (whatever its state) or a
// status. The conversation route's 404 rule, shared by the todos route.
func (s *Store) Known(convKey string) (bool, error) {
	has, err := s.HasEntries(convKey)
	if err != nil || has {
		return has, err
	}
	_, ok, err := s.Status(convKey)
	return ok, err
}

// Entries lists entries across conversations newest first. since is inclusive and until exclusive, on turn_at (0 = no
// bound); thingDone keeps only the entries whose turn finished a thing.
func (s *Store) Entries(since, until int64, thingDone bool, limit int) ([]Entry, error) {
	q := `SELECT ` + entryCols + ` FROM wb_entries WHERE turn_at >= ?`
	args := []any{since}
	if until > 0 {
		q += ` AND turn_at < ?`
		args = append(args, until)
	}
	if thingDone {
		q += ` AND thing_done = 1`
	}
	q += ` ORDER BY id DESC LIMIT ?`
	return s.queryEntries(q, append(args, limit)...)
}

// RecentForPrompt is the last n ok entries of a conversation, oldest first (what the next call is told).
func (s *Store) RecentForPrompt(convKey string, n int) ([]Entry, error) {
	rows, err := s.queryEntries(`SELECT `+entryCols+` FROM wb_entries WHERE conv_key = ? AND state = 'ok' ORDER BY id DESC LIMIT ?`, convKey, n)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, nil
}

// SetStatus writes a conversation's current status.
func (s *Store) SetStatus(convKey, status string, entryID int64, sessionID string) error {
	s.wmu.Lock() // one writer at a time, and its event goes out before the next write starts: events follow commit order
	defer s.wmu.Unlock()
	at := s.now()
	_, err := s.db.Exec(`INSERT INTO wb_status (conv_key, status, entry_id, session_id, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (conv_key) DO UPDATE SET status = excluded.status, entry_id = excluded.entry_id,
			session_id = excluded.session_id, updated_at = excluded.updated_at`, convKey, status, entryID, sessionID, at)
	if err != nil {
		return fmt.Errorf("set workbook status: %w", err)
	}
	s.emit(Event{Kind: EventStatus, ConvKey: convKey, SessionID: sessionID,
		Status: StatusRow{ConvKey: convKey, Status: status, EntryID: entryID, SessionID: sessionID, UpdatedAt: at}})
	return nil
}

// Status reads a conversation's status; ok is false when none was ever written.
func (s *Store) Status(convKey string) (StatusRow, bool, error) {
	r := StatusRow{ConvKey: convKey}
	err := s.db.QueryRow(`SELECT status, entry_id, session_id, updated_at FROM wb_status WHERE conv_key = ?`, convKey).
		Scan(&r.Status, &r.EntryID, &r.SessionID, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return StatusRow{}, false, nil
	}
	if err != nil {
		return StatusRow{}, false, fmt.Errorf("read workbook status: %w", err)
	}
	return r, true, nil
}
