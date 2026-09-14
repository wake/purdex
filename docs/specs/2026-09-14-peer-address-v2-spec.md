# Spec — Peer address v2: session labels ("Peer Address v2")

Status: draft v3.1 (plan review `task-mu1dulow-tnozhu` amendments: §3.1 suffix source, §3.3 alive-unknowns only, §3.4 label_rev, §3.5 unapplied revision, §3.6 version trailer; R1 `task-mu1ags4q-o5eeev`: 2 Blockers, 12 Majors, 3 Minors,
5 omissions; R2 `task-mu1ayutr-i477ud`: 1 Blocker, 8 Majors, 3 Minors — all
accepted; §8/§9 hold both disposition tables)
Date: 2026-09-14
Branch: `worktree-peer-address-v2`
Amends: `2026-09-13-peer-bridge-spec.md` v3.2 (§4.1, §4.2, §4.4, §4.5, §4.7, §4.8)

> This spec replaces the `<session>` half of the human address. Wire identity,
> authentication, delivery and audit are unchanged unless a section below
> says otherwise. Section numbers with a "PB" prefix refer to the Peer Bridge
> spec; "P3 Dn" to the P3 plan's decision table.
>
> **v2 (after R1):** occupancy on registry-entry liveness with an explicit
> "cannot tell"; default labels in a namespace users cannot claim; suffix
> display only; helper renames rewrite the registry file instead of
> respawning. **v3 (after R2):** one registry diagnosis feeds both
> inventory `partial` and claim `not_ready` (§3.3); every live entry has an
> inventory row, so label ambiguity is decided per conversation (§3.2,
> §3.4); a host-wide revision counter replaces timestamps for helper
> renames, and the rename is bound to a helper instance (§3.5); error
> bodies use the existing `error` key (§3.6); acceptance is split by phase
> and the take-over sequence is corrected (§5).

## 1. Problem

The Peer Bridge human address is `<host>/<session>` where `<session>` is
resolved against the tmux session name, then the tmux session code, then
`cc:<Claude Code registry name>` (PB §4.1). Every one of those is an identity
the user does not control at the moment they need it:

- **tmux session names** on this machine are `mt0`…`mt7`. `mini-lab/mt4`
  tells a remote agent nothing about which session it is talking to.
- **session codes** are a reversible encoding of the tmux `$N` id. After a
  tmux server restart `$0` mints the same code for whatever session is
  created first (`handler.go:306`), so a recorded code can silently point
  at a different session after a crash.
- **Claude Desktop sessions** — the sessions that will carry computer-use
  work — are outside tmux entirely. Their only address is
  `<host>/cc:<registry name>`, and the registry name defaults to
  `<cwd basename>-<2 hex>` (`purdex-3f`). Purdex has no lever on it: the
  registry file is owned and rewritten by Claude Code, and the only ways to
  set the name are `/rename` typed inside the session or `claude --name` at
  launch.

The scenario that fails today: a Desktop session on air-2026 is started as
"the tester"; a development session on mlab is told to first find its
tester. With three Desktop sessions in the same cwd the mlab session sees
`air/cc:purdex-3f`, `air/cc:purdex-1b`, `air/cc:purdex-6d` and cannot tell
them apart. Nothing in the address survives the session being restarted.

## 2. Goals / non-goals

### Goals

1. A session can name itself, from inside, with one command
   (`pdx msg name <label>`), and that name becomes the primary segment of
   its human address on every host.
2. The name is independent of tmux (works for Desktop sessions), survives
   `claude --resume` of the same conversation, and survives a daemon
   restart.
3. A name is held only while its holder is alive. When the holder is
   confirmed gone the name is free; nobody alive is ever displaced, and
   when the daemon cannot tell, it refuses rather than guesses — for
   claims **and** for resolution.
4. The address still carries enough machine-derived context to tell two
   sessions apart by eye, without that context being part of what the user
   has to type or something the daemon has to verify.
5. A session can ask what its own address is (`pdx msg whoami`).
6. tmux session names keep working as a fallback address for operators,
   with an explicit form that can never be shadowed by a label.

### Non-goals

- Renaming through the Purdex UI, or showing labels in the tab strip.
  Labels are an addressing concern; the UI keeps its own tab names.
- An MCP tool for renaming. The MCP control plane does not exist yet; the
  CLI is the only entry point in this spec.
