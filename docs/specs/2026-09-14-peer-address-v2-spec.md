# Spec — Peer address v2: session labels ("Peer Address v2")

Status: draft v2 (R1 `task-mu1ags4q-o5eeev`: 2 Blockers, 12 Majors, 3 Minors,
5 omissions — all accepted; §8 holds the disposition table)
Date: 2026-09-14
Branch: `worktree-peer-address-v2`
Amends: `2026-09-13-peer-bridge-spec.md` v3.2 (§4.1, §4.2, §4.4, §4.5, §4.7, §4.8)

> This spec replaces the `<session>` half of the human address. Wire identity,
> authentication, delivery and audit are unchanged unless a section below
> says otherwise. Section numbers with a "PB" prefix refer to the Peer Bridge
> spec; "P3 Dn" to the P3 plan's decision table.
>
> **v2 (after R1):** occupancy is decided on registry-entry liveness with an
> explicit "cannot tell" outcome (§3.3); default labels live in a namespace
> users cannot claim (§3.1); the suffix is display only (§3.1, §3.2); helper
> renames rewrite the registry file instead of respawning (§3.5); the
> compatibility matrix and phases are rewritten (§3.9, §4); acceptance
> separates "same session renames" from "another session takes over" (§5).

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
   when the daemon cannot tell, it refuses rather than guesses.
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
the label (or the `tmux` keyword); everything after it is the suffix and is
discarded by the resolver.

**User label rule.** `^[a-z0-9][a-z0-9-]{1,31}$` — lowercase ASCII letters,
digits and hyphen, 2–32 characters, must not start with a hyphen. Anything
else is refused at claim time with `label_invalid`. There is no
case-folding: a label that would need folding is simply invalid, so two
labels that look alike never differ only in case. **Reserved:** `cc` and
`tmux` are refused with `label_reserved`, so the old `cc:<name>` form and
the new `tmux:<name>` form can never be parsed as a label plus suffix.

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

- inside tmux: `san(tmux session name) + "-" + san(registry name)`
- outside tmux: `san(registry name)`
- `san` keeps `[A-Za-z0-9_.-]`, replaces every other byte with `_`, and
  truncates to 32 characters; an empty input becomes `_`.

The suffix is not a unique encoding (both parts may contain `-`) and it is
not identity: two sessions can share a suffix, and one session's suffix
changes when its tmux session or registry name changes. Nothing in this
spec depends on it beyond a human reading it. On input the resolver strips
it without looking at it (§3.2).

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
`session` is first split at its first `:` into `head` and `rest`.

| Case | Matches when | Notes |
|---|---|---|
| `head == "tmux"` | `rest` equals a record's `session_name` | explicit fallback; labels never considered; `rest` empty ⇒ `ErrNotFound` |
| tier 1: label | `head` equals a record's `label` | proxy rows and rows with `label == ""` excluded; `rest` (the typed suffix) is ignored |
| tier 2: tmux name | `head` equals a record's `session_name` and `rest == ""` | reached only when tier 1 has no match **and** `partial == false` |
| tier 1 miss, `partial == true` | — | `ErrNotReady` — the snapshot may be missing the label the caller meant (PB §4.2 deadline, or a label-store read failure, §3.3); the caller retries or uses `tmux:` |

Removed: the session-code tier and the `cc:` tier. `cc:<name>` now fails
with `ErrNotFound` because `cc` is reserved (§3.1); the error text points
to `pdx peers --all` so an agent following old instructions recovers in
one step.

**Shadowing.** A user label equal to another session's tmux name wins at
tier 1; that is what tier order means. Operators who need the tmux session
regardless of labels use `tmux:<name>`. The claim path does not refuse
such labels — tmux sessions appear and disappear independently of claims,
so a refusal there would be a false promise.

