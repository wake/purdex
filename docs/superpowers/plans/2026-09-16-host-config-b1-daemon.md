# Host Config (B1: daemon) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a daemon module `hostconfig` that stores per-host Projects, Commands and Resume templates with optimistic concurrency, plus a path-check endpoint.

**Architecture:** New package `internal/module/hostconfig` shaped like `internal/module/devicestate` (Module + SQLite Store + handlers + validators). One key/value table holding JSON per collection with a revision number; PUT replaces a whole collection guarded by `baseRevision` in a single transaction.

**Tech Stack:** Go 1.26, net/http ServeMux method patterns, modernc.org/sqlite, testify.

**Spec:** `docs/superpowers/specs/2026-09-16-host-projects-commands-launcher-design.md` §3

## Global Constraints

- Package path: `github.com/wake/purdex/internal/module/hostconfig`; DB file `DataDir/host_config.db` (WAL; `:memory:` for tests with `SetMaxOpenConns(1)`).
- Routes: `GET /api/hostconfig`, `PUT /api/hostconfig/projects`, `PUT /api/hostconfig/commands`, `PUT /api/hostconfig/resume-templates`, `POST /api/hostconfig/check-path`.
- Errors: plain text via `http.Error` (400 validation, 413 body > 1 MB, 409 revision conflict returns JSON `{items, revision}`, 500 `internal error`).
- Validation limits (verbatim from spec §3.3): id `^[A-Za-z0-9_-]{1,64}$` unique; project name trimmed 1–64 runes; slug `^[a-z0-9][a-z0-9-]{0,31}$` unique; path trimmed 1–1024 bytes, starts with `/` or is `~` or starts with `~/`, no NUL; command name trimmed 1–64 runes; command 1–4096 bytes no NUL; icon kind `agent` (value ∈ cc-bot, cc-star, openai, codex, opencode) or `phosphor` (value `^[A-Z][A-Za-z0-9]{0,63}$`); max 200 items per list; resume agentType `^[a-z0-9][a-z0-9_-]{0,31}$`, max 32 entries, exact/fallback 0–4096 bytes no NUL.
- Every Bash command in a subagent must be prefixed with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && `.
- Commits end with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/module/hostconfig/module.go` | Module lifecycle, route registration |
| `internal/module/hostconfig/store.go` | SQLite key/value with revision CAS |
| `internal/module/hostconfig/validate.go` | Types + validation/normalisation of the three collections |
| `internal/module/hostconfig/handler.go` | HTTP handlers (GET, 3×PUT, check-path) |
| `internal/module/hostconfig/checkpath.go` | `~` expansion + stat classification |
| `*_test.go` next to each | Tests |
| `cmd/pdx/main.go` | `c.AddModule(hostconfigmod.New())` |

---

### Task 1: Store with revision CAS

**Files:**
- Create: `internal/module/hostconfig/store.go`
- Test: `internal/module/hostconfig/store_test.go`

**Interfaces:**
- Produces:
  - `const KeyProjects = "projects"`, `KeyCommands = "commands"`, `KeyResumeTemplates = "resume_templates"`
  - `func OpenStore(path string) (*Store, error)`, `func (s *Store) Close() error`
  - `func (s *Store) Get(key string) (Entry, error)` — missing row → `Entry{Value: nil, Revision: 0}`
  - `func (s *Store) Put(key string, value json.RawMessage, baseRevision int64) (Entry, bool, error)` — returns (current entry, ok). ok=false means conflict and Entry is the stored current state (unchanged). ok=true means Entry is the newly written state.
  - `type Entry struct { Value json.RawMessage; Revision int64; UpdatedAt int64 }`

- [ ] **Step 1: Write the failing test**