- Changing Claude Code's own registry `name`. Purdex never rewrites a
  registry file it did not create (helper files are Purdex's own — §3.5).
- Label hand-over between sessions (an old session passing its name to a
  successor while both are alive). §7 records the hook for it.
- Proving that the process that ran `pdx msg name` is the session it names.
  The origin rule is PB §4.4's: endpoint attribution for a trusted local
  admin caller (§3.6).
- Picking one process of a conversation that has several live processes.
  A label names a conversation; `tmux:<name>` names a tmux session and
  keeps PB §4.2's pane tiebreak (§3.2).
- Making `pdx` reachable from a Desktop session on air-2026 (the binary is
  at `~/.config/pdx/bin/pdx`, not on `PATH`). That is a deployment
  prerequisite tracked with Local Daemon Install, not part of this spec.

## 3. Design

### 3.1 Address grammar

```
<host>/<label>[:<suffix>]
<host>/tmux:<tmux session name>
```

| Segment | Who sets it | Used for |
|---|---|---|
| `<host>` | unchanged (PB §4.1: local alias, configured alias, full `host_id`) | routing to a daemon |
| `<label>` | the session, via `pdx msg name`; default derived from the Claude Code `sessionId` | **the address** — the only part a caller has to type |
| `<suffix>` | the owning daemon, read-only | identification by eye; ignored on input |
| `tmux:<name>` | tmux | operator fallback that never goes through labels |

`:` separates label from suffix. `SplitAddress` splits on the first `/` as
today, so the `:` inside a full `host_id` on the left never collides with
the one on the right. Within `<session>`, the text before the first `:` is
the `head`; everything after it is the `rest`. For `head == "tmux"` the
`rest` is the tmux session name; for every other head the `rest` is a
suffix and is discarded by the resolver.

**User label rule.** `^[a-z0-9][a-z0-9-]{1,31}$` — lowercase ASCII letters,
digits and hyphen, 2–32 characters, must not start with a hyphen. Anything
else is refused at claim time with `label_invalid`. There is no
case-folding: a label that would need folding is simply invalid, so two
labels that look alike never differ only in case. **Reserved:** `cc` and
`tmux` are refused with `label_reserved`; the resolver also short-circuits
them (§3.2), so neither the old `cc:<name>` form nor the `tmux:<name>` form
can ever be read as a label plus suffix.

**Default label.** A session with no claimed label has the label
`"_" + enc(sessionId)`, where `enc` is: FNV-1a 64 over the UTF-8 bytes of
the `sessionId` string exactly as the registry stores it, reduced
`mod 36⁶`, rendered base36 with `0-9a-z`, left-padded with `0` to 6
characters. The `_` prefix is outside the user label charset, so a default
label can never be claimed, never collides with a user label, and is
recognisable at a glance as "unnamed". The derivation is deterministic:
the same conversation has the same default before and after a resume.
`enc` ships with golden vectors in its tests. Records carry `label_source:
"default" | "user"` so listings can show which sessions are still unnamed.

**Suffix.** Display only. The owning daemon computes it on every
inventory read and never stores it:

- the tmux session name is read from the entry's own registry `tmux` field
  (the text before its first `:`), for session rows and entry rows alike,
  so a record built from the entry alone (§3.6 self responses) renders the
  same address the listing shows
- inside tmux: `san(tmux session name) + "-" + san(registry name)`
- outside tmux: `san(registry name)`
- a session row whose agent is only the owner fallback (no entry):
  `san(tmux session name) + "-_"`
- `san` keeps `[A-Za-z0-9_.-]`, replaces every other byte with `_`, and
  truncates to 32 characters; an empty input becomes `_`.

The complete suffix therefore matches `^[A-Za-z0-9_.-]{1,65}$`; that is
the wire grammar the receiver checks (§3.5). The suffix is not a unique
encoding (both parts may contain `-`) and it is not identity: two sessions
can share a suffix, and one session's suffix changes when its tmux session
or registry name changes. Nothing in this spec depends on it beyond a
human reading it.

Examples:

```
air/purdex-tester:purdex-3f          Desktop session on air, named
air/_k3x9qz:purdex-1b                Desktop session on air, not yet named
mini-lab/purdex-dev:mt0-purdex-49    tmux session mt0 on mlab, named
mini-lab/tmux:mt4                    operator fallback, no label involved
```

### 3.2 Resolution (replaces PB §4.1 `<session>` tiers)

`peers.Resolve(records, session, partial)` keeps its shape (one host's
records, first tier with ≥1 match decides, several matches ⇒
`AmbiguousError`, none ⇒ `ErrNotFound`) with a new signature and tiers.
`session` is split at its first `:` into `head` and `rest`; `partial` is
the target envelope's `partial` flag (the sender already holds the whole
envelope — `send.go:254`).

| Case | Matches when | Notes |
|---|---|---|
| `head == "cc"` | — | `ErrNotFound` before any tier, regardless of `partial`; the message points to `pdx peers --all` |
| `head == "tmux"` | `rest` equals a record's `session_name` | explicit fallback; labels never considered; `rest` empty ⇒ `ErrNotFound`; PB §4.2's owner/pane tiebreak decides which process the session row carries |
| tier 1: label | `head` equals a record's `label` | over **all** rows — session rows and entry rows (§3.4); proxy rows and rows with `label == ""` excluded; `rest` ignored; more than one match ⇒ `AmbiguousError` with the candidates |
| tier 2: tmux name | `head` equals a record's `session_name` and `rest == ""` | reached only when tier 1 has no match **and** `partial == false` |
| tier 1 miss, `partial == true` | — | `ErrNotReady` — the snapshot may be missing a label (§3.3 evidence, PB §4.2 deadline, or a label-store read failure); the caller retries or uses `tmux:` |

`handleSend` maps `ErrNotReady` ⇒ `503 not_ready` (§3.6) next to its
existing `ambiguous` / `peer_not_found` cases.

**Shadowing.** A user label equal to another session's tmux name wins at
tier 1; that is what tier order means. Operators who need the tmux session
regardless of labels use `tmux:<name>`. The claim path does not refuse
such labels — tmux sessions appear and disappear independently of claims,
so a refusal there would be a false promise.

**One label per conversation, decided at tier 1.** A label (default or
user) belongs to a `sessionId`. Because every live non-proxy entry has
exactly one inventory row (§3.4), a conversation with two live processes
has two rows carrying the same label, and tier 1 reports `ambiguous` with
both. Naming does not and cannot pick a process; `tmux:<name>` can, through
PB §4.2's pane tiebreak, when the processes are in tmux. (Two *different*
conversations whose default labels collide — 36⁶ space — look the same at
tier 1 and are fixed by naming either one.)

**Snapshot semantics.** The sender resolves over a snapshot taken at send
time and only the resulting tuple travels (PB §4.4). A label released or
re-claimed between the snapshot and `/deliver` does not change where the
message goes: the receiver verifies the tuple, not the name. This is the
same window PB §4.4 already accepts for tmux renames, restated here so
that "the label moved" is never read as "the message should have moved".

