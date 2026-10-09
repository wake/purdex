package workbook

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/redact"
)

// Todo states and who closed one (spec §5.5). 'user' is reserved for a later hand edit and is not written.
const (
	TodoOpen    = "open"
	TodoDone    = "done"
	TodoDropped = "dropped"

	ClosedByModel   = "model"
	ClosedByRefresh = "refresh"
)

// Limits of the todo list (spec §5.4, plan D13).
const (
	maxOpenTodos      = 30
	maxTodoTitle      = 30
	maxTodoDetail     = 100
	maxAddsPerTurn    = 2
	maxAddsPerRefresh = 10
)

// Todo is one row of the conversation's list. ClosedEntryID is 0 while it is open.
type Todo struct {
	ID            int64
	ConvKey       string
	Title         string
	Detail        string
	State         string
	AddedEntryID  int64
	ClosedEntryID int64
	ClosedBy      string
	CreatedAt     int64
	ClosedAt      int64
	UpdatedAt     int64
}

// TodoAdd is what a result asks to add.
type TodoAdd struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// TodoChanges are a result's todo changes with the numbers already read through the job's map: Done and Dropped are
// todo ids.
type TodoChanges struct {
	Done, Dropped []int64
	Adds          []TodoAdd
}

// TodoResult is what applying a TodoChanges did: the rows that changed (closings first, in the order given, then the
// adds), how many adds the 30-open cap turned away, and how many had no title.
type TodoResult struct {
	Changed    []Todo
	CapIgnored int
	EmptyTitle int
}

// Usage is the token count of a call (or of both calls of an entry).
type Usage struct{ In, Out, CacheRead int64 }