```go
package hostconfig

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	s.now = func() int64 { return 1000 }
	return s
}

func TestStoreGetMissingIsRevisionZero(t *testing.T) {
	s := openTestStore(t)
	e, err := s.Get(KeyProjects)
	require.NoError(t, err)
	assert.Nil(t, e.Value)
	assert.Equal(t, int64(0), e.Revision)
}

func TestStorePutFirstWriteRevisionOne(t *testing.T) {
	s := openTestStore(t)
	e, ok, err := s.Put(KeyProjects, json.RawMessage(`[{"id":"a"}]`), 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, int64(1), e.Revision)
	assert.JSONEq(t, `[{"id":"a"}]`, string(e.Value))

	got, err := s.Get(KeyProjects)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.Revision)
	assert.JSONEq(t, `[{"id":"a"}]`, string(got.Value))
	assert.Equal(t, int64(1000), got.UpdatedAt)
}

func TestStorePutConflictLeavesRowUntouched(t *testing.T) {
	s := openTestStore(t)
	_, ok, err := s.Put(KeyCommands, json.RawMessage(`[1]`), 0)
	require.NoError(t, err)
	require.True(t, ok)

	cur, ok, err := s.Put(KeyCommands, json.RawMessage(`[2]`), 0) // stale base
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, int64(1), cur.Revision)
	assert.JSONEq(t, `[1]`, string(cur.Value))

	next, ok, err := s.Put(KeyCommands, json.RawMessage(`[3]`), 1)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, int64(2), next.Revision)
}

func TestStoreKeysAreIndependent(t *testing.T) {
	s := openTestStore(t)
	_, _, err := s.Put(KeyProjects, json.RawMessage(`[]`), 0)
	require.NoError(t, err)
	e, err := s.Get(KeyResumeTemplates)
	require.NoError(t, err)
	assert.Equal(t, int64(0), e.Revision)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/hostconfig/ -run TestStore -v`
Expected: FAIL (package does not compile: `OpenStore` undefined)

- [ ] **Step 3: Write minimal implementation**

