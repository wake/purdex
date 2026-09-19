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
func (s *Store) DeleteAttachment(profileID, clientID string) (bool, error) // both must match
func (s *Store) ListAttachments(profileID string) ([]Attachment, error)
```

- `DeleteProfile` returns `ErrProfileAttached` when any `profile_attachments` row points at it
  (spec decision 16). Deleting a profile deletes its sections in the same call.
- **Profile existence is part of every write statement, not a separate check** (plan review #3 — a
  check-then-write pair lets `PutAttachment` land between the attachment check and the delete,
  leaving an attachment that points at nothing and defeating the 409):
  - `DeleteProfile`: `DELETE FROM profiles WHERE id = ? AND NOT EXISTS (SELECT 1 FROM
    profile_attachments WHERE profile_id = ?)`. `RowsAffected() == 0` → re-read to classify as
    `ErrProfileAttached` vs not-found. **The profile row goes first, the sections after** — once the
    row is gone no new section or attachment can be inserted (next bullet), so nothing is orphaned.
  - `PutAttachment` and the section insert (Task 2) are `INSERT … SELECT … WHERE EXISTS (SELECT 1
    FROM profiles WHERE id = ?) ON CONFLICT … DO UPDATE/NOTHING`; zero rows affected with no
    existing row → `ErrProfileNotFound`.
- `DeleteAttachment` matches on **both** `client_id` and `profile_id`, so a detach aimed at the wrong
  profile is a no-op (`false`), never somebody else's detach (plan review #4).
- `PutAttachment` moves a client from one profile to another (a client has at most one master):
  `client_id` is the primary key, so the upsert replaces `profile_id`.
- The store owns the id generation; tests inject a deterministic id source the same way they inject
  the clock.

Test list: create→get→list round trip; rename unknown id → `false`; delete with an attachment →
`ErrProfileAttached`; delete after detach → sections gone too; `PutAttachment` twice with different
profiles leaves one row; `ListAttachments` never nil; `PutAttachment` to an unknown profile →
`ErrProfileNotFound` and no row; `DeleteAttachment` with the right client but the wrong profile →
`false`, row intact; **concurrent** (file-backed DB, see Task 2) `DeleteProfile` ∥ `PutAttachment`
repeated N times — every run ends in exactly one of {profile gone, no attachment} or {profile kept,
attachment present}, never an attachment without a profile.

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

1. Read the current row. If **absent or a tombstone** (`deleted = 1`, see below): accept only
   `baseRev == 0` → `PutApplied`, at `rev = 1` for a never-seen section and at
   `rev = tombstone.rev + 1` over a tombstone. A non-zero `baseRev` is `PutConflict` with `Rev: 0`
   and no `Current` (the section was deleted under the client; §4.6.3). Fingerprint/ordinal of a
   tombstone are ignored — there is no stored shape left to protect.
2. If present and `row.Fingerprint != in.Fingerprint`:
   - `in.Ordinal > row.Ordinal` → the writer is newer, continue to step 3 (its write replaces the
     stored shape);
   - otherwise → `PutSchema` (covers both "writer is older" and "equal ordinals", the developer
     error the spec fails closed on).
3. `row.Rev == baseRev` → update, `rev = rev + 1`, `PutApplied`.
4. `row.Hash == in.Hash` → `PutConverged`, **no write**, return `row.Rev` (spec §4.6 middle branch:
   "驗算，沒改變就不寫回").
5. otherwise → `PutConflict` with the current row attached.

**The stored ordinal never goes down** (plan review #2). With equal fingerprints an older client is
allowed to write (spec §4.5 row 1), but its lower ordinal must not replace the stored one, or the
next value-domain bump loses its direction signal. The UPDATE writes `ordinal = MAX(ordinal, ?)`.
(When the fingerprints differ, step 2 already guarantees `in.Ordinal > row.Ordinal`, so `MAX` is the
incoming value.)

Atomicity: steps 3 and 1 are each expressed as **one statement**, following
`devicestate/store.go:76`:

```sql
-- step 3
UPDATE profile_sections SET rev = rev + 1, hash = ?, fingerprint = ?, ordinal = MAX(ordinal, ?),
       payload = ?, writer = ?, updated_at = ?
 WHERE profile_id = ? AND section = ? AND rev = ? AND deleted = 0;  -- RowsAffected()==1 ⇒ applied