### 3.3 Registry evidence, label store and occupancy

**Registry diagnosis.** `ReadRegistry` is extended to return, besides the
live entries, a classification of every file it considered:

| Class | Files | Effect |
|---|---|---|
| not a candidate | name does not match `<pid>.json`, `.key` files, temp files, directories | none |
| **confirmed dead** | inbox socket `ENOENT`; `kill(pid, 0)` ⇒ `ESRCH`; start time read and ≠ `procStart` | none — proven not to be a live session |
| **unknown** | file unreadable, JSON undecodable or fails schema, process cannot be classified (P3 D9: `Info` error or empty argv), start time unreadable, `kill(pid, 0)` ⇒ `EPERM`, pid outside the platform's valid range | **an entry whose `sessionId` the daemon cannot see** |
| live | passes PB §4.2 liveness | an `Entry` |

`EPERM` means the process exists; it is never "dead". The class is decided
per file in the order the checks already run (`registry.go:232`
pre-check, D9 classification, `isLive`), so the zero-fork skip of dead
entries is unchanged.

One diagnosis serves both consumers below. It is taken once per inventory
call and once per claim, never cached across calls.

**Inventory `partial`** (amends PB §4.2): `partial: true` when owner
resolution did not run for some session (today's rule) **or** the registry
diagnosis contains at least one **unknown** file **whose pid is alive**
(`kill(pid,0)` ⇒ success or `EPERM`). An unknown file for a dead pid cannot
hide a live session and is ignored here exactly as it is for claims. The envelope gains
`unknown_registry_files: []string` (empty when none) so an operator can see
why. §3.2 turns `partial` into `not_ready` for bare-name resolution; this
is what stops a bare `mt0` from falling to tier 2 while the Desktop session
that named itself `mt0` is temporarily unreadable (R2-1).

**Liveness of a conversation** is decided on the registry, at entry level:
`sessionId S` is *live* iff at least one live entry has that `sessionId`.
Owner resolution, tmux, row `reason`s and `partial` play no part.

**Label store.** Table `peer_labels` in `meta.db`, host-local, never
replicated:

```sql
CREATE TABLE IF NOT EXISTS peer_labels (
    session_id TEXT PRIMARY KEY,    -- Claude Code sessionId
    label      TEXT UNIQUE,         -- NULL after a release (row kept for rev)
    rev        INTEGER NOT NULL,    -- host-wide, strictly increasing (§3.5)
    set_at     INTEGER NOT NULL     -- unix ms, informational
);
CREATE TABLE IF NOT EXISTS peer_label_seq (
    id  INTEGER PRIMARY KEY CHECK (id = 1),
    rev INTEGER NOT NULL
);
```

`rev` is drawn from `peer_label_seq` inside the same transaction as the
row change (`UPDATE … SET rev = rev + 1 RETURNING rev`), so it is strictly
increasing across claims and releases on this host, survives daemon
restarts, and never involves a clock. One user label per conversation, one
conversation per user label, by `UNIQUE(label)` (NULLs do not collide).
Default labels are never stored.

A label `L` is *held* iff a row with `label = L` exists **and** its
`session_id` is live. A row whose session is not live is inert: it
neither resolves nor blocks.

**Claim** `(S', L)` — the peers module's dedicated `labelMu` (never
`Core.CfgMu`) is held from the registry diagnosis through the transaction
commit; registry and process I/O happen before the transaction is opened:

| Situation | Result |
|---|---|
| `L` fails the user label rule | `400 label_invalid` |
| `L` is `cc` or `tmux` | `400 label_reserved` |
| `S'` is not the `sessionId` of a live, non-proxy entry | `400 origin_unknown` (§3.6 Origin) |
| registry read failed, or the diagnosis has an **unknown** file whose pid is alive (`kill(pid,0)` ⇒ success or `EPERM`) | `503 not_ready`, `skipped` lists those files. An unknown file for a dead pid cannot be a live holder and does not block |
| label store read or write failed | `503 store_unavailable` |
| `L` held by live `S ≠ S'` | `409 label_taken` — body carries `holder` (that session's row, §3.4) and `live_labels` (every held label on this host) so the caller can pick a free one without a second round-trip |
| `(S', L)` already the row | `200`, no write, `rev` unchanged |
| otherwise | one transaction: delete any row with `label = L` (a holder that is not live), upsert `(S', L, next rev, now)`. `200` with the updated record |

The response is sent after the transaction commits; a commit failure is
`store_unavailable` and nothing changed.

**What the lock guarantees.** `labelMu` serialises claims and releases on
this daemon, so two concurrent claims of `L` cannot both pass the "held?"
test. It does not freeze the world: a holder `S` that was not live at the
registry read can be resumed a millisecond later and finds its row gone.
That outcome is the same as being resumed after the claim (next paragraph)
and is accepted; "nobody alive is displaced" is a statement about what the
daemon observed at the claim, which is the strongest statement a
single-host lock can make.

**Consequence for resume:** session `S` held `purdex-tester`, crashed; `S'`
claimed `purdex-tester`; `S` is resumed. `S`'s row is gone, so `S` comes
back as `_…` — unnamed — and `S'` keeps the name. `pdx msg whoami` tells
`S` its address; the operator's bootstrap sentence (§3.7) names it again.

**Release** `S'`: if a row for `S'` exists, set `label = NULL` and assign
the next `rev` (the row stays so the revision keeps climbing, §3.5);
otherwise no-op. `200` with the updated record. `origin_unknown` and
`store_unavailable` as for claim; no registry completeness requirement.

**Inventory read of labels.** `GET /api/peers` reads the whole table once
per call. If that read fails, every row is reported with its default label
and `label_source: "default"`, the envelope is `partial: true`, and the
failure is logged once per call.

**Garbage.** Released rows and rows for sessions that never come back
accumulate at the rate sessions are named, which is small; rows holding a
label are removed lazily by the next claim of that label. No sweeper in
this spec.

### 3.4 Peer record (amends PB §4.2)

**Every live non-proxy Claude Code entry has exactly one row.** PB §4.2's
rules 4/5 produce a session row per tmux session (carrying the owner entry
chosen by owner resolution and the pane tiebreak) and an outside row per
entry not in any listed tmux session. v2 adds **entry rows**: a live entry
that is inside a listed tmux session but was not the one consumed by that
session's row gets its own row, shaped like an outside row. Consequences:
a conversation with two live processes shows two rows with the same label
(and resolves `ambiguous` at tier 1, §3.2); a second Claude Code process
running in a window of someone else's tmux session is addressable by its
label; `whoami`, `holder` and the claim response always have a row to
return.

New fields, all always present:

```jsonc
{
  "address": "mini-lab/purdex-dev:mt0-purdex-49",   // <host>/<label>:<suffix>
  "row_kind": "session",             // session | entry
  "label": "purdex-dev",
  "label_source": "user",            // user | default | "" (no cc agent ⇒ no label)
  "suffix": "mt0-purdex-49",         // "" when no cc agent
  "session_code": "02ybs5",          // session rows only; "" on entry rows
  "session_name": "mt0",             // session rows only; tier-2 / tmux: address
  ...
}
```

Rules:

- A session row with no Claude Code agent (`agent: null`, or
  codex/opencode) has no `sessionId` to derive from: `label`,
  `label_source` and `suffix` are `""` and `address` is
  `<host>/tmux:<session_name>`.
- A session row whose agent is known only through `ownerFallbackAgent`
  (`inbox_dead`, `ambiguous`) still has a `sessionId`, hence a label and
  an address; it is not deliverable, as today. Its `sessionId` is live
  only if some live entry says so (§3.3), which for `ambiguous` is the
  case and for `inbox_dead` is not.
- Entry rows: `row_kind: "entry"`, `session_code`/`session_name`/
  `tmux_instance` empty, `cwd` from the entry, `agent` from the entry,
  `deliverable` by PB §4.2's inbox rule, suffix per §3.1 (from the entry's
  own `tmux` field). Every cc row also carries `label_rev` — the row's
  `rev` (§3.3), `0` when the conversation has no row — which is what
  `address_rev` (§3.5) is filled from.
- Proxy rows keep their helper's registry name as `address`, `label: ""`,
  and are excluded from tier 1.

`BuildInput` gains `Labels map[string]string` (sessionId ⇒ user label) and
`Build` stays a pure join: a `sessionId` absent from the map gets its
default label. The envelope gains `daemon_version` (this daemon's build
version) and `HostResult` carries it through for `pdx peers --all`.

### 3.5 Wire and helper naming (amends PB §4.4, §4.5)

`from` in `/api/peers/deliver` gains two fields:

| Field | Value |
|---|---|
| `address` | the origin's `<label>:<suffix>` as the sending daemon computed it at send time (or reply time, for the replier) |
| `address_rev` | the origin's row `rev` (§3.3), or `0` when the session has never had a row |

The receiver validates `address` syntactically only — `head` matches the
user-label rule or the default-label form, `rest` matches the suffix wire
grammar (§3.1) or is empty — refusing with `400 bad_address` otherwise,
and treats it as display data attributed to an authenticated host.
`address_rev` is compared only among requests for the **same origin
key**, so it is never compared across hosts or clocks. The wrapper's
`from-name` becomes `"<A alias>/<address>"`.

**Helper name follows the address, in place.** A helper (PB §4.5) is
keyed by the origin tuple; its registry `name` is `"<alias>/<address>"` at
spawn, and the helper record remembers `applied_rev` (the `address_rev` of
the request that spawned or last renamed it). A rename is an operation on
one helper **instance** (the record `Acquire` returned, identified by pid
and start time), executed under the helper manager's lock for that
record:

1. If the record is not `ready` (starting, stopping, released), skip —
   the frame is still delivered.
2. If `req.address_rev <= applied_rev`, skip. Otherwise set
   `applied_rev = req.address_rev` **even when the name is unchanged**
   (this is what defeats the A→B→A ordering hole, R2-5).
3. If the name differs: write `<dir>/.<pid>.json.tmp` (a name
   `ReadRegistry` classifies as *not a candidate*), containing the
   existing file with only `name` and `nameSince` (receiver's wall clock,
   ms) replaced; `rename(2)` it over `<pid>.json`. On any failure: unlink
   the temp file, log, roll `applied_rev` back to its previous value, and
   still deliver the frame — naming is display.
4. Update the record's `name`.

The wrapper written for that request uses the name snapshot taken under
the same lock; nothing reads `h.name` unlocked (R2-6). The socket, pid,
key, `files` list and `proxies.json` are untouched: nothing in flight is
lost, `Release`/cleanup still unlink by path, and the startup sweep still
judges ownership by `procStart`. A request whose rename lost the race to
`Release` (state `stopping`) simply skips at step 1. Claude Code's
`ListAgents` reads the registry directory on each call, so the new name is
visible on the next listing (verified in §5 P4b step 4).

The helper process is not told. P3 D8 stands: the daemon→helper channel is
still the first-line config only.