```go
// Package hostconfig stores per-host launcher configuration — projects,
// commands and resume templates — so every client sees the same set.
package hostconfig

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const (
	KeyProjects        = "projects"
	KeyCommands        = "commands"
	KeyResumeTemplates = "resume_templates"
)

// Entry is one stored collection. A missing row reads as Revision 0, Value nil.
type Entry struct {
	Value     json.RawMessage
	Revision  int64
	UpdatedAt int64
}

// Store is the SQLite-backed persistence layer for host config.
type Store struct {
	db  *sql.DB
	now func() int64 // ms; injectable for tests
}

// OpenStore opens (or creates) a Store at path. Use ":memory:" for tests.
func OpenStore(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(wal)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open host config db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	s := &Store{db: db, now: func() int64 { return time.Now().UnixMilli() }}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS host_config (
			key        TEXT PRIMARY KEY,
			value      TEXT    NOT NULL,
			revision   INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate host config db: %w", err)
	}
	return s, nil
}

// Close closes the underlying DB connection.
func (s *Store) Close() error { return s.db.Close() }

type querier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func getEntry(q querier, key string) (Entry, error) {
	var e Entry
	var value string
	err := q.QueryRow(`SELECT value, revision, updated_at FROM host_config WHERE key = ?`, key).
		Scan(&value, &e.Revision, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, nil
	}
	if err != nil {
		return Entry{}, fmt.Errorf("get host config %s: %w", key, err)
	}
	e.Value = json.RawMessage(value)
	return e, nil
}

// Get returns the stored entry for key (Revision 0 when never written).
func (s *Store) Get(key string) (Entry, error) { return getEntry(s.db, key) }

// Put writes value when baseRevision equals the stored revision, in one
// transaction. On mismatch it returns the current entry and ok=false.
func (s *Store) Put(key string, value json.RawMessage, baseRevision int64) (Entry, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Entry{}, false, fmt.Errorf("begin host config tx: %w", err)
	}
	defer tx.Rollback()

	cur, err := getEntry(tx, key)
	if err != nil {
		return Entry{}, false, err
	}
	if cur.Revision != baseRevision {
		return cur, false, nil
	}
	next := Entry{Value: value, Revision: cur.Revision + 1, UpdatedAt: s.now()}
	if _, err := tx.Exec(`
		INSERT INTO host_config (key, value, revision, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, revision = excluded.revision, updated_at = excluded.updated_at`,
		key, string(value), next.Revision, next.UpdatedAt); err != nil {
		return Entry{}, false, fmt.Errorf("put host config %s: %w", key, err)
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, false, fmt.Errorf("commit host config %s: %w", key, err)
	}
	return next, true, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/hostconfig/ -run TestStore -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/module/hostconfig/store.go internal/module/hostconfig/store_test.go
git commit -m "feat(daemon): hostconfig store with revision CAS"
```

---

### Task 2: Types and validation

**Files:**
- Create: `internal/module/hostconfig/validate.go`
- Test: `internal/module/hostconfig/validate_test.go`

**Interfaces:**
- Produces:
  - `type Project struct { ID, Name, Slug, Path string }` (json `id,name,slug,path`)
  - `type CommandIcon struct { Kind, Value string }` (json `kind,value`)
  - `type Command struct { ID, Name, Command string; Icon CommandIcon }` (json `id,name,command,icon`)
  - `type ResumeTemplatePair struct { Exact, Fallback string }` (json `exact,fallback`)
  - `func normalizeProjects(raw json.RawMessage) ([]Project, error)` — decodes, trims name/path, validates, returns normalised slice (never nil)
  - `func normalizeCommands(raw json.RawMessage) ([]Command, error)` — trims name only
  - `func normalizeResumeTemplates(raw json.RawMessage) (map[string]ResumeTemplatePair, error)` — never nil

- [ ] **Step 1: Write the failing test**

```go
package hostconfig

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeProjectsOK(t *testing.T) {
	got, err := normalizeProjects(json.RawMessage(`[
		{"id":"p1","name":"  Purdex ","slug":"purdex","path":" ~/Workspace/wake/purdex "},
		{"id":"p2","name":"Root","slug":"r-2","path":"/"},
		{"id":"p3","name":"Home","slug":"home","path":"~"}
	]`))
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "Purdex", got[0].Name)
	assert.Equal(t, "~/Workspace/wake/purdex", got[0].Path)
}

func TestNormalizeProjectsEmptyIsNonNil(t *testing.T) {
	got, err := normalizeProjects(json.RawMessage(`[]`))
	require.NoError(t, err)
	assert.NotNil(t, got)
}

func TestNormalizeProjectsRejects(t *testing.T) {
	long := strings.Repeat("a", 65)
	cases := map[string]string{
		"not array":     `{}`,
		"null":          `null`,
		"bad id":        `[{"id":"a b","name":"n","slug":"s1","path":"/"}]`,
		"dup id":        `[{"id":"a","name":"n","slug":"s1","path":"/"},{"id":"a","name":"n","slug":"s2","path":"/"}]`,
		"empty name":    `[{"id":"a","name":"  ","slug":"s1","path":"/"}]`,
		"long name":     `[{"id":"a","name":"` + long + `","slug":"s1","path":"/"}]`,
		"bad slug":      `[{"id":"a","name":"n","slug":"Bad","path":"/"}]`,
		"slug dash lead": `[{"id":"a","name":"n","slug":"-x","path":"/"}]`,
		"dup slug":      `[{"id":"a","name":"n","slug":"s","path":"/"},{"id":"b","name":"n","slug":"s","path":"/"}]`,
		"relative path": `[{"id":"a","name":"n","slug":"s1","path":"foo/bar"}]`,
		"tilde user":    `[{"id":"a","name":"n","slug":"s1","path":"~bob/x"}]`,
		"empty path":    `[{"id":"a","name":"n","slug":"s1","path":" "}]`,
		"nul path":      `[{"id":"a","name":"n","slug":"s1","path":"/a b"}]`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeProjects(json.RawMessage(raw))
			assert.Error(t, err)
		})
	}
}

func TestNormalizeProjectsMax200(t *testing.T) {
	items := make([]string, 201)
	for i := range items {
		items[i] = fmt.Sprintf(`{"id":"p%d","name":"n","slug":"s%d","path":"/"}`, i, i)
	}
	_, err := normalizeProjects(json.RawMessage("[" + strings.Join(items, ",") + "]"))
	assert.Error(t, err)
}

func TestNormalizeCommandsOK(t *testing.T) {
	got, err := normalizeCommands(json.RawMessage(`[
		{"id":"c1","name":" Claude ","command":"cld-yolo","icon":{"kind":"agent","value":"cc-bot"}},
		{"id":"c2","name":"Shell","command":"echo hi && ls","icon":{"kind":"phosphor","value":"Terminal"}}
	]`))
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "Claude", got[0].Name)
	assert.Equal(t, "cld-yolo", got[0].Command)
}

func TestNormalizeCommandsRejects(t *testing.T) {
	cases := map[string]string{
		"empty command":   `[{"id":"a","name":"n","command":"","icon":{"kind":"agent","value":"codex"}}]`,
		"long command":    `[{"id":"a","name":"n","command":"` + strings.Repeat("x", 4097) + `","icon":{"kind":"agent","value":"codex"}}]`,
		"nul command":     `[{"id":"a","name":"n","command":"a ","icon":{"kind":"agent","value":"codex"}}]`,
		"bad kind":        `[{"id":"a","name":"n","command":"x","icon":{"kind":"emoji","value":"x"}}]`,
		"bad agent":       `[{"id":"a","name":"n","command":"x","icon":{"kind":"agent","value":"gemini"}}]`,
		"bad phosphor":    `[{"id":"a","name":"n","command":"x","icon":{"kind":"phosphor","value":"terminal"}}]`,
		"dup id":          `[{"id":"a","name":"n","command":"x","icon":{"kind":"agent","value":"codex"}},{"id":"a","name":"n","command":"y","icon":{"kind":"agent","value":"codex"}}]`,
		"empty name":      `[{"id":"a","name":"","command":"x","icon":{"kind":"agent","value":"codex"}}]`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeCommands(json.RawMessage(raw))
			assert.Error(t, err)
		})
	}
}

func TestNormalizeResumeTemplates(t *testing.T) {
	got, err := normalizeResumeTemplates(json.RawMessage(`{"cc":{"exact":"cld --resume {id}","fallback":""}}`))
	require.NoError(t, err)
	assert.Equal(t, "cld --resume {id}", got["cc"].Exact)

	empty, err := normalizeResumeTemplates(json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.NotNil(t, empty)

	for name, raw := range map[string]string{
		"array":     `[]`,
		"null":      `null`,
		"bad agent": `{"CC":{"exact":"","fallback":""}}`,
		"too long":  `{"cc":{"exact":"` + strings.Repeat("x", 4097) + `","fallback":""}}`,
		"nul":       `{"cc":{"exact":"a ","fallback":""}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeResumeTemplates(json.RawMessage(raw))
			assert.Error(t, err)
		})
	}

	many := make([]string, 33)
	for i := range many {
		many[i] = fmt.Sprintf(`"a%d":{"exact":"","fallback":""}`, i)
	}
	_, err = normalizeResumeTemplates(json.RawMessage("{" + strings.Join(many, ",") + "}"))
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/hostconfig/ -run TestNormalize -v`
Expected: FAIL (`normalizeProjects` undefined)

- [ ] **Step 3: Write minimal implementation**

```go
package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxItems        = 200
	maxResumeAgents = 32
	nameMaxRunes    = 64
	pathMaxBytes    = 1024
	commandMaxBytes = 4096
)