-- step 1, never-seen section (profile existence is in the statement, Task 1)
INSERT INTO profile_sections (…) SELECT … WHERE EXISTS (SELECT 1 FROM profiles WHERE id = ?)
  ON CONFLICT(profile_id, section) DO NOTHING;          -- RowsAffected() == 1 ⇒ inserted

-- step 1, over a tombstone: CAS on the tombstone's own rev
UPDATE profile_sections SET deleted = 0, rev = rev + 1, hash = ?, … 
 WHERE profile_id = ? AND section = ? AND rev = ? AND deleted = 1;
```

The re-read in steps 2/4/5 is only there to *classify* an outcome that the conditional statement
already decided against; a racing writer can make the reported `Current` one revision stale, which
is harmless because the client re-runs §4.6.1 on the next event anyway. **Write that reasoning as a
comment** — it is the first thing a reviewer will challenge.

`DeleteSection` **writes a tombstone, it does not remove the row** (plan review #1, critical). A
real `DELETE` lets the revision counter restart: A deletes at rev 1, B recreates the section and
gets rev 1 again, A's retried `DELETE baseRev=1` (lost response) then destroys B's new content — an
ABA hole straight through the CAS. So:

```sql
UPDATE profile_sections SET deleted = 1, rev = rev + 1, payload = '{}', hash = '', writer = ?,
       updated_at = ?
 WHERE profile_id = ? AND section = ? AND rev = ? AND deleted = 0;
