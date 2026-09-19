# Plan — Profile Sync P1: the daemon `profiles` module

- Spec: `2026-09-20-profile-sync-spec.md`. Section numbers below refer to it; §4.6 is the
  protocol, §4.6.1 the base/fast-forward table, §4.6.3 section lifecycle, §4.8 the schema and
  routes.
- Worktree: `.claude/worktrees/profile-sync`, branch `worktree-profile-sync`, based on
  `origin/main` alpha.410 (`a4f3e957`). This plan covers **P1 only** — one PR, daemon-only.
  P2a/P2b/P3/P4a/P4b each get their own plan when their turn comes (§5 of the spec).
- Every task: subagent, **TDD — the failing test first, then the code**, one commit per task with
  `git commit --only <files>` (new files `git add` first; parallel subagents in one worktree share
  an index, so a bare `git commit -a` would sweep in another task's files).
- Every Bash call in a subagent prefixed with
  `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/profile-sync &&`.
- Verify per task: `go test ./internal/module/profiles/...`.
  Before the PR: `go build ./... && go vet ./... && go test ./...`.
- **PR size**: this lands ~10 files. The project rule is **≤ 800 changed lines *or* ≤ 20 files**;
  P1 passes on the file count (the Go store + handler + validate + their tests exceed 800 lines).
  State that in the PR body so review does not re-litigate it.
- No SPA change in this PR. Nothing is wired to a UI; P1 is reachable only by `curl`.

## Measured baseline (2026-09-20, worktree at alpha.410)

- **Module template** — `internal/module/devicestate/module.go` (52 lines) is a complete example:
  `New()`, `Name()`, `Dependencies() nil`, `Init(c *core.Core)` opening
  `filepath.Join(c.Cfg.DataDir, "device_state.db")`, `RegisterRoutes(mux)` with Go 1.22
  `"METHOD /path/{param}"` patterns, `Start` logging a banner, `Stop` closing the store.
- **Store template** — `internal/module/devicestate/store.go:37 OpenStore(path)`: DSN
  `path + "?_pragma=journal_mode(wal)"`, `":memory:"` special-cased with `SetMaxOpenConns(1)`,
  injectable `now func() int64` (ms), `migrate()` = one `CREATE TABLE IF NOT EXISTS`.
  **There is no schema-version table anywhere in the daemon** — do not invent one.
- **Conditional write precedent** — `devicestate/store.go:76 Upsert` does its comparison inside a
  single `INSERT … ON CONFLICT … WHERE` and reads `RowsAffected()`. P1 uses the same technique for
  compare-and-set (no explicit transaction needed; see Task 2).
- **Handler template** — `devicestate/handler.go`: `putBodyCap = 5 << 20`, reads `cap+1` through
  `io.LimitReader` and returns 413 when over, `writeJSON` helper, `r.PathValue("…")`.
- **Broadcast** — `internal/core/events.go:112 Broadcast(session, eventType, value string)`;
  `HostEvent` is three strings. The rich-payload precedent is
  `internal/module/backup/handler.go:129`
  `m.core.Events.Broadcast("", "backup:done", string(payload))`, guarded by
  `m.core != nil && m.core.Events != nil`. A module keeps `core` from `Init` (`backup/module.go:27`).
- **Registration** — `cmd/pdx/main.go:283` `c.AddModule(devicestatemod.New())`, in a run of
  `AddModule` calls; `profilesmod.New()` goes in the same block.
- **Test layout** — one `_test.go` per source file (`devicestate/`, `hostconfig/` both do this).

## Task 1 — `store.go`: schema, profiles, attachments

Tests first (`store_test.go`), all against `OpenStore(":memory:")` with an injected clock.

Schema exactly as spec §4.8 (three tables, `CREATE TABLE IF NOT EXISTS`, no version table).

**No foreign keys.** `DeleteProfile` removes that profile's sections and attachments explicitly in
the same call, so cascade buys nothing here. If a later phase wants them, note the trap already
documented at `internal/store/agent_event.go:35-52`: `PRAGMA foreign_keys` is **per connection**, so
it has to be in the DSN (`?_pragma=foreign_keys(1)`) — a post-`Open` `db.Exec("PRAGMA …")` only
affects whichever pooled connection served it and silently does nothing for the rest.

API:

```go
type Profile struct { ID, Name string; CreatedAt, UpdatedAt int64 }
type Attachment struct { ClientID, ProfileID, DeviceName string; AttachedAt, LastSeen int64 }

func (s *Store) CreateProfile(name string) (Profile, error)   // id = "p_" + 12 hex, crypto/rand
func (s *Store) ListProfiles() ([]Profile, error)             // never nil
func (s *Store) GetProfile(id string) (Profile, bool, error)
func (s *Store) RenameProfile(id, name string) (bool, error)
func (s *Store) DeleteProfile(id string) error                // see ErrProfileAttached
func (s *Store) PutAttachment(a Attachment) error             // upsert; attached_at only on insert
func (s *Store) DeleteAttachment(clientID string) (bool, error)
func (s *Store) ListAttachments(profileID string) ([]Attachment, error)
```

- `DeleteProfile` returns `ErrProfileAttached` when any `profile_attachments` row points at it
  (spec decision 16). Deleting a profile deletes its sections in the same call.
- `PutAttachment` moves a client from one profile to another (a client has at most one master):
  `client_id` is the primary key, so the upsert replaces `profile_id`.
- The store owns the id generation; tests inject a deterministic id source the same way they inject
  the clock.

Test list: create→get→list round trip; rename unknown id → `false`; delete with an attachment →
`ErrProfileAttached`; delete after detach → sections gone too; `PutAttachment` twice with different
profiles leaves one row; `ListAttachments` never nil.

## Task 2 — `store.go`: section compare-and-set

Tests first. This is the heart of the PR — the table in spec §4.6 and the schema rule in §4.5.

```go
type Section struct {
    Section     string
    Rev         int64
    Hash        string
    Fingerprint string
    Ordinal     int
    Payload     json.RawMessage
    Writer      string
    UpdatedAt   int64
}
type SectionMeta struct { /* Section minus Payload */ }

type PutOutcome int
const ( PutApplied PutOutcome = iota; PutConverged; PutConflict; PutSchema )

type PutResult struct {
    Outcome PutOutcome
    Rev     int64
    Current *Section   // set for PutConflict (the SOT side the user will choose against)
    CurrentFingerprint string; CurrentOrdinal int // set for PutSchema
}

func (s *Store) PutSection(profileID string, in Section, baseRev int64) (PutResult, error)
func (s *Store) DeleteSection(profileID, section string, baseRev int64) (PutResult, error)
func (s *Store) GetSection(profileID, section string) (Section, bool, error)
func (s *Store) ListSections(profileID string) ([]SectionMeta, error)
```

Decision order inside `PutSection` — **schema is checked before revision, and fails closed**:

1. Read the current row. If **absent**: accept only `baseRev == 0` → insert at `rev = 1`,
   `PutApplied`. A non-zero `baseRev` against an absent section is `PutConflict` with `Rev: 0`
   (the section was deleted under the client; §4.6.3).
2. If present and `row.Fingerprint != in.Fingerprint`:
   - `in.Ordinal > row.Ordinal` → the writer is newer, continue to step 3 (its write replaces the
     stored shape);
   - otherwise → `PutSchema` (covers both "writer is older" and "equal ordinals", the developer
     error the spec fails closed on).
3. `row.Rev == baseRev` → update, `rev = rev + 1`, `PutApplied`.
4. `row.Hash == in.Hash` → `PutConverged`, **no write**, return `row.Rev` (spec §4.6 middle branch:
   "驗算，沒改變就不寫回").
5. otherwise → `PutConflict` with the current row attached.

Atomicity: steps 3 and 1 are each expressed as **one statement**, following
`devicestate/store.go:76`:

```sql
-- step 3
UPDATE profile_sections SET rev = rev + 1, hash = ?, fingerprint = ?, ordinal = ?,
       payload = ?, writer = ?, updated_at = ?
 WHERE profile_id = ? AND section = ? AND rev = ?;      -- RowsAffected() == 1 ⇒ applied

-- step 1
INSERT INTO profile_sections (…) VALUES (…)
  ON CONFLICT(profile_id, section) DO NOTHING;          -- RowsAffected() == 1 ⇒ inserted
```

The re-read in steps 2/4/5 is only there to *classify* an outcome that the conditional statement
already decided against; a racing writer can make the reported `Current` one revision stale, which
is harmless because the client re-runs §4.6.1 on the next event anyway. **Write that reasoning as a
comment** — it is the first thing a reviewer will challenge.

`DeleteSection` is the same CAS on `rev` (`DELETE … WHERE rev = ?`), returning `PutApplied` on a hit,
`PutConflict` with the current row on a miss, and `PutApplied` when the section is already absent
(idempotent delete — two clients removing the same workspace must not deadlock each other).

Test list (one per row of the table, plus): absent + baseRev 0 → applied at rev 1; absent +
baseRev 3 → conflict rev 0; stale baseRev + different hash → conflict carrying the current payload;
stale baseRev + identical hash → converged, `rev` unchanged, `updated_at` unchanged; newer ordinal
with a different fingerprint → applied and the stored fingerprint changes; older ordinal → schema;
equal ordinals + different fingerprints → schema; delete with matching rev → gone; delete twice →
applied both times; delete with stale rev → conflict; `ListSections` never nil and omits payloads;
sections of a deleted profile are gone.

## Task 3 — `validate.go`

Tests first. Mirrors `devicestate/validate.go`'s shape (plain functions returning `error`).

| Input | Rule |
|---|---|
| `profileId` | `^p_[0-9a-f]{12}$` |
| `clientId` | `^c_[0-9a-f]{12}$` (same alphabet as devicestate, so a client keeps one identity) |
| `name`, `deviceName` | trimmed, 1–64 runes, printable, no control characters |
| `section` | `^(hosts\|settings\|workspaces\|tabs\.[A-Za-z0-9_-]{1,64})$` |
| `hash`, `fingerprint` | `^[0-9a-f]{64}$` |
| `ordinal` | `>= 1` |
| `baseRev` | `>= 0` |
| `payload` | parses, and is a JSON **object** (not an array or scalar) |

`tabs.<id>` is validated structurally only — the daemon never learns what a workspace is (spec
§4.6: "the daemon is deliberately dumb").

## Task 4 — `handler.go`: the nine routes

Tests first (`handler_test.go`), driving a real `*Store` on `":memory:"` through
`httptest.NewRecorder()`; no `core.Core` needed if the broadcast is nil-guarded (it is).

| Route | Success | Failure |
|---|---|---|
| `GET /api/profiles` | `{profiles:[{…, sections:[SectionMeta], attachments:[…]}]}` | — |
| `POST /api/profiles` | 200 `{id}` | 400 bad name |
| `PATCH /api/profiles/{id}` | 200 | 400, 404 |
| `DELETE /api/profiles/{id}` | 200 | **409 `{reason:"attached", attachments:[…]}`**, 404 |
| `GET /api/profiles/{id}` | `{sections:{<key>:Section}}` | 404 |
| `GET /api/profiles/{id}/sections/{section}` | `Section` | 400, 404 |
| `PUT /api/profiles/{id}/sections/{section}` | 200 `{rev, applied}` | 409 conflict / 409 schema / 400 / 404 / **413 over 5 MB** |
| `DELETE /api/profiles/{id}/sections/{section}` | 200 `{rev}` | 409, 400, 404 |
| `PUT` / `DELETE /api/profiles/{id}/attachment` | 200 | 400, 404 |

409 bodies carry `reason` so the client can branch without guessing:
`{"reason":"conflict","rev":N,"hash":"…","payload":{…}}` and
`{"reason":"schema","fingerprint":"…","ordinal":N}`.

Body cap: copy `putBodyCap = 5 << 20` and the read-cap+1 idiom verbatim; a 5 MB+1 body is a test.

## Task 5 — `module.go`, registration, broadcast

Tests first (`module_test.go`: routes registered, `Stop` closes, DB file created 0600).

- `Init`: `OpenStore(filepath.Join(c.Cfg.DataDir, "profiles.db"))`, then **`os.Chmod(path, 0600)`**
  — the file now holds host tokens (spec §7); its siblings are created 0644 by sqlite and this is
  the one place that matters.
- Broadcast on every applied `PUT` and `DELETE` of a section, after the store call succeeds:

```go
if m.core != nil && m.core.Events != nil {
    payload, _ := json.Marshal(profileEvent{
        ProfileID: id, Section: sec, Rev: res.Rev,
        Hash: hash, Writer: writer, Deleted: deleted,
    })
    m.core.Events.Broadcast("", "profile", string(payload))
}
```

  `session` is `""` (this is not a session event), matching `backup:done`.
  **No broadcast for `PutConverged`** — nothing changed, and a spurious event would make every
  client re-fetch a section it already has.
- Register in `cmd/pdx/main.go` next to `devicestatemod`.

## PR

Title: `feat(daemon): profiles module — per-section compare-and-set SOT for Profile Sync (P1)`

Body must state: the spec path; that this is daemon-only and unreachable from the UI; the file
count vs the 800-line rule; and the three semantics a reviewer should check hardest — schema before
revision (§4.5), converged writes nothing (§4.6), delete-is-idempotent (§4.6.3).

Review: R1 `/codex:review --base <sha>` then R2 attacker → critic, `--model gpt-5.6-sol`, per
CLAUDE.md. Focus text for the attacker should name: the CAS statements' atomicity argument in
Task 2, the classify-after-the-fact re-read, 5 MB bodies, and whether any route lets a client
write a section into a profile it is not attached to (it does — by design, the wizard's push
happens before attachment; make sure the attacker is told so it is not reported as a hole).

## After P1

Next plan is P2a (`lib/profile/` pure core). Do not start it in this PR — spec §5 keeps the pure
core and the transport in separate PRs so the state machine can be tested without a daemon.