var (
	idPattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	slugPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	phosphorPattern  = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{0,63}$`)
	agentTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	agentIconValues  = map[string]bool{"cc-bot": true, "cc-star": true, "openai": true, "codex": true, "opencode": true}
)

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Path string `json:"path"`
}

type CommandIcon struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type Command struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Command string      `json:"command"`
	Icon    CommandIcon `json:"icon"`
}

type ResumeTemplatePair struct {
	Exact    string `json:"exact"`
	Fallback string `json:"fallback"`
}

func firstByte(raw []byte) byte {
	t := bytes.TrimLeft(raw, " \t\r\n")
	if len(t) == 0 {
		return 0
	}
	return t[0]
}

func decodeArray(raw json.RawMessage, v any) error {
	if firstByte(raw) != '[' || json.Unmarshal(raw, v) != nil {
		return errors.New("items must be a JSON array")
	}
	return nil
}

func validName(field, v string) (string, error) {
	t := strings.TrimSpace(v)
	n := utf8.RuneCountInString(t)
	if n == 0 {
		return "", fmt.Errorf("%s is required", field)
	}
	if n > nameMaxRunes {
		return "", fmt.Errorf("%s too long", field)
	}
	return t, nil
}

func checkIDs(seen map[string]bool, id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid id %q", id)
	}
	if seen[id] {
		return fmt.Errorf("duplicate id %q", id)
	}
	seen[id] = true
	return nil
}

func normalizeProjects(raw json.RawMessage) ([]Project, error) {
	var in []Project
	if err := decodeArray(raw, &in); err != nil {
		return nil, err
	}
	if len(in) > maxItems {
		return nil, fmt.Errorf("at most %d projects", maxItems)
	}
	out := make([]Project, 0, len(in))
	ids, slugs := map[string]bool{}, map[string]bool{}
	for _, p := range in {
		if err := checkIDs(ids, p.ID); err != nil {
			return nil, err
		}
		name, err := validName("project name", p.Name)
		if err != nil {
			return nil, err
		}
		if !slugPattern.MatchString(p.Slug) {
			return nil, fmt.Errorf("invalid slug %q", p.Slug)
		}
		if slugs[p.Slug] {
			return nil, fmt.Errorf("duplicate slug %q", p.Slug)
		}
		slugs[p.Slug] = true
		path := strings.TrimSpace(p.Path)
		if path == "" || len(path) > pathMaxBytes || strings.ContainsRune(path, 0) {
			return nil, fmt.Errorf("invalid path for project %q", p.ID)
		}
		if !(strings.HasPrefix(path, "/") || path == "~" || strings.HasPrefix(path, "~/")) {
			return nil, fmt.Errorf("path must be absolute or start with ~/ (project %q)", p.ID)
		}
		out = append(out, Project{ID: p.ID, Name: name, Slug: p.Slug, Path: path})
	}
	return out, nil
}

func normalizeCommands(raw json.RawMessage) ([]Command, error) {
	var in []Command
	if err := decodeArray(raw, &in); err != nil {
		return nil, err
	}
	if len(in) > maxItems {
		return nil, fmt.Errorf("at most %d commands", maxItems)
	}
	out := make([]Command, 0, len(in))
	ids := map[string]bool{}
	for _, c := range in {
		if err := checkIDs(ids, c.ID); err != nil {
			return nil, err
		}
		name, err := validName("command name", c.Name)
		if err != nil {
			return nil, err
		}
		if c.Command == "" || len(c.Command) > commandMaxBytes || strings.ContainsRune(c.Command, 0) {
			return nil, fmt.Errorf("invalid command for %q", c.ID)
		}
		switch c.Icon.Kind {
		case "agent":
			if !agentIconValues[c.Icon.Value] {
				return nil, fmt.Errorf("invalid agent icon %q", c.Icon.Value)
			}
		case "phosphor":
			if !phosphorPattern.MatchString(c.Icon.Value) {
				return nil, fmt.Errorf("invalid phosphor icon %q", c.Icon.Value)
			}
		default:
			return nil, fmt.Errorf("invalid icon kind %q", c.Icon.Kind)
		}
		out = append(out, Command{ID: c.ID, Name: name, Command: c.Command, Icon: c.Icon})
	}
	return out, nil
}

func validTemplate(v string) bool {
	return len(v) <= commandMaxBytes && !strings.ContainsRune(v, 0)
}

func normalizeResumeTemplates(raw json.RawMessage) (map[string]ResumeTemplatePair, error) {
	var in map[string]ResumeTemplatePair
	if firstByte(raw) != '{' || json.Unmarshal(raw, &in) != nil {
		return nil, errors.New("items must be a JSON object")
	}
	if len(in) > maxResumeAgents {
		return nil, fmt.Errorf("at most %d agents", maxResumeAgents)
	}
	out := make(map[string]ResumeTemplatePair, len(in))
	for agent, pair := range in {
		if !agentTypePattern.MatchString(agent) {
			return nil, fmt.Errorf("invalid agent type %q", agent)
		}
		if !validTemplate(pair.Exact) || !validTemplate(pair.Fallback) {
			return nil, fmt.Errorf("invalid template for %q", agent)
		}
		out[agent] = pair
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/hostconfig/ -run TestNormalize -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/hostconfig/validate.go internal/module/hostconfig/validate_test.go
git commit -m "feat(daemon): hostconfig types and validation"
```

---

### Task 3: check-path classification

**Files:**
- Create: `internal/module/hostconfig/checkpath.go`
- Test: `internal/module/hostconfig/checkpath_test.go`

**Interfaces:**
- Produces:
  - `type PathCheck struct { Status, Resolved, Reason string }` (json `status,resolved,reason,omitempty`); Status ∈ `dir|not_dir|missing|error`
  - `func checkPath(path string, home func() (string, error)) (PathCheck, error)` — returns error only for invalid input (empty, NUL, relative after expansion, `~user` form, home lookup failure) → handler maps to 400.

- [ ] **Step 1: Write the failing test**

```go
package hostconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	home := func() (string, error) { return dir, nil }

	got, err := checkPath(dir, home)
	require.NoError(t, err)
	assert.Equal(t, "dir", got.Status)
	assert.Equal(t, dir, got.Resolved)

	got, err = checkPath(file, home)
	require.NoError(t, err)
	assert.Equal(t, "not_dir", got.Status)

	got, err = checkPath(filepath.Join(dir, "nope"), home)
	require.NoError(t, err)
	assert.Equal(t, "missing", got.Status)

	got, err = checkPath("~", home)
	require.NoError(t, err)
	assert.Equal(t, "dir", got.Status)
	assert.Equal(t, dir, got.Resolved)

	got, err = checkPath("~/f.txt", home)
	require.NoError(t, err)
	assert.Equal(t, "not_dir", got.Status)
	assert.Equal(t, file, got.Resolved)

	got, err = checkPath("  "+dir+"/./  ", home)
	require.NoError(t, err)
	assert.Equal(t, dir, got.Resolved)
}

func TestCheckPathRejects(t *testing.T) {
	home := func() (string, error) { return "/home/x", nil }
	for _, p := range []string{"", "   ", "relative/dir", "~bob", "/a\x00b"} {
		_, err := checkPath(p, home)
		assert.Error(t, err, p)
	}
	_, err := checkPath("~/x", func() (string, error) { return "", errors.New("no home") })
	assert.Error(t, err)
}

func TestCheckPathPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permissions")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	require.NoError(t, os.Mkdir(locked, 0o000))
	t.Cleanup(func() { os.Chmod(locked, 0o700) })

	got, err := checkPath(filepath.Join(locked, "child"), func() (string, error) { return dir, nil })
	require.NoError(t, err)
	assert.Equal(t, "error", got.Status)
	assert.NotEmpty(t, got.Reason)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/hostconfig/ -run TestCheckPath -v`
Expected: FAIL (`checkPath` undefined)

- [ ] **Step 3: Write minimal implementation**

```go
package hostconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PathCheck is the verdict for one project path on this host.
type PathCheck struct {
	Status   string `json:"status"` // dir | not_dir | missing | error
	Resolved string `json:"resolved"`
	Reason   string `json:"reason,omitempty"`
}

// checkPath expands a leading ~ with the daemon user's home (not a pane's
// $HOME) and classifies what is there. Input errors are returned as error.
func checkPath(path string, home func() (string, error)) (PathCheck, error) {
	p := strings.TrimSpace(path)
	if p == "" || strings.ContainsRune(p, 0) {
		return PathCheck{}, errors.New("path is required")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, err := home()
		if err != nil || h == "" {
			return PathCheck{}, errors.New("cannot resolve home directory")
		}
		p = h + p[1:]
	}
	if !filepath.IsAbs(p) {
		return PathCheck{}, errors.New("path must be absolute or start with ~/")
	}
	resolved := filepath.Clean(p)
	info, err := os.Stat(resolved)
	switch {
	case err == nil && info.IsDir():
		return PathCheck{Status: "dir", Resolved: resolved}, nil
	case err == nil:
		return PathCheck{Status: "not_dir", Resolved: resolved}, nil
	case errors.Is(err, fs.ErrNotExist):
		return PathCheck{Status: "missing", Resolved: resolved}, nil
	default:
		return PathCheck{Status: "error", Resolved: resolved, Reason: fmt.Sprint(err)}, nil
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/hostconfig/ -run TestCheckPath -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/hostconfig/checkpath.go internal/module/hostconfig/checkpath_test.go
git commit -m "feat(daemon): hostconfig path check"
```

---

### Task 4: Module, handlers, registration

**Files:**
- Create: `internal/module/hostconfig/module.go`, `internal/module/hostconfig/handler.go`
- Test: `internal/module/hostconfig/module_test.go`, `internal/module/hostconfig/handler_test.go`
- Modify: `cmd/pdx/main.go` (import block near line 23; `c.AddModule(devicestatemod.New())` near line 265)

**Interfaces:**
- Consumes: Task 1 `Store`, Task 2 `normalize*`, Task 3 `checkPath`
- Produces (HTTP contract used by B2):
  - `GET /api/hostconfig` → `{"projects":{"items":[...],"revision":N},"commands":{"items":[...],"revision":N},"resumeTemplates":{"items":{...},"revision":N}}` (empty collections serialise as `[]` / `{}`)
  - `PUT /api/hostconfig/{projects|commands|resume-templates}` body `{"items":..., "baseRevision":N}` → 200 `{"items":..., "revision":N}` | 400 | 409 `{"items":..., "revision":N}` | 413
  - `POST /api/hostconfig/check-path` body `{"path":"..."}` → 200 `PathCheck` | 400

- [ ] **Step 1: Write the failing tests**

`module_test.go`:

```go
package hostconfig

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestModule(t *testing.T) *Module {
	t.Helper()
	s, err := OpenStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return &Module{store: s, home: func() (string, error) { return t.TempDir(), nil }}
}

func serve(m *Module, method, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestModuleNameAndDependencies(t *testing.T) {
	m := New()
	require.Equal(t, "hostconfig", m.Name())
	require.Nil(t, m.Dependencies())
}
```

`handler_test.go`:

```go
package hostconfig

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlerGetEmpty(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{
		"projects":{"items":[],"revision":0},
		"commands":{"items":[],"revision":0},
		"resumeTemplates":{"items":{},"revision":0}
	}`, rr.Body.String())
}