**Legacy requests.** A `/deliver` without `address` (a v1 sender) names a
freshly spawned helper `<alias>/<session_name or cc:peer_name>` as v1 did,
and applies no rename to an existing one. Such a helper has **no applied
revision**: the first later request from the same origin that does carry
`address` renames it whatever its `address_rev` is (a never-named
conversation legitimately sends `0`), and from then on the monotonic rule
applies. The revision of the request that spawned a helper is recorded at
admission, so a spawn whose waiter left before the helper was ready still
carries the revision that named it.

**Freshness.** A helper on host B representing origin A is renamed only by
a `/deliver` **from A** for that key. A reply from B's session to A
updates the helper on A that represents B (its `from.address` is the
replier's current address) and cannot update B's helper for A, because B
learns nothing about A's label from a reply. A rename with no subsequent
`/deliver` from the renamed session therefore leaves its old name on
remote hosts. After a release the origin's address is its default label
with the row's new `rev`, so the next `/deliver` does rename the helper
to the `_…` form. `pdx peers --all` is always current because it reads the
owning host's inventory; helper names are a convenience for `ListAgents`.

### 3.6 CLI and API

```
pdx msg name <label>        claim <label> for the calling session
pdx msg name --release      drop the calling session's label
pdx msg whoami              print the calling session's address
pdx msg send <host>/<label>[:<suffix>] <text>
pdx msg send <host>/tmux:<name> <text>
```

**Origin.** `name` and `whoami` read `CLAUDE_CODE_MESSAGING_SOCKET` and
send it as `origin_inbox`, like `send` (PB §4.4). The daemon maps it to a
**live, non-proxy registry entry** by inbox path — entry attribution, not
`send`'s "deliverable inventory row" — and the response record is that
entry's row (§3.4 guarantees there is one). Unset or unknown ⇒
`origin_unknown`. That rule is endpoint attribution for a trusted local
admin caller — any same-UID process can present another session's socket
path — and this spec does not promise more: `pdx msg name` is "name the
session that owns this inbox", not "prove I am that session". Both
commands talk to the **local** daemon only, with the admin token, through:

| Method | Path | Body | Returns |
|---|---|---|---|
| `POST` | `/api/peers/self` | `{origin_inbox}` | the caller's `PeerRecord` |
| `PUT` | `/api/peers/self/label` | `{origin_inbox, label}` | the updated record, or an error below |
| `DELETE` | `/api/peers/self/label` | `{origin_inbox}` | the updated record |

Both routes are admin-only: the PB §4.6 row for `/send` applies verbatim,
`HostRoutePolicy` denies them to a peer `inbound_token`, and that denial
is tested.

**Errors** use the existing `ipeers.APIError` body — wire key **`error`**
(`wire.go:255`; the CLI decoder at `msg.go:301` depends on it), plus
`detail` and the optional fields below, which are added to the struct:

| HTTP | `error` | Extra fields | From |
|---|---|---|---|
| 400 | `label_invalid` / `label_reserved` | — | claim |
| 400 | `origin_unknown` | — | claim, release, whoami |
| 400 | `bad_address` | — | `/deliver` with a malformed `from.address` |
| 409 | `label_taken` | `holder: PeerRecord`, `live_labels: []string` | claim |
| 503 | `not_ready` | `skipped: []string` | claim |
| 503 | `not_ready` | `partial: true` | `/send` when `Resolve` returns `ErrNotReady` |
| 503 | `store_unavailable` | — | claim, release, whoami |
| 404 | `peer_not_found` | — | `/send` (unchanged) |
| 409 | `ambiguous` | `candidates` | `/send` (unchanged) |

CLI text mode prints `error` and `detail`, `live_labels` one per line for
`label_taken`, and `skipped` one per line for `not_ready`; `--json` prints
the body verbatim. Exit status 0 only on 2xx. `whoami` reads the label
store; if that read fails it reports `store_unavailable` rather than
printing a default label as if it were the truth.

`pdx msg whoami` output (text mode):

```
address:  air/purdex-tester:purdex-3f
label:    purdex-tester (user, rev 7)
host:     air (air:9k2m4q)
session:  fa5d4c07-… pid 76973
```

`pdx peers` / `pdx peers --all` show the new `address` column, mark
`label_source: default` rows with `*`, show `row_kind: entry` rows
indented under their host, and print each host's `daemon_version` as a
trailer line after the table (`<alias>  daemon <version>`; both tables are
one aligned block, so a per-host header row is not used).

`pdx msg selftest` is **unchanged**. It is the harness-upgrade gate (P3
D4: the process acts as its own virtual peer and never calls the daemon),
so it cannot exercise labels. The label path is covered by the two-daemon
integration test in §4 and by §5.

### 3.7 Documentation contract for agents

`CLAUDE.md` (project) gains a short "Peer addresses" section — the grammar,
the label rule, the `_` meaning "unnamed", the naming convention
`<project>-<role>[-<n>]` (`purdex-tester`, `purdex-tester-2`), and the
three commands. The wording an operator uses to bootstrap a session is
then one sentence: *"You are the tester: run `pdx msg name
purdex-tester`."* The session confirms with `pdx msg whoami`.

### 3.8 Configuration

None. Label rule, reserved words, default-label form, `san`, and the `:`
separator are constants.

### 3.9 Compatibility (amends PB §4.8)

Alpha: no data migration, and **this is not a mixed-version protocol**.
Both hosts of a pair are upgraded together (the Peer Bridge fleet is
mlab + air-2026 today). The matrix below says what breaks if they are not,
so an operator can tell a version skew from a bug; it is not a supported
state.

| Sender | Target | Session kind | Result |
|---|---|---|---|
| v2 | v1 | tmux | bare tmux name still resolves (tier 2 over v1 rows, which carry `session_name`); `tmux:<name>` works; no labels |
| v2 | v1 | Desktop | **unreachable** by a new send: v1 rows have no `label`, no `session_name`, and `cc:` is gone. Replies to an existing helper still work (tuple-bound) |
| v1 | v2 | tmux | v1 resolves by its own tiers over v2 rows (`session_name` / `session_code` are kept on session rows); works |
| v1 | v2 | Desktop | v1 resolves `cc:<peer_name>` over v2 rows (`agent.peer_name` is kept); works. The v2 listing's `address` is not usable from v1 |
| v1 | v2 | any | `/deliver` without `from.address` ⇒ legacy helper name, never renamed by that request (§3.5) |

Skew is visible in `pdx peers --all`'s host header (`daemon_version`,
§3.4). PB §4.8's version warning is about the Claude Code version and does
not detect daemon skew.

## 4. Phases

Two PRs. Each has a pre-deployment gate that runs in CI (`go test`); §5
lists the post-deployment confirmation per phase.

### P4a — Address protocol end to end

Everything a sender and a target need for `pdx msg send air/purdex-tester`
to work, with the v1 `from-name` still on the wire.

- Registry diagnosis (§3.3 classes) returned by `ReadRegistry`; inventory
  `partial` + `unknown_registry_files`; `daemon_version` in the envelope.
- `peer_labels` / `peer_label_seq` tables + store (claim / release /
  snapshot), `labelMu`.
- `PeerRecord` fields, entry rows, `BuildInput.Labels`, `enc` + `san`.
- `peers.Resolve` v2 (`cc` short-circuit, `tmux:`, tier 1 over all rows,
  tier 2, `ErrNotReady`); `handleSend` maps `ErrNotReady`.
- `/api/peers/self`, `/self/label` routes + `HostRoutePolicy` denial;
  `pdx msg name|whoami`; `pdx peers` columns.
- `CLAUDE.md` section (§3.7) — agents need it from the first deploy.
- Tests: label rule + reserved table; `enc` golden vectors and padding;
  `san` table and the 65-character suffix bound; registry diagnosis table
  (each class; `EPERM` ⇒ unknown; dead-pid unknown ignored, live-pid
  unknown ⇒ `partial` and `not_ready`); claim matrix (§3.3, every row,
  resume-after-crash, two concurrent claims, `rev` strictly increasing
  across claim/release/restart); Resolve table (`cc` with partial
  true/false, `tmux:`, label, label+suffix, tier 2, partial ⇒
  `ErrNotReady`, default-label collision ⇒ ambiguous, same-conversation
  two entries ⇒ ambiguous via two rows, tmux unique-pane tiebreak still
  deliverable via `tmux:`); Build: entry rows (non-owner entry in a listed
  session gets a row; every live entry appears exactly once); origin by
  entry (an entry with only an entry row can name itself); label-store
  read failure ⇒ `partial`; **the R2-1 scenario** (live holder of label
  `mt0` unreadable + another tmux session named `mt0` ⇒ bare `mt0` is
  `not_ready`, `tmux:mt0` delivers); **two-daemon e2e** (existing
  `internal/module/peers/e2e_test.go` harness): claim on B, send from A by
  label, deliver, native reply — asserting the tuple, not the name.

Deployment: both hosts. Until P4b, `from-name` and helper names are the
v1 form.

### P4b — Wire display and helper renames

- `from.address` / `address_rev` on `/deliver` and on replies; receiver
  syntactic validation (`bad_address`); `from-name` in the wrapper.
- Per-helper `applied_rev` and name; instance-bound rename (§3.5 steps
  1–4) under the helper manager lock; locked name snapshot for the
  wrapper.
- Peer Bridge spec §4.1/§4.2/§4.4/§4.5/§4.8 get a one-line pointer to this
  spec.
- Tests: e2e wrapper `from-name`; rename on newer `address_rev` (same
  key, file rewritten, socket/pid unchanged, a frame written during the
  rename is delivered); equal/older `address_rev` ignored; **A→B→A**
  ordering (`(A,10)`, `(A,30)`, late `(B,20)` ⇒ name stays A); rename vs
  `Release` race (stopping ⇒ skip, no file recreated after cleanup);
  rewrite failure ⇒ frame delivered, `applied_rev` rolled back; legacy
  request without `address` ⇒ v1 name, no rename; malformed `address` ⇒
  `400 bad_address`.

Deployment: both hosts.

## 5. Acceptance (manual)

Run once on mlab against two isolated daemons, then once for real between
mlab and air-2026. **P4a subset:** steps 1, 2 (with the v1 `from-name` and
helper name expected), 3, 5, 6, 7, 8, 9, 10, 11, 12. **P4b:** all steps,
with the P4b expectations.

**Isolation.** Each daemon has its own `--config` and `data_dir`, hence its
own `meta.db`, `peer_labels` and `peer_label_seq`. The Claude Code registry
(`~/.claude/sessions/`) is per user and therefore **shared** by both local
daemons; that is real (it is how P3 was verified) and it is why the local
run cannot test "Desktop on the other host" — the local stand-in for a
Desktop session is a Claude Code session started outside tmux
(`claude` in a plain terminal), and only the mlab↔air run exercises real
Desktop. Record for every step: `daemon_version` on both ends from
`pdx peers --all`, `pdx msg whoami` output, and, for steps that send a
message, the `msg_id` from `pdx msg log` and the reply text; `ListAgents`
output where named.

1. **Name and see.** In a tmux Claude Code session on mlab:
   `pdx msg name purdex-dev` → `pdx msg whoami` shows
   `mini-lab/purdex-dev:mtN-purdex-xx`; `pdx peers --all` on air shows the
   same row; `pdx peers --all` on mlab marks other sessions with `*`.
2. **Desktop tester.** Start a Claude Desktop session on air, send it one
   message so it registers, then tell it: "run `pdx msg name
   purdex-tester`". From mlab: `pdx msg send air/purdex-tester "ping"` is
   delivered; the tester replies natively; the reply arrives on mlab.
   P4a: `from-name="air/cc:purdex-xx"`, `ListAgents` on mlab lists
   `air/cc:purdex-xx`. P4b: `from-name="air/purdex-tester:purdex-xx"`,
   `ListAgents` on mlab lists `air/purdex-tester:purdex-xx` — the helper
   **mlab** spawned for the replier.
3. **Taken.** A second Desktop session on air runs
   `pdx msg name purdex-tester` → `label_taken`; the output lists
   `purdex-tester` among `live_labels` and shows the holder's suffix.
4. **Same session renames (P4b).** The tester runs `pdx msg name
   purdex-tester-2`, replies to mlab once more: on mlab `ListAgents` shows
   exactly one entry for that helper, now `air/purdex-tester-2:purdex-xx`,
   same socket path as in step 2 (`~/.claude/sessions/<helper pid>.json`
   rewritten, pid unchanged). Then it runs `pdx msg name purdex-tester`
   and replies again: the same helper is renamed back. (The helper on
   **air** representing mlab's sender is unaffected throughout.)
5. **Crash and take over.** Quit the first Desktop session. The second
   one runs `pdx msg name purdex-tester` → success (the first session's
   label was `purdex-tester` again after step 4, and it is now confirmed
   dead). From mlab, `pdx msg send air/purdex-tester "ping"` reaches the
   **second** session; it replies; mlab now has a **new** helper for it
   (different `sessionId` ⇒ different key), and the old helper for the
   first session remains on mlab until idle reap or `target_gone` —
   `ListAgents` on mlab may show both `air/purdex-tester:…` entries until
   then. Expected; note the two socket paths.
6. **Resume comes back unnamed.** Resume the first session
   (`claude --resume`, same `sessionId`): `pdx msg whoami` shows a `_…`
   label; `pdx msg name purdex-tester` → `label_taken`.
7. **Release.** The second session runs `pdx msg name --release`;
   `whoami` shows `_…` and a higher `rev`; the first session claims
   `purdex-tester` → success.
8. **Stale suffix is harmless.** From mlab, send to
   `air/purdex-tester:<suffix from step 2>` → delivered to the current
   holder (suffix ignored).
9. **Fallback.** `pdx msg send mini-lab/tmux:mt0 "x"` from air delivers;
   `pdx msg send mini-lab/mt0 "x"` delivers (tier 2);
   `pdx msg send mini-lab/cc:purdex-49 "x"` fails `peer_not_found` with
   the `pdx peers --all` hint.
10. **Not ready.** On air, create `~/.claude/sessions/<pid>.json`
    containing `{` where `<pid>` is a live shell's pid: `pdx peers` on
    mlab shows air `partial: true` with that file in
    `unknown_registry_files`; `pdx msg name anything` on air →
    `not_ready` naming that file; `pdx msg send air/purdex-tester …` from
    mlab → `not_ready`; `pdx msg send air/tmux:<name> …` still delivers to
    a tmux session; remove the file → all succeed.
11. **Restart.** `pdx stop` / `pdx start` on air; the tester's label and
    `rev` are still there (`whoami`); occupancy re-derived from the live
    registry.
12. `pdx msg selftest` still passes on both hosts (harness gate, not a
    label test).

## 6. Risks

| Risk | Mitigation |
|---|---|
| Default label collision on one host (36⁶ space) | resolves as `ambiguous`; naming either session ends it; defaults cannot collide with user labels by construction |
| Two sessions claim one label at the same moment | `labelMu` on the owning host; the second sees `label_taken` |
| A holder is resumed a moment after being judged not live | same outcome as resume-after-claim (§3.3): it comes back unnamed and is told so by `whoami` |
| A registry file for a live pid cannot be read | claims refuse with `not_ready`; inventory is `partial`, so bare-name resolution refuses too instead of falling to a tmux name |
| A conversation has two live processes | two rows, `ambiguous` at tier 1; `tmux:` still reaches the owner |
| A delayed `/deliver` carries an old address | host-wide `rev`, advanced on every newer request even without a name change |
| Rename races helper release | instance-bound, under the manager lock; `stopping` ⇒ skip |
| Operators keep typing `cc:` from old notes | short-circuit ⇒ deterministic `peer_not_found` with the `pdx peers --all` hint |
| A label shadows a tmux name | documented tier order; `tmux:<name>` for the unshadowable form |
| Helper names go stale on remote hosts | stated as a limit (§3.5 Freshness); `pdx peers --all` is the source of truth |
| Label store grows with released/dead rows | lazy cleanup on claim; a time-based sweep is noted as future work |

## 7. Decisions recorded

1. **Label is Purdex-owned, keyed by Claude Code `sessionId`.** Not the
   registry name (Claude Code owns that file; only `/rename` and
   `--name` can change it, neither reachable by Purdex) and not tmux (absent
   for Desktop, ids float across restarts).
2. **Occupancy = registry-entry liveness, with an explicit "cannot tell"
   that also gates resolution.** No takeover of a live holder; no manual
   clean-up of a dead one; a resumed former holder comes back unnamed; an
   unknown registry file for a live pid blocks claims (`not_ready`) and
   marks the inventory `partial` so bare names refuse rather than fall
   through.
3. **Default labels live in a namespace users cannot claim** (`_` prefix).
4. **Suffix is display, not identity, and not verified.** Typing it is
   harmless; the resolver strips it.
5. **`:` as separator; `cc` and `tmux` reserved and short-circuited.**
6. **Label charset is ASCII lowercase only.** English labels were the
   user's preference; near-collisions are reduced by construction.
7. **Removed tiers: session code and `cc:`; tmux name kept as bare
   fallback plus explicit `tmux:`.**
8. **Every live entry has one inventory row** (entry rows). Makes
   "conversation with several processes" a visible `ambiguous` at tier 1
   instead of an invisible choice, and gives `whoami`/`holder` a row to
   return. `tmux:` keeps PB §4.2's pane tiebreak.
9. **Helper rename = in-place registry rewrite by the daemon**, bound to a
   helper instance, gated by a host-wide `rev` that advances even when the
   name is unchanged; no respawn (R1 #10), no new stdin command (P3 D8).
10. **Freshness is one-directional** (R1 #12).
11. **`pdx msg selftest` is untouched** (R1 #14).
12. **Not a mixed-version protocol** (R1 #15/#16).
13. **Future hook — label hand-over.** `pdx msg name --release` already lets
    a departing session free its name deliberately. A later
    `pdx msg name <label> --inherit <sessionId>` (explicit, both parties
    alive) is the natural extension if the resume-loses-name rule turns out
    to bite in practice. Not in this spec.

## 8. Review disposition (R1 `task-mu1ags4q-o5eeev`)

| # | Sev | Finding | Disposition (v2) | R2 verdict → v3 |
|---|---|---|---|---|
| 1 | Blocker | inventory absence ≠ holder dead | entry-level liveness; completeness rule | partial → R2-1/R2-2, closed in v3 §3.3 |
| 2 | Major | `CfgMu` misuse | dedicated `labelMu` | resolved |
| 3 | Major | row / entry / conversation conflated | one label per conversation | partial → R2-3/R2-4, closed in v3 §3.2/§3.4 |
| 4 | Major | default labels not in the table | `_` namespace | resolved |
| 5 | Major | labels shadow tmux; partial ⇒ wrong tier | `tmux:`; partial ⇒ `not_ready` | partial → R2-1, closed in v3 |
| 6 | Major | `cc` valid label | reserved | resolved (R2-10 short-circuit added) |
| 7 | Major | suffix cannot prove hand-over | display only | resolved |
| 8 | Minor | suffix grammar undefined | `san` | partial → R2-8, closed in v3 §3.1 |
| 9 | Minor | base36 skew | `mod 36⁶` + padding | resolved |
| 10 | Blocker | reap drops frames | in-place rewrite | resolved |
| 11 | Major | rename ordering | `address_since` | partial → R2-5/R2-6, closed in v3 §3.5 (`rev`) |
| 12 | Major | freshness overclaimed | one-directional | resolved |
| 13 | Minor | sweep / `proxies.json` | moot | resolved |
| 14 | Major | selftest misread | untouched; e2e | resolved |
| 15 | Major | compatibility overgeneralised | matrix | partial → R2-11, closed in v3 (`daemon_version`) |
| 16 | Major | P4a not host-local | re-cut | partial → R2-12, closed in v3 §5 |
| 17 | Major | acceptance premise | separated | unresolved → R2-7, rewritten in v3 §5 |
| O1–O5 | — | omissions | added | O1/O2/O5 partial → R2-3/R2-9/R2-12, closed in v3 |

## 9. Review disposition (R2 `task-mu1ayutr-i477ud`)

| # | Sev | Finding | Disposition |
|---|---|---|---|
| R2-1 | Blocker | registry unknowns not in inventory `partial` ⇒ bare name can fall to tier 2 and hit the wrong conversation | accepted — one registry diagnosis feeds `partial` and `not_ready`; `unknown_registry_files`; the scenario is a named P4a test (§3.3, §4) |
| R2-2 | Major | `skipped` ≠ unknown | accepted — three classes with the exact checks; `EPERM` ⇒ exists; dead-pid unknowns ignored (§3.3) |
| R2-3 | Major | origin / self record / holder undefined for entries without rows | accepted — entry rows (§3.4); origin by entry attribution; `whoami` `store_unavailable` (§3.6) |
| R2-4 | Major | multi-process conversation not "always ambiguous" | accepted — tier 1 over all rows gives a conversation-level gate; `tmux:` keeps the pane tiebreak (§3.2, D8) |
| R2-5 | Major | `set_at` not monotonic; A→B→A | accepted — host-wide `rev` from `peer_label_seq`, advanced on release too; `applied_rev` advances on every newer request (§3.3, §3.5) |
| R2-6 | Major | rewrite not atomic w.r.t. helper lifecycle | accepted — instance-bound rename under the manager lock, ready-check, rollback on failure, locked name snapshot (§3.5) |
| R2-7 | Major | acceptance take-over sequence and helper side wrong | accepted — §5 steps 4/5 rewritten; helper for the tester lives on mlab |
| R2-8 | Major | suffix 65 vs 32 | accepted — wire grammar `{1,65}` (§3.1, §3.5) |
| R2-9 | Major | `APIError` key is `error` | accepted — §3.6 |
| R2-10 | Minor | `cc:` vs partial branch | accepted — short-circuit before tiers (§3.2) |
| R2-11 | Minor | no daemon version in `--all` | accepted — `daemon_version` in the envelope, P4a (§3.4, §3.9) |
| R2-12 | Minor | acceptance timing | accepted — P4a subset with v1 expectations; references fixed (§5) |