**One label per conversation.** A label (default or user) belongs to a
`sessionId`. If one conversation has more than one live process, PB §4.2
already reports that row as `deliverable: false, reason: ambiguous`; tier 1
resolves to that row and the send fails as today. Naming does not and
cannot pick one process. (This is distinct from two *different*
conversations whose default labels collide — 36⁶ space — which resolve as
`ambiguous` at tier 1 and are fixed by naming either one.)

**Snapshot semantics.** The sender resolves over a snapshot taken at send
time and only the resulting tuple travels (PB §4.4). A label released or
re-claimed between the snapshot and `/deliver` does not change where the
message goes: the receiver verifies the tuple, not the name. This is the
same window PB §4.4 already accepts for tmux renames, restated here so
that "the label moved" is never read as "the message should have moved".

### 3.3 Label store and occupancy

Table `peer_labels` in `meta.db`, host-local, never replicated:

```sql
CREATE TABLE IF NOT EXISTS peer_labels (
    session_id TEXT PRIMARY KEY,   -- Claude Code sessionId
    label      TEXT NOT NULL UNIQUE,
    set_at     INTEGER NOT NULL    -- unix ms; also address_since on the wire (§3.5)
);
```

One user label per conversation, one conversation per user label, by
schema. Default labels are never stored.

**Liveness of a conversation** is decided on the Claude Code registry, at
entry level, not on inventory rows: `sessionId S` is *live* iff at least
one registry entry with that `sessionId` passes PB §4.2 liveness (socket
path exists, `kill(pid, 0)` succeeds, process start time matches
`procStart`). Owner resolution, tmux, `partial` and row `reason`s play no
part; a session whose inventory row says `inbox_dead` or `ambiguous` is
still live if its entry is.

**Evidence quality.** A registry read is *complete* for this purpose iff
every file under `~/.claude/sessions/` that could not be read, decoded or
classified (`ReadRegistry`'s `skipped`) names, in its filename, a pid that
is **not** alive. A skipped file for a live pid is an entry whose
`sessionId` the daemon cannot see; while one exists, no label can be
proven free. (A skipped file for a dead pid cannot be a live holder and
is ignored, so one stale malformed file cannot block claims forever.)
`ReadRegistry` is extended to return the skipped filenames so the peers
module can apply this rule.

A label `L` is *held* iff a row `(S, L)` exists **and** `S` is live. A row
whose session is not live is inert: it neither resolves nor blocks.

**Claim** `(S', L)` — the peers module's dedicated `labelMu` (never
`Core.CfgMu`) is held from the registry read through the transaction:

| Situation | Result |
|---|---|
| `L` fails the user label rule | `400 label_invalid` |
| `L` is `cc` or `tmux` | `400 label_reserved` |
| `S'` is not the `sessionId` of a live, non-proxy registry entry | `400 origin_unknown` (same as `/send`) |
| registry read failed, or is not complete (above) | `503 not_ready`, `detail` names the skipped files |
| label store read or write failed | `503 store_unavailable` |
| `L` held by live `S ≠ S'` | `409 label_taken` — body carries the holder's record and `live_labels` (every held label on this host) so the caller can pick a free one without a second round-trip |
| `(S', L)` already the row | `200`, no write |
| otherwise | one transaction: delete any row with `label = L` (a holder that is not live), delete any row with `session_id = S'` (the caller's previous label), insert `(S', L, now)`. `200` with the updated record |

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

**Release** `S'`: delete the row for `S'` if any; `200`. The session's
address reverts to its default label immediately. `origin_unknown` and
`store_unavailable` as for claim; no registry completeness requirement.

**Inventory read of labels.** `GET /api/peers` reads the whole table once
per call. If that read fails, every row is reported with its default label
and `label_source: "default"`, and the envelope is marked `partial: true`
(§3.2 turns that into `not_ready` for bare-name resolution rather than
letting a caller conclude a name does not exist). The failure is logged
once per call.

**Garbage.** Rows for sessions that never come back accumulate at the rate
sessions are named, which is small, and are removed lazily by the next
claim of the same label. No sweeper in this spec.

### 3.4 Peer record (amends PB §4.2)

New fields, all always present:

```jsonc
{
  "address": "mini-lab/purdex-dev:mt0-purdex-49",   // <host>/<label>:<suffix>
  "label": "purdex-dev",
  "label_source": "user",            // user | default | "" (no cc agent ⇒ no label)
  "suffix": "mt0-purdex-49",         // "" when no cc agent
  "session_code": "02ybs5",          // kept for the UI/session API; no longer an address
  "session_name": "mt0",             // tier-2 / tmux: fallback address
  ...
}
```

A row with no Claude Code agent (`agent: null`, or codex/opencode) has no
`sessionId` to derive from: `label`, `label_source` and `suffix` are `""`
and `address` is `<host>/tmux:<session_name>`. A row whose agent is known
only through `ownerFallbackAgent` (`inbox_dead`, `ambiguous`) still has a
`sessionId` and therefore a label and an address; it is simply not
deliverable, as today. Proxy rows keep their helper's registry name as
`address`, `label: ""`, and are excluded from tier 1.

`BuildInput` gains `Labels map[string]string` (sessionId ⇒ user label) and
`Build` stays a pure join: a `sessionId` absent from the map gets its
default label.

### 3.5 Wire and helper naming (amends PB §4.4, §4.5)

`from` in `/api/peers/deliver` gains two fields:

| Field | Value |
|---|---|
| `address` | the origin's `<label>:<suffix>` as the sending daemon computed it at send time (or reply time, for the replier) |
| `address_since` | `set_at` of the origin's label row, or `0` for a default label |

The receiver validates `address` only syntactically (head matches the
user-label rule or the default-label form; suffix, if present, is `san`
output) and treats it as display data attributed to an authenticated host.
The wrapper's `from-name` becomes `"<A alias>/<address>"`.

**Helper name follows the address, in place.** A helper (PB §4.5) is
keyed by the origin tuple and its registry `name` is `"<alias>/<address>"`
at spawn. The daemon remembers, per helper, the `(address, address_since)`
it last applied. When a `/deliver` for the same key carries a different
`address` **and** an `address_since` strictly greater than the remembered
one, the daemon rewrites the helper's `<pid>.json` — the file Purdex
itself created — with only `name` and `nameSince` changed, atomically
(temp file in the same directory + rename), before writing the frame.
Equal or older `address_since` is ignored, so a delayed `/deliver` from
before a rename cannot turn the name back. The socket, pid and key are
untouched: nothing in flight is lost, `Acquire`/`writeFrame` (P3) keep
their lifecycle unchanged, and `proxies.json` needs no new field. Claude
Code's `ListAgents` reads the registry directory on each call, so the new
name is visible on the next listing (verified in §5.6).

The helper process is not told. P3 D8 stands: the daemon→helper channel is
still the first-line config only.

A release (§3.3) produces a default address with `address_since: 0`, which
the monotonic rule ignores; remote helpers therefore keep the last user
name until the session claims a new one. Accepted: a released session is
on its way to being renamed or closed, and `pdx peers --all` is current.

**Freshness.** A helper on host B representing origin A is renamed only by
a `/deliver` **from A** for that key. A reply from B's session to A
updates the helper on A that represents B (its `from.address` is the
replier's current address) and cannot update B's helper for A, because B
learns nothing about A's label from a reply. A rename with no subsequent
`/deliver` from the renamed session therefore leaves its old name on
remote hosts. `pdx peers --all` is always current because it reads the
owning host's inventory; helper names are a convenience for `ListAgents`.

### 3.6 CLI and API

```
pdx msg name <label>        claim <label> for the calling session
pdx msg name --release      drop the calling session's label
pdx msg whoami              print the calling session's address
pdx msg send <host>/<label>[:<suffix>] <text>
pdx msg send <host>/tmux:<name> <text>
```

`name` and `whoami` identify the caller exactly as `send` does (PB §4.4
Origin): `CLAUDE_CODE_MESSAGING_SOCKET` ⇒ `origin_inbox` ⇒ live, non-proxy
registry entry; unset or unknown ⇒ `origin_unknown`. That rule is endpoint
attribution for a trusted local admin caller — any same-UID process can
present another session's socket path — and this spec does not promise
more: `pdx msg name` is "name the session that owns this inbox", not "prove
I am that session". Both commands talk to the **local** daemon only, with
the admin token, through:

| Method | Path | Body | Returns |
|---|---|---|---|
| `POST` | `/api/peers/self` | `{origin_inbox}` | the caller's `PeerRecord` |
| `PUT` | `/api/peers/self/label` | `{origin_inbox, label}` | the updated record, or an error below |
| `DELETE` | `/api/peers/self/label` | `{origin_inbox}` | the updated record |

Both routes are admin-only: the PB §4.6 row for `/send` applies verbatim,
`HostRoutePolicy` denies them to a peer `inbound_token`, and that denial
is tested.

**Errors** use the existing `ipeers.APIError` shape `{code, detail, …}`:

| HTTP | `code` | Extra fields | From |
|---|---|---|---|
| 400 | `label_invalid` / `label_reserved` | — | claim |
| 400 | `origin_unknown` | — | claim, release, whoami |
| 409 | `label_taken` | `holder: PeerRecord`, `live_labels: []string` | claim |
| 503 | `not_ready` | `skipped: []string` | claim |
| 503 | `store_unavailable` | — | claim, release |
| 404 | `peer_not_found` | — | `/send` when `Resolve` returns `ErrNotFound` (unchanged) |
| 409 | `ambiguous` | `candidates` | `/send` (unchanged) |
| 503 | `not_ready` | `partial: true` | `/send` when `Resolve` returns `ErrNotReady` — `handleSend`'s error mapping gains this case |

CLI text mode prints `code` and `detail`, plus `live_labels` one per line
for `label_taken`; `--json` prints the body verbatim. Exit status 0 only
on 2xx.

`pdx msg whoami` output (text mode):

```
address:  air/purdex-tester:purdex-3f
label:    purdex-tester (user)
host:     air (air:9k2m4q)
session:  fa5d4c07-… pid 76973
```

`pdx peers` / `pdx peers --all` show the new `address` column and mark
`label_source: default` rows with `*` so an operator sees at a glance which
sessions are unnamed.

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
| v1 | v2 | tmux | v1 resolves by its own tiers over v2 rows (`session_name` / `session_code` are kept); works |
| v1 | v2 | Desktop | v1 resolves `cc:<peer_name>` over v2 rows (`agent.peer_name` is kept); works. The v2 listing's `address` is not usable from v1 |
| v1 | v2 | any | `/deliver` without `from.address` ⇒ v2 names the helper `<alias>/<session_name or cc:peer_name>` as v1 did and never rewrites it |

PB §4.8's version warning is about the Claude Code version and does not
detect daemon skew; `pdx peers --all` shows each host's daemon version
(P2 envelope) and that is the check.

## 4. Phases

Two PRs. Each has a pre-deployment gate that runs in CI (`go test`), and
§5 is the post-deployment confirmation for both.

### P4a — Address protocol end to end

Everything a sender and a target need for `pdx msg send air/purdex-tester`
to work, with the old `from-name` still on the wire.

- `peer_labels` table + store (claim / release / snapshot), `labelMu`.
- `ReadRegistry` returns skipped filenames; completeness rule (§3.3).
- `PeerRecord` fields, `BuildInput.Labels`, default-label `enc` + `san`.
- `peers.Resolve` v2 (`tmux:`, tier 1, tier 2, `ErrNotReady`, reserved
  words); `handleSend` maps `ErrNotReady` ⇒ `503 not_ready`.
- `/api/peers/self`, `/self/label` routes + `HostRoutePolicy` denial;
  `pdx msg name|whoami`; `pdx peers` address column and `*` marker.
- `CLAUDE.md` section (§3.7) — agents need it from the first deploy.
- Tests: label rule + reserved table; `enc` golden vectors and padding;
  `san` table; claim matrix (§3.3, every row, plus resume-after-crash and
  two concurrent claims); completeness (skipped file with live pid ⇒
  `not_ready`, with dead pid ⇒ ignored); Resolve table (`tmux:`, label,
  label+suffix, tier 2, partial ⇒ `ErrNotReady`, `cc:` ⇒ not found,
  default-label collision ⇒ ambiguous, same-conversation multi-process ⇒
  row not deliverable); `whoami`/`name` origin handling; label-store read
  failure ⇒ `partial: true`; **two-daemon e2e** (existing
  `internal/module/peers/e2e_test.go` harness): claim on B, send from A
  by label, deliver, native reply — asserting the tuple, not the name.

Deployment: both hosts. Until P4b, `from-name` and helper names are the
v1 form (`<alias>/<session_name or cc:peer_name>`).

### P4b — Wire display and helper renames

- `from.address` / `address_since` on `/deliver` and on replies;
  `from-name` in the wrapper; receiver-side syntactic validation.
- Per-helper `(address, address_since)` memory; in-place `<pid>.json`
  rewrite on a newer address (§3.5).
- Peer Bridge spec §4.1/§4.2/§4.4/§4.5/§4.8 get a one-line pointer to this
  spec.
- Tests: e2e wrapper `from-name`; helper rename on newer `address_since`
  (same key, file rewritten, socket and pid unchanged, a frame written
  during the rename is delivered); older/equal `address_since` ignored;
  `/deliver` without `address` (v1 sender) ⇒ v1 name, never rewritten;
  malformed `address` ⇒ `400`.

Deployment: both hosts.

## 5. Acceptance (manual, after P4b is deployed)

Run once on mlab against two isolated daemons, then once for real between
mlab and air-2026.

**Isolation.** Each daemon has its own `--config` and `data_dir`, hence its
own `meta.db` and `peer_labels`. The Claude Code registry
(`~/.claude/sessions/`) is per user and therefore **shared** by both local
daemons; that is real (it is how P3 was verified) and it is why the local
run cannot test "Desktop on the other host" — only the mlab↔air run can.
Record for every step: daemon version on both ends, `pdx msg whoami`
output, the `msg_id` from `pdx msg log`, the reply text, and `ListAgents`
output where named.

1. **Name and see.** In a tmux Claude Code session on mlab:
   `pdx msg name purdex-dev` → `pdx msg whoami` shows
   `mini-lab/purdex-dev:mtN-purdex-xx`; `pdx peers --all` on air shows the
   same row; `pdx peers --all` on mlab marks other sessions with `*`.
2. **Desktop tester.** Start a Claude Desktop session on air, send it one
   message so it registers, then tell it: "run `pdx msg name
   purdex-tester`". From mlab: `pdx msg send air/purdex-tester "ping"` is
   delivered; the tester replies natively; the reply arrives on mlab with
   `from-name="air/purdex-tester:purdex-xx"`; `ListAgents` on mlab now
   lists `air/purdex-tester:purdex-xx` (the helper A spawned for the
   replier).
3. **Taken.** A second Desktop session on air runs
   `pdx msg name purdex-tester` → `label_taken`; the output lists
   `purdex-tester` among `live_labels` and shows the holder's suffix.
4. **Same session renames.** The tester runs `pdx msg name
   purdex-tester-2`, then replies to mlab once more. On mlab:
   `ListAgents` shows exactly one entry for that helper, now named
   `air/purdex-tester-2:purdex-xx`, with the same socket path as in step
   2 (`~/.claude/sessions/<helper pid>.json` rewritten, pid unchanged).
5. **Crash and take over.** Quit the first Desktop session. The second one
   runs `pdx msg name purdex-tester` → success. From mlab,
   `pdx msg send air/purdex-tester "ping"` reaches the **second** session
   (different `sessionId`, so a **new** helper key on air; the old helper
   for the first session stays until idle reap or `target_gone`, and
   `ListAgents` on air may show both until then — expected).
6. **Resume comes back unnamed.** Resume the first session
   (`claude --resume`, same `sessionId`): `pdx msg whoami` shows a `_…`
   label; `pdx msg name purdex-tester` → `label_taken`.
7. **Release.** The second session runs `pdx msg name --release`;
   `whoami` shows `_…`; the first session claims `purdex-tester` →
   success.
8. **Stale suffix is harmless.** From mlab, send to
   `air/purdex-tester:<old suffix from step 2>` → delivered to the
   current holder (suffix ignored).
9. **Fallback.** `pdx msg send mini-lab/tmux:mt0 "x"` from air delivers;
   `pdx msg send mini-lab/mt0 "x"` delivers (tier 2);
   `pdx msg send mini-lab/cc:purdex-49 "x"` fails `peer_not_found` with
   the `pdx peers --all` hint.
10. **Not ready.** Drop an unreadable file named after a live pid into
    `~/.claude/sessions/` on air (e.g. `<pid of a shell>.json` containing
    `{`); `pdx msg name anything` → `not_ready` naming that file; remove
    it → claim succeeds.
11. **Restart.** `pdx stop` / `pdx start` on air; the tester's label is
    still there (`whoami`); occupancy re-derived from the live registry.
12. `pdx msg selftest` still passes on both hosts (harness gate, not a
    label test).

## 6. Risks

| Risk | Mitigation |
|---|---|
| Default label collision on one host (36⁶ space) | resolves as `ambiguous`; naming either session ends it; defaults cannot collide with user labels by construction |
| Two sessions claim one label at the same moment | `labelMu` on the owning host; the second sees `label_taken` |
| A holder is resumed a moment after being judged not live | same outcome as resume-after-claim (§3.3): it comes back unnamed and is told so by `whoami` |
| A registry file for a live pid cannot be read | claims refuse with `not_ready` naming the file rather than reclaiming over an unseen holder |
| A delayed `/deliver` carries an old address | `address_since` monotonic check; older is ignored |
| Operators keep typing `cc:` from old notes | reserved word ⇒ deterministic `peer_not_found` with the `pdx peers --all` hint |
| A label shadows a tmux name | documented tier order; `tmux:<name>` for the unshadowable form |
| Helper names go stale on remote hosts | stated as a limit (§3.5 Freshness); `pdx peers --all` is the source of truth |
| Label store grows with dead rows | lazy cleanup on claim; a time-based sweep is noted as future work |

## 7. Decisions recorded

1. **Label is Purdex-owned, keyed by Claude Code `sessionId`.** Not the
   registry name (Claude Code owns that file; only `/rename` and
   `--name` can change it, neither reachable by Purdex) and not tmux (absent
   for Desktop, ids float across restarts).
2. **Occupancy = registry-entry liveness, with an explicit "cannot tell".**
   No takeover of a live holder; no manual clean-up of a dead one; a
   resumed former holder comes back unnamed; an unreadable file for a live
   pid blocks reclaim (`not_ready`) instead of being ignored. Chosen over
   "reject until manually cleared" because the crash-and-rebuild path is
   the one the user hits most, and over "trust the inventory rows" because
   R1 showed rows can be absent for live sessions.
3. **Default labels live in a namespace users cannot claim** (`_` prefix).
   Keeps the occupancy table to user labels only and makes "unnamed"
   visible.
4. **Suffix is display, not identity, and not verified.** Typing it is
   harmless; the resolver strips it. A "did the name change hands" check
   would need a bound identity, which is what the wire tuple already is.
5. **`:` as separator; `cc` and `tmux` reserved.** Two-level convention
   (`/` host, `:` detail) that needs no shell escaping and reads
   unambiguously to an LLM; the reserved words keep the old and the
   explicit fallback syntaxes from ever parsing as labels.
6. **Label charset is ASCII lowercase only.** Reduces near-collisions by
   construction; English labels were the user's preference.
7. **Removed tiers: session code and `cc:`; tmux name kept as bare
   fallback plus explicit `tmux:`.** The code is the tmux id the user does
   not want in addresses; `cc:` is subsumed by the default label.
8. **Helper rename = in-place registry rewrite by the daemon**, gated by a
   monotonic `address_since`; no respawn (R1 #10: a respawn drops frames
   the helper has not yet forwarded), no new stdin command (P3 D8 stands).
9. **Freshness is one-directional** (R1 #12): only a `/deliver` from the
   renamed origin renames its helper on the receiving host.
10. **`pdx msg selftest` is untouched** (R1 #14); the label path gets a
    two-daemon e2e test instead.
11. **Not a mixed-version protocol** (R1 #15/#16): both hosts upgrade per
    PR; §3.9 documents skew symptoms only.
12. **Future hook — label hand-over.** `pdx msg name --release` already lets
    a departing session free its name deliberately. A later
    `pdx msg name <label> --inherit <sessionId>` (explicit, both parties
    alive) is the natural extension if the resume-loses-name rule turns out
    to bite in practice. Not in this spec.

## 8. Review disposition (R1 `task-mu1ags4q-o5eeev`)

| # | Sev | Finding | Disposition |
|---|---|---|---|
| 1 | Blocker | inventory absence ≠ holder dead (`partial`, skipped registry files) | accepted — §3.3 liveness at entry level; completeness rule; `not_ready` |
| 2 | Major | `CfgMu` is global and cannot freeze session lifecycle | accepted — dedicated `labelMu`; guarantee scoped to the observation (§3.3) |
| 3 | Major | row / entry / conversation conflated; multi-process conversation | accepted — §3.2 "one label per conversation"; §3.4 fallback-agent rows keep a label but stay non-deliverable |
| 4 | Major | default labels not in the occupancy table | accepted — `_` namespace (§3.1) |
| 5 | Major | labels shadow tmux names; partial ⇒ wrong tier | accepted — `tmux:` form; partial ⇒ `not_ready` (§3.2) |
| 6 | Major | `cc` is a valid label | accepted — reserved words (§3.1) |
| 7 | Major | suffix cannot prove hand-over | accepted — suffix display only, `SuffixMismatchError` removed (§3.1, §3.2, D4) |
| 8 | Minor | suffix grammar undefined | accepted — `san` (§3.1); receiver validates syntactically (§3.5) |
| 9 | Minor | base36 prefix is skewed | accepted — `mod 36⁶`, padding, golden vectors (§3.1) |
| 10 | Blocker | reap drops frames in flight | accepted — in-place file rewrite, no respawn (§3.5, D8) |
| 11 | Major | rename ordering / acquire-write race | accepted — `address_since` monotonic; lifecycle untouched (§3.5) |
| 12 | Major | "either direction refreshes" is false | accepted — §3.5 Freshness, D9 |
| 13 | Minor | startup sweep / `proxies.json` misuse | moot with #10; `proxies.json` unchanged (§3.5) |
| 14 | Major | selftest misread (D4) | accepted — selftest untouched; two-daemon e2e (§3.6, §4, D10) |
| 15 | Major | compatibility overgeneralised | accepted — §3.9 matrix by session kind and direction; not a supported state |
| 16 | Major | P4a not host-local, gates unclear | accepted — phases re-cut with CI gates; both hosts per PR (§4) |
| 17 | Major | acceptance step 6 premise wrong | accepted — §5 separates rename (4) from take-over (5); `--all`; added 6/7/8/10 |
| O1 | — | label store failure contract | added — `store_unavailable`; inventory read failure ⇒ `partial` (§3.3) |
| O2 | — | error formats, `handleSend` mapping | added — §3.6 error table |
| O3 | — | resolve→deliver name change | added — §3.2 Snapshot semantics |
| O4 | — | trust boundary of `name` | added — §2 non-goal, §3.6 |
| O5 | — | acceptance evidence and isolation | added — §5 preamble |