func TestHandlerPutProjectsRoundTrip(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/projects",
		`{"items":[{"id":"p1","name":" Purdex ","slug":"purdex","path":"~/w"}],"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":[{"id":"p1","name":"Purdex","slug":"purdex","path":"~/w"}],"revision":1}`, rr.Body.String())

	rr = serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"slug":"purdex"`)
	assert.Contains(t, rr.Body.String(), `"revision":1`)
}

func TestHandlerPutConflict(t *testing.T) {
	m := newTestModule(t)
	body := `{"items":[{"id":"c1","name":"x","command":"ls","icon":{"kind":"phosphor","value":"Terminal"}}],"baseRevision":0}`
	require.Equal(t, http.StatusOK, serve(m, http.MethodPut, "/api/hostconfig/commands", body).Code)

	rr := serve(m, http.MethodPut, "/api/hostconfig/commands", `{"items":[],"baseRevision":0}`)
	require.Equal(t, http.StatusConflict, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"items":[{"id":"c1","name":"x","command":"ls","icon":{"kind":"phosphor","value":"Terminal"}}],"revision":1}`, rr.Body.String())
}

func TestHandlerPutResumeTemplates(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/resume-templates",
		`{"items":{"cc":{"exact":"cld --resume {id}","fallback":"cld -c"}},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"cc":{"exact":"cld --resume {id}","fallback":"cld -c"}},"revision":1}`, rr.Body.String())
}