```

- hit → `PutApplied` with the tombstone's rev;
- miss, row live → `PutConflict` with the current row;
- miss, row already a tombstone or never existed → `PutApplied` (idempotent — two clients removing
  the same workspace must not deadlock each other). This is now safe: in the ABA interleaving the
  recreated row is live at `tombstone.rev + 1`, which can never equal the retrier's old `baseRev`.
- **Revisions of a `(profile, section)` pair are therefore strictly increasing for the life of the
  profile**, which is also what spec §4.6.1's "`rev < baseRev` ⇒ the profile was recreated" relies on.
- Tombstones are invisible to `GetSection` / `ListSections` / the profile GET (the section reads as
  absent, so spec §4.6.3's client logic is unchanged) and are removed with their profile.
  Schema: one added column, `deleted INTEGER NOT NULL DEFAULT 0`.

Test list (one per row of the table, plus): absent + baseRev 0 → applied at rev 1; absent +
baseRev 3 → conflict rev 0; stale baseRev + different hash → conflict carrying the current payload;
stale baseRev + identical hash → converged, `rev` unchanged, `updated_at` unchanged; newer ordinal
with a different fingerprint → applied and the stored fingerprint changes; older ordinal → schema;
equal ordinals + different fingerprints → schema; delete with matching rev → gone; delete twice →
applied both times; delete with stale rev → conflict; `ListSections` never nil and omits payloads;
sections of a deleted profile are gone (tombstones included).

Added by the plan review:
- **ABA**: put (rev 1) → delete baseRev 1 → put baseRev 0 (**rev 3, not 1**) → delete baseRev 1
  again → `PutConflict`, content intact.
- a tombstone is absent from `GetSection` and `ListSections`; put with non-zero baseRev onto a
  tombstone → conflict rev 0.
- same fingerprint + **lower** ordinal → applied, stored ordinal unchanged; same fingerprint +
  higher ordinal → applied, stored ordinal raised.
- `PutSection` into an unknown / just-deleted profile → `ErrProfileNotFound`, no row.
- **Concurrency, on a file-backed WAL database** (`t.TempDir()`, not `":memory:"` — that mode is
  pinned to one connection and cannot race): N goroutines `PutSection` with the same `baseRev` and
  distinct hashes → exactly one `PutApplied`, the rest `PutConflict`, final `rev == base + 1`. Same
  shape for N concurrent first-inserts (`baseRev 0`). The file DSN needs
  `_pragma=busy_timeout(5000)` alongside WAL so a losing writer waits instead of failing with
  `SQLITE_BUSY` — add it to `OpenStore`.

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
| `payload` | parses, is a JSON **object** (not an array or scalar), and is ≤ 5 MiB **on its own** |

`tabs.<id>` is validated structurally only — the daemon never learns what a workspace is (spec
§4.6: "the daemon is deliberately dumb").

## Task 4 — `handler.go`: the ten routes

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
| `PUT /api/profiles/{id}/attachment` — body `{clientId, deviceName}` | 200 | 400, 404 |
| `DELETE /api/profiles/{id}/attachment?clientId=c_…` | 200 `{detached: bool}` | 400, 404 |

That is **ten** routes (the spec's table has ten; an earlier draft of this plan said nine). The
route-registration test enumerates all ten. `DELETE …/attachment` takes `clientId` from the **query
string** — DELETE bodies are unreliable through proxies — and only detaches when the attachment
belongs to the `{id}` in the path (`detached:false` otherwise).

The section `PUT` body is `{clientId, baseRev, hash, fingerprint, ordinal, payload}`; the section
`DELETE` takes `?baseRev=N&clientId=c_…`. `clientId` is where `writer` comes from — the spec's §4.6
sketch omitted it (fixed there in the same commit as this plan revision).

409 bodies carry `reason` so the client can branch without guessing:
`{"reason":"conflict","rev":N,"hash":"…","payload":{…}}` and
`{"reason":"schema","fingerprint":"…","ordinal":N}`.

Size: the spec caps the **payload** at 5 MiB, not the body (plan review #7) — a legal 5 MiB payload
plus its envelope must not be refused. `payloadCap = 5 << 20`; `putBodyCap = payloadCap + 64<<10`,
read with the devicestate read-cap+1 idiom; then `len(payload) > payloadCap` → 413. Tests: payload of
exactly 5 MiB → 200; payload of 5 MiB + 1 → 413; body over `putBodyCap` → 413.

**Broadcast is injected, so it is testable here** (plan review #5): the handler holds
`broadcast func(eventType, value string)`; Task 5 wires it to `core.Events`, tests pass a recorder.
Wire shape, with explicit tags because the key names are a contract with P2b:

```go
type profileEvent struct {
    ProfileID      string `json:"profileId"`
    Section        string `json:"section"`
    Rev            int64  `json:"rev"`
    Hash           string `json:"hash"`
    WriterClientID string `json:"writerClientId"`
    Deleted        bool   `json:"deleted,omitempty"`
}
```

Broadcast tests: applied PUT → exactly one event with the keys above; applied DELETE → one event
with `deleted:true`; idempotent DELETE of an absent section, `PutConverged`, conflict, schema, 400,
404, 413 → **zero** events.

## Task 5 — `module.go`, registration, broadcast

Tests first (`module_test.go`: routes registered, `Stop` closes, DB file created 0600).

- `Init`: `OpenStore(filepath.Join(c.Cfg.DataDir, "profiles.db"))`, then **`os.Chmod(path, 0600)`**
  — the file now holds host tokens (spec §7); its siblings are created 0644 by sqlite and this is
  the one place that matters.
- Wire the handler's injected `broadcast` (Task 4) to the core, nil-guarded:

```go
broadcast := func(eventType, value string) {
    if m.core != nil && m.core.Events != nil {
        m.core.Events.Broadcast("", eventType, value)
    }
}
```

  `session` is `""` (this is not a session event), matching `backup:done`. The event type is
  `"profile"`.
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