// execer is what the todo code needs from a *sql.Tx.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// inTx runs fn in one transaction.
func (s *Store) inTx(fn func(tx execer) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

const todoCols = `id, conv_key, title, detail, state, added_entry_id, COALESCE(closed_entry_id, 0), closed_by, created_at, closed_at, updated_at`

func scanTodo(r scanner) (Todo, error) {
	var t Todo
	err := r.Scan(&t.ID, &t.ConvKey, &t.Title, &t.Detail, &t.State, &t.AddedEntryID, &t.ClosedEntryID, &t.ClosedBy, &t.CreatedAt, &t.ClosedAt, &t.UpdatedAt)
	return t, err
}

func queryTodos(q interface {
	Query(query string, args ...any) (*sql.Rows, error)
}, query string, args ...any) ([]Todo, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("read workbook todos: %w", err)
	}
	defer rows.Close()
	var out []Todo
	for rows.Next() {
		t, err := scanTodo(rows)
		if err != nil {
			return nil, fmt.Errorf("read workbook todos: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// OpenTodos is the conversation's open todos, oldest first, at most limit (the prompt takes 30).
func (s *Store) OpenTodos(convKey string, limit int) ([]Todo, error) {
	return queryTodos(s.db, `SELECT `+todoCols+` FROM wb_todos WHERE conv_key = ? AND state = 'open' ORDER BY id ASC LIMIT ?`, convKey, limit)
}

// Todos lists a conversation's todos of one state, newest first; beforeID > 0 returns the ones with a smaller id (the done
// record's paging).
func (s *Store) Todos(convKey, state string, limit int, beforeID int64) ([]Todo, error) {
	if beforeID <= 0 {
		return queryTodos(s.db, `SELECT `+todoCols+` FROM wb_todos WHERE conv_key = ? AND state = ? ORDER BY id DESC LIMIT ?`, convKey, state, limit)
	}
	return queryTodos(s.db, `SELECT `+todoCols+` FROM wb_todos WHERE conv_key = ? AND state = ? AND id < ? ORDER BY id DESC LIMIT ?`, convKey, state, beforeID, limit)
}

// applyTodoChanges is spec §5.4 v2 / plan D13, inside the caller's transaction. Closings first: a number that is not an
// open todo of this conversation is ignored, and a todo in both lists counts as done. Then the adds one by one: the first
// 2 (10 for a refresh), title cut at 30, detail at the last sentence end ≤ 100 (none: at 100), an empty title or one equal
// to an open title (including one added a moment ago) ignored, and the 30-open cap re-checked before each add.
func applyTodoChanges(tx execer, convKey string, entryID int64, ch TodoChanges, by string, now int64) (TodoResult, error) {
	var res TodoResult
	isDone := map[int64]bool{}
	for _, id := range ch.Done {
		isDone[id] = true
	}
	closeOne := func(id int64, state string) error {
		r, err := tx.Exec(`UPDATE wb_todos SET state = ?, closed_entry_id = ?, closed_by = ?, closed_at = ?, updated_at = ?
			WHERE id = ? AND conv_key = ? AND state = 'open'`, state, entryID, by, now, now, id, convKey)
		if err != nil {
			return fmt.Errorf("close workbook todo %d: %w", id, err)
		}
		if n, _ := r.RowsAffected(); n == 1 {
			t, err := scanTodo(tx.QueryRow(`SELECT `+todoCols+` FROM wb_todos WHERE id = ?`, id))
			if err != nil {
				return fmt.Errorf("read workbook todo %d: %w", id, err)
			}
			res.Changed = append(res.Changed, t)
		}
		return nil
	}
	for _, id := range ch.Done {
		if err := closeOne(id, TodoDone); err != nil {
			return TodoResult{}, err
		}
	}
	for _, id := range ch.Dropped {
		if isDone[id] {
			continue
		}
		if err := closeOne(id, TodoDropped); err != nil {
			return TodoResult{}, err
		}
	}

	max := maxAddsPerTurn
	if by == ClosedByRefresh {
		max = maxAddsPerRefresh
	}
	adds := ch.Adds
	if len(adds) > max {
		adds = adds[:max]
	}
	if len(adds) == 0 {
		return res, nil
	}
	open, err := queryTodos(tx, `SELECT `+todoCols+` FROM wb_todos WHERE conv_key = ? AND state = 'open'`, convKey)
	if err != nil {
		return TodoResult{}, err
	}
	titles := map[string]bool{}
	for _, t := range open {
		titles[strings.TrimSpace(t.Title)] = true
	}
	openCount := len(open)
	for _, a := range adds {
		title := cutRunes(strings.TrimSpace(redact.String(a.Title)), maxTodoTitle)
		title = strings.TrimSpace(title)
		if title == "" { // an add with no title (absent or blank) is nothing; the caller logs the count
			res.EmptyTitle++
			continue
		}
		if titles[title] {
			continue
		}
		if openCount >= maxOpenTodos {
			res.CapIgnored++
			continue
		}
		detail := cutAtSentenceEnd(strings.TrimSpace(redact.String(a.Detail)), maxTodoDetail)
		r, err := tx.Exec(`INSERT INTO wb_todos (conv_key, title, detail, state, added_entry_id, created_at, updated_at) VALUES (?, ?, ?, 'open', ?, ?, ?)`,
			convKey, title, detail, entryID, now, now)
		if err != nil {
			return TodoResult{}, fmt.Errorf("add workbook todo: %w", err)
		}
		id, _ := r.LastInsertId()
		t, err := scanTodo(tx.QueryRow(`SELECT `+todoCols+` FROM wb_todos WHERE id = ?`, id))
		if err != nil {
			return TodoResult{}, fmt.Errorf("read workbook todo %d: %w", id, err)
		}
		res.Changed = append(res.Changed, t)
		titles[title] = true
		openCount++
	}
	return res, nil
}

// PushLineV2 is the result of a turn job once it is validated (plan D3): everything the push waits for, written in one
// transaction. By is who the todo changes are recorded as (model for a turn).
type PushLineV2 struct {
	Thing, Push, Status string
	Usage               Usage
	Todos               TodoChanges
	By                  string
}

// SetPushLineV2 writes thing, push, push_ready_at and the usage of a pending entry, the conversation's status and the
// todo changes in ONE transaction; the entry stays pending (the re-write may follow). It returns the todo result: the rows that
// changed, and how many adds the 30-open cap turned away (the caller logs that once). ok is false, and nothing is written, when the entry is not pending any more. A status event follows the commit.
func (s *Store) SetPushLineV2(entryID int64, p PushLineV2) (res TodoResult, ok bool, err error) {
	s.wmu.Lock() // events follow commit order
	defer s.wmu.Unlock()
	var conv, session string
	now := s.now()
	err = s.inTx(func(tx execer) error {
		r, err := tx.Exec(`UPDATE wb_entries SET thing = ?, push = ?, push_ready_at = ?, usage_in = ?, usage_out = ?, usage_cache_read = ?, updated_at = ?
			WHERE id = ? AND state = 'pending'`, p.Thing, p.Push, now, nullInt(p.Usage.In), nullInt(p.Usage.Out), nullInt(p.Usage.CacheRead), now, entryID)
		if err != nil {
			return fmt.Errorf("set workbook push line: %w", err)
		}
		if n, _ := r.RowsAffected(); n != 1 {
			return errNotPending
		}
		if err := tx.QueryRow(`SELECT conv_key, session_id FROM wb_entries WHERE id = ?`, entryID).Scan(&conv, &session); err != nil {
			return fmt.Errorf("read workbook entry %d: %w", entryID, err)
		}
		if _, err := tx.Exec(`INSERT INTO wb_status (conv_key, status, entry_id, session_id, updated_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (conv_key) DO UPDATE SET status = excluded.status, entry_id = excluded.entry_id,
				session_id = excluded.session_id, updated_at = excluded.updated_at`, conv, p.Status, entryID, session, now); err != nil {
			return fmt.Errorf("set workbook status: %w", err)
		}
		res, err = applyTodoChanges(tx, conv, entryID, p.Todos, p.By, now)
		return err
	})
	if errors.Is(err, errNotPending) {
		return TodoResult{}, false, nil
	}
	if err != nil {
		return TodoResult{}, false, err
	}
	s.emit(Event{Kind: EventStatus, ConvKey: conv, SessionID: session,
		Status: StatusRow{ConvKey: conv, Status: p.Status, EntryID: entryID, SessionID: session, UpdatedAt: now}})
	s.emitTodos(conv, session, res.Changed)
	return res, true, nil
}

// FinishSkippedV2 turns a pending entry into skipped (reason, e.g. "model") and applies its todo changes in the same
// transaction: a turn with no progress may still answer an open question (spec §5.4). ok is false when the entry is not
// pending.
func (s *Store) FinishSkippedV2(entryID int64, reason string, u Usage, latencyMS int64, ch TodoChanges, by string) (res TodoResult, ok bool, err error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	var conv, session string
	now := s.now()
	err = s.inTx(func(tx execer) error {
		r, err := tx.Exec(`UPDATE wb_entries SET state = 'skipped', reason = ?, usage_in = ?, usage_out = ?, usage_cache_read = ?, latency_ms = ?, updated_at = ?
			WHERE id = ? AND state = 'pending'`, reason, nullInt(u.In), nullInt(u.Out), nullInt(u.CacheRead), latencyMS, now, entryID)
		if err != nil {
			return fmt.Errorf("finish workbook entry: %w", err)
		}
		if n, _ := r.RowsAffected(); n != 1 {
			return errNotPending
		}
		if err := tx.QueryRow(`SELECT conv_key, session_id FROM wb_entries WHERE id = ?`, entryID).Scan(&conv, &session); err != nil {
			return fmt.Errorf("read workbook entry %d: %w", entryID, err)
		}
		res, err = applyTodoChanges(tx, conv, entryID, ch, by, now)
		return err
	})
	if errors.Is(err, errNotPending) {
		return TodoResult{}, false, nil
	}
	if err != nil {
		return TodoResult{}, false, err
	}
	s.emitEntry(entryID)
	s.emitTodos(conv, session, res.Changed)
	return res, true, nil
}

// EntryTodoChanges is what one entry did to the list (spec §9): the todos it added, and the ones it closed.
type EntryTodoChanges struct {
	Added, Done, Dropped []Todo
}

// TodoChangesByEntry reads, for the given entries, the todos each added and closed. An entry that changed nothing has no
// key. Both lists are in todo id order (the order the entry applied them in is closings first, but ids keep it stable).
func (s *Store) TodoChangesByEntry(entryIDs []int64) (map[int64]EntryTodoChanges, error) {
	out := map[int64]EntryTodoChanges{}
	if len(entryIDs) == 0 {
		return out, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(entryIDs)), ",")
	args := make([]any, 0, 2*len(entryIDs))
	for i := 0; i < 2; i++ {
		for _, id := range entryIDs {
			args = append(args, id)
		}
	}
	rows, err := queryTodos(s.db, `SELECT `+todoCols+` FROM wb_todos WHERE added_entry_id IN (`+marks+`) OR closed_entry_id IN (`+marks+`) ORDER BY id ASC`, args...)
	if err != nil {
		return nil, err
	}
	want := map[int64]bool{}
	for _, id := range entryIDs {
		want[id] = true
	}
	for _, t := range rows {
		if want[t.AddedEntryID] {
			c := out[t.AddedEntryID]
			c.Added = append(c.Added, t)
			out[t.AddedEntryID] = c
		}
		if t.ClosedEntryID != 0 && want[t.ClosedEntryID] {
			c := out[t.ClosedEntryID]
			if t.State == TodoDone {
				c.Done = append(c.Done, t)
			} else if t.State == TodoDropped {
				c.Dropped = append(c.Dropped, t)
			}
			out[t.ClosedEntryID] = c
		}
	}
	return out, nil
}

var errNotPending = errors.New("workbook entry is not pending")

// nullInt stores 0 as NULL (no usage recorded).
func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