func TestHandlerPutRejects(t *testing.T) {
	m := newTestModule(t)
	cases := map[string]string{
		"invalid json":       `{`,
		"missing items":      `{"baseRevision":0}`,
		"negative revision":  `{"items":[],"baseRevision":-1}`,
		"validation failure": `{"items":[{"id":"p1","name":"n","slug":"BAD","path":"/"}],"baseRevision":0}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rr := serve(m, http.MethodPut, "/api/hostconfig/projects", body)
			assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
		})
	}
}

func TestHandlerPutBodyTooLarge(t *testing.T) {
	m := newTestModule(t)
	body := `{"items":[],"baseRevision":0,"pad":"` + strings.Repeat("x", bodyCap) + `"}`
	rr := serve(m, http.MethodPut, "/api/hostconfig/projects", body)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
}

func TestHandlerCheckPath(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPost, "/api/hostconfig/check-path", `{"path":"~"}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), `"status":"dir"`)

	rr = serve(m, http.MethodPost, "/api/hostconfig/check-path", `{"path":"relative"}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code)

	rr = serve(m, http.MethodPost, "/api/hostconfig/check-path", `nope`)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/module/hostconfig/ -v`
Expected: FAIL (`Module`, `New`, `bodyCap` undefined)

- [ ] **Step 3: Write implementation**

`module.go`:

```go
package hostconfig

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wake/purdex/internal/core"
)

// Module serves per-host launcher config over /api/hostconfig*.
type Module struct {
	core  *core.Core
	store *Store
	home  func() (string, error) // daemon user's home; injectable for tests
}

// New returns a new Module ready for registration.
func New() *Module { return &Module{home: os.UserHomeDir} }

func (m *Module) Name() string           { return "hostconfig" }
func (m *Module) Dependencies() []string { return nil }

// Init opens (or creates) the host config SQLite database inside DataDir.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	var err error
	m.store, err = OpenStore(filepath.Join(c.Cfg.DataDir, "host_config.db"))
	return err
}

// RegisterRoutes wires up all /api/hostconfig endpoints.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/hostconfig", m.handleGet)
	mux.HandleFunc("PUT /api/hostconfig/projects", m.putHandler(KeyProjects, func(raw []byte) (any, error) { return normalizeProjects(raw) }))
	mux.HandleFunc("PUT /api/hostconfig/commands", m.putHandler(KeyCommands, func(raw []byte) (any, error) { return normalizeCommands(raw) }))
	mux.HandleFunc("PUT /api/hostconfig/resume-templates", m.putHandler(KeyResumeTemplates, func(raw []byte) (any, error) { return normalizeResumeTemplates(raw) }))
	mux.HandleFunc("POST /api/hostconfig/check-path", m.handleCheckPath)
}

// Start logs a banner; no background work required.
func (m *Module) Start(_ context.Context) error {
	log.Println("[hostconfig] endpoints enabled")
	return nil
}

// Stop closes the underlying SQLite database.
func (m *Module) Stop(_ context.Context) error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}
```

`handler.go`:

```go
package hostconfig

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
)

// bodyCap bounds request bodies. Reads cap+1 so an over-cap body is 413.
const bodyCap = 1 << 20

type collection struct {
	Items    json.RawMessage `json:"items"`
	Revision int64           `json:"revision"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[hostconfig] encode response: %v", err)
	}
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, bodyCap+1))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return nil, false
	}
	if len(body) > bodyCap {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	return body, true
}

// entryItems returns the stored JSON, or the empty value for a never-written key.
func entryItems(e Entry, empty string) json.RawMessage {
	if e.Value == nil {
		return json.RawMessage(empty)
	}
	return e.Value
}

func emptyFor(key string) string {
	if key == KeyResumeTemplates {
		return `{}`
	}
	return `[]`
}

// handleGet returns all collections: GET /api/hostconfig.
func (m *Module) handleGet(w http.ResponseWriter, _ *http.Request) {
	out := map[string]collection{}
	for field, key := range map[string]string{"projects": KeyProjects, "commands": KeyCommands, "resumeTemplates": KeyResumeTemplates} {
		e, err := m.store.Get(key)
		if err != nil {
			log.Printf("[hostconfig] get %s: %v", key, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out[field] = collection{Items: entryItems(e, emptyFor(key)), Revision: e.Revision}
	}
	writeJSON(w, http.StatusOK, out)
}

// putHandler replaces one collection guarded by baseRevision.
func (m *Module) putHandler(key string, normalize func([]byte) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := readBody(w, r)
		if !ok {
			return
		}
		var req struct {
			Items        json.RawMessage `json:"items"`
			BaseRevision *int64          `json:"baseRevision"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if req.Items == nil || req.BaseRevision == nil || *req.BaseRevision < 0 {
			http.Error(w, "items and baseRevision (>= 0) are required", http.StatusBadRequest)
			return
		}
		normalized, err := normalize(req.Items)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		value, err := json.Marshal(normalized)
		if err != nil {
			log.Printf("[hostconfig] marshal %s: %v", key, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		entry, stored, err := m.store.Put(key, value, *req.BaseRevision)
		if err != nil {
			log.Printf("[hostconfig] put %s: %v", key, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		status := http.StatusOK
		if !stored {
			status = http.StatusConflict
		}
		writeJSON(w, status, collection{Items: entryItems(entry, emptyFor(key)), Revision: entry.Revision})
	}
}

// handleCheckPath classifies a project path: POST /api/hostconfig/check-path.
func (m *Module) handleCheckPath(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	res, err := checkPath(req.Path, m.home)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
```

Note: `normalizeProjects` returns `[]Project` which marshals to `[]` when empty (non-nil), and `normalizeResumeTemplates` returns a non-nil map → `{}`. `json.RawMessage` in `collection.Items` is emitted verbatim, so the stored normalised JSON round-trips.

- [ ] **Step 4: Register the module in `cmd/pdx/main.go`**

Add to the import block (alphabetical neighbour of `devicestatemod`):

```go
	hostconfigmod "github.com/wake/purdex/internal/module/hostconfig"
```

After `c.AddModule(devicestatemod.New())`:

```go
	c.AddModule(hostconfigmod.New())
```

- [ ] **Step 5: Run tests, vet and build**

Run: `go test ./internal/module/hostconfig/ -v && go vet ./internal/module/hostconfig/ ./cmd/pdx/ && go build ./...`
Expected: all PASS, build succeeds.

Then the full daemon suite: `go test ./...` — Expected: PASS (report any pre-existing flaky failure verbatim; do not "fix" unrelated tests).

- [ ] **Step 6: Commit**

```bash
git add internal/module/hostconfig/module.go internal/module/hostconfig/handler.go internal/module/hostconfig/module_test.go internal/module/hostconfig/handler_test.go cmd/pdx/main.go
git commit -m "feat(daemon): hostconfig module and HTTP API"
```

---

### Task 5: Live smoke check against a temp daemon (no deploy)

**Files:** none (verification only)

- [ ] **Step 1:** Build: `go build -o /tmp/pdx-hostconfig ./cmd/pdx` (use scratch path under the worktree `bin/` if /tmp is disallowed).
- [ ] **Step 2:** Do NOT start it on the live port 7860. Report to the orchestrator that manual smoke will be done at deploy time (daemon binds the tailscale IP and a second instance would conflict). This task only confirms the binary builds.

Expected: binary builds; report path.
