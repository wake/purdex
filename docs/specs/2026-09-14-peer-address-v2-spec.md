# Spec — Peer address v2: session labels ("Peer Address v2")

Status: draft v1
Date: 2026-09-14
Branch: `worktree-peer-address-v2`
Amends: `2026-09-13-peer-bridge-spec.md` v3.2 (§4.1, §4.2, §4.4, §4.5, §4.7)

> This spec replaces the `<session>` half of the human address. Wire identity,
> authentication, delivery, helpers and audit are unchanged unless a section
> below says otherwise. Section numbers with a "PB" prefix refer to the Peer
> Bridge spec.

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
3. A name is held only while its holder is alive. When the holder leaves
   the live peer list the name is free; nobody is ever displaced.
4. The address still carries enough machine-derived context to tell two
   sessions apart by eye, without that context being part of what the user
   has to type.
5. A session can ask what its own address is (`pdx msg whoami`).
6. tmux session names keep working as a fallback address for operators.

### Non-goals

- Renaming through the Purdex UI, or showing labels in the tab strip.
  Labels are an addressing concern; the UI keeps its own tab names.
- An MCP tool for renaming. The MCP control plane does not exist yet; the
  CLI is the only entry point in this spec.
- Changing Claude Code's own registry `name`. Purdex never writes to a
  registry file it did not create.
- Label hand-over between sessions (an old session passing its name to a
  successor while both are alive). §7 records the hook for it.
- Making `pdx` reachable from a Desktop session on air-2026 (the binary is
  at `~/.config/pdx/bin/pdx`, not on `PATH`). That is a deployment
  prerequisite tracked with Local Daemon Install, not part of this spec.

## 3. Design

### 3.1 Address grammar

```
<host>/<label>[:<suffix>]
```

| Segment | Who sets it | Used for |
|---|---|---|
| `<host>` | unchanged (PB §4.1: local alias, configured alias, full `host_id`) | routing to a daemon |
| `<label>` | the session, via `pdx msg name`; default derived from the Claude Code `sessionId` | **the address** — the only part a caller has to type |
| `<suffix>` | the owning daemon, read-only | identification by eye; optional verification when typed |

`:` separates label from suffix. `SplitAddress` splits on the first `/` as
today, so the `:` inside a full `host_id` on the left never collides with
the one on the right.

**Label rule.** `^[a-z0-9][a-z0-9-]{1,31}$` — lowercase ASCII letters,
digits and hyphen, 2–32 characters, must not start with a hyphen. Anything
else is refused at claim time with `label_invalid`. There is no
case-folding: a label that would need folding is simply invalid, so two
labels that look alike never differ only in case.

**Suffix.** `<tmux session name>-<cc registry name>` for a session inside
tmux, `<cc registry name>` outside tmux. tmux forbids `:` and `.` in session
names and the registry name is sanitised by Claude Code, so the suffix
cannot contain the separator. The suffix is computed by the owning host on
every inventory read and is never stored.

**Default label.** When a session has no claimed label its label is
`d(sessionId)`: the first 6 characters of the base36 encoding of the FNV-1a
64-bit hash of the Claude Code `sessionId`. It is deterministic — the same
conversation has the same default before and after a resume — and always
satisfies the label rule. Records carry `label_source: "default" | "user"`
so listings can show which sessions are still unnamed.

Examples:

```
air/purdex-tester:purdex-3f          Desktop session on air, named
air/k3x9qz:purdex-1b                 Desktop session on air, not yet named
mini-lab/purdex-dev:mt0-purdex-49    tmux session mt0 on mlab, named
mini-lab/mt4                         fallback: tmux session name, no label involved
```

### 3.2 Resolution (replaces PB §4.1 `<session>` tiers)

`peers.Resolve(records, session)` keeps its shape (one host's records, first
tier with ≥1 match decides, several matches ⇒ `AmbiguousError`, none ⇒
`ErrNotFound`) with new tiers:

| Tier | Matches when | Notes |
|---|---|---|
| 1 label | `session` (with any `:<suffix>` removed) equals the record's `label` | proxy rows excluded. If a suffix was typed and the matched record's suffix differs, the result is `SuffixMismatchError{Expected, Actual}` — not a fall-through. This is how a caller holding an old full address learns the name has changed hands |
| 2 tmux name | `session` equals the record's `session_name` | reached only when tier 1 has no match at all. Suffix syntax is not accepted here |

Removed: the session-code tier and the `cc:` tier. `cc:<name>` now fails
with `ErrNotFound` (the error text points to `pdx peers --all` so an agent
following old instructions recovers in one step).

A default label is a label: tier 1 matches it. Two live sessions on one
host with the same default label (a 36⁶ collision) resolve as `ambiguous`,
exactly as two same-named tmux sessions do today; naming either one ends it.

### 3.3 Label store and occupancy

Table `peer_labels` in `meta.db`, host-local, never replicated:

```sql
CREATE TABLE IF NOT EXISTS peer_labels (
    session_id TEXT PRIMARY KEY,   -- Claude Code sessionId
    label      TEXT NOT NULL UNIQUE,
    set_at     INTEGER NOT NULL    -- unix ms
);
```

One label per session, one session per label, by schema.

**Occupancy is a property of the live inventory, not of the table.** A
label `L` is *held* iff a row `(S, L)` exists **and** `S` is the
`agent.session_id` of a live, non-proxy inventory row (PB §4.2 liveness). A
row whose session is not live is inert: it neither resolves nor blocks.

**Claim** `(S', L)`:

| Situation | Result |
|---|---|
| `L` invalid | `400 label_invalid` |
| `S'` not a live non-proxy row | `400 origin_unknown` (same as `/send`) |
| `L` held by live `S ≠ S'` | `409 label_taken` — body carries the holder's record and `live_labels` (every label currently held on this host) so the caller can pick a free one without a second round-trip |
| `(S', L)` already the row | `200`, no write |
| otherwise | in one transaction: delete any row with `label = L` (a dead holder), delete any row with `session_id = S'` (the caller's previous label), insert `(S', L, now)`. `200` with the updated record |

Consequence for resume: session `S` held `purdex-tester`, crashed; `S'`
claimed `purdex-tester`; `S` is resumed. `S`'s row is gone, so `S` comes
back as `d(S)` — the unnamed default — and `S'` keeps the name. Nobody is
displaced while alive; the resumed session is told its address by
`pdx msg whoami` and can pick another.

**Release** `S'`: delete the row for `S'` if any; `200`. The session's
address reverts to its default label immediately.

The claim/release path holds the peers module's config mutex across the
liveness check and the write, so two concurrent claims of one label on one
host cannot both pass the "held?" test.

**Garbage.** Rows for sessions that never come back accumulate at the rate
sessions are named, which is small, and are removed lazily by the next
claim of the same label. No sweeper in this spec; if it ever matters, a
row older than N days with no live session is the obvious rule.

### 3.4 Peer record (amends PB §4.2)

New fields, all always present:

```jsonc
{
  "address": "mini-lab/purdex-dev:mt0-purdex-49",   // <host>/<label>:<suffix>
  "label": "purdex-dev",
  "label_source": "user",            // user | default | "" (no cc agent ⇒ no label)
  "suffix": "mt0-purdex-49",         // "" when no cc agent
  "session_code": "02ybs5",          // kept for the UI/session API; no longer an address
  "session_name": "mt0",             // tier-2 fallback address
  ...
}
```

A row with no Claude Code agent (`agent: null`, or codex/opencode) has no
`sessionId` to derive from: `label`, `label_source` and `suffix` are `""`
and `address` is `<host>/<session_name>` as today. Proxy rows keep their
helper's registry name as `address` and are excluded from tier 1.

`BuildInput` gains `Labels map[string]string` (sessionId ⇒ label) read from
`peer_labels` once per inventory call. `Build` stays a pure join.

### 3.5 Wire and helper naming (amends PB §4.4, §4.5)

`from` in `/api/peers/deliver` gains `address` — the origin's full
`<label>:<suffix>` as the sending daemon computed it at send time. The
wrapper's `from-name` becomes `"<A alias>/<label>:<suffix>"`. The receiver
does not validate `address` beyond the label rule and the suffix charset;
it is display data attributed to an authenticated host.

**Helper name follows the address.** A helper (PB §4.5) is still keyed by
the origin tuple; its registry `name` is the origin's address at spawn.
When a later `/deliver` or reply for the same key arrives with a different
`address`, the daemon reaps the helper (SIGTERM, wait, sweep its files as in
the startup sweep) and spawns a fresh one before writing the frame. The old
socket disappears; a reply already in flight to it fails on the sender's
side with the harness's own "peer gone" error and the sender retries via
`ListAgents`, which now shows the new name. Renames are rare and the helper
is cheap; this avoids a second daemon→helper command on the stdin channel
(P3 D8).

A rename with no subsequent traffic leaves the old name on remote hosts
until the next message in either direction. `pdx peers --all` is always
current because it reads the owning host's inventory.

### 3.6 CLI

```
pdx msg name <label>        claim <label> for the calling session
pdx msg name --release      drop the calling session's label
pdx msg whoami              print the calling session's address
pdx msg send <host>/<label>[:<suffix>] <text>     (grammar in usage text updated)
```

`name` and `whoami` identify the caller exactly as `send` does (PB §4.4
Origin): `CLAUDE_CODE_MESSAGING_SOCKET` ⇒ `origin_inbox` ⇒ live inventory
row; unset or unknown ⇒ `origin_unknown`. Both talk to the **local** daemon
only, with the admin token, through:

| Method | Path | Body | Returns |
|---|---|---|---|
| `POST` | `/api/peers/self` | `{origin_inbox}` | the caller's `PeerRecord` |
| `PUT` | `/api/peers/self/label` | `{origin_inbox, label}` | the updated record, or `label_taken` with `{holder, live_labels}` |
| `DELETE` | `/api/peers/self/label` | `{origin_inbox}` | the updated record |

Both routes are admin-only (PB §4.6 row for `/send` applies verbatim);
`/self*` is never reachable with a peer `inbound_token`.

`pdx msg whoami` output (text mode):

```
address:  air/purdex-tester:purdex-3f
label:    purdex-tester (user)
host:     air (air:9k2m4q)
session:  fa5d4c07-… pid 76973
```

`--json` prints the record. `pdx peers` / `pdx peers --all` show the new
`address` column and mark `label_source: default` rows with `*` so an
operator sees at a glance which sessions are unnamed.

`pdx msg selftest` targets its throwaway tmux session by the **default
label** of the Claude Code session it spawns (read back from the inventory),
not by tmux name, so the self-test exercises tier 1.

### 3.7 Documentation contract for agents

`CLAUDE.md` (project) gains a short "Peer addresses" section — the grammar,
the label rule, the naming convention `<project>-<role>[-<n>]`
(`purdex-tester`, `purdex-tester-2`), and the two commands. The wording an
operator uses to bootstrap a session is then one sentence: *"You are the
tester: run `pdx msg name purdex-tester`."* The session confirms with
`pdx msg whoami`.

### 3.8 Configuration

None. Label rule, default-label length and the `:` separator are constants.

### 3.9 Compatibility (amends PB §4.8)

Alpha: no data migration. Mixed versions across hosts:

| Sender | Target | Behaviour |
|---|---|---|
| v2 | v1 | target's records have no `label`; tier 1 never matches; tier 2 (tmux name) works; `cc:` no longer works from a v2 sender |
| v1 | v2 | v1 resolves by its own tiers over v2 records (v2 keeps `session_code`/`session_name`, so tmux name and code still resolve); `from.address` absent ⇒ v2 names the helper `<alias>/<session_name or cc:peer_name>` as v1 did |

Both directions deliver; only the human-facing name degrades. The
`/api/peers` envelope is unchanged, so PB §4.8's version warning still
covers the rest.

## 4. Phases

Two PRs, each independently reviewable and shippable.

### P4a — Labels, resolution, CLI (host-local)

- `peer_labels` table + store functions (claim / release / snapshot).
- `PeerRecord` fields, `BuildInput.Labels`, default-label derivation.
- `peers.Resolve` v2 tiers + `SuffixMismatchError`.
- `/api/peers/self`, `/self/label` routes; `pdx msg name|whoami`;
  `pdx peers` address column and `*` marker.
- Tests: label rule table; default-label determinism; claim matrix from
  §3.3 (all five rows, plus the resume-after-crash sequence); Resolve tier
  table (label, label+suffix ok, suffix mismatch, tmux fallback, `cc:`
  gone, default-label ambiguity); `whoami`/`name` origin handling; two
  concurrent claims of one label.

After P4a, `pdx msg send air/purdex-tester` already works because the
sender resolves over the target's records — only `from-name` and helper
names are still the old form.

### P4b — Wire, helpers, self-test, docs

- `from.address` on `/deliver`; `from-name` in the wrapper.
- Helper reap-and-respawn on address change; `proxies.json` records the
  address used.
- `pdx msg selftest` targets by default label.
- `CLAUDE.md` section (§3.7); Peer Bridge spec §4.1/§4.2/§4.4/§4.5 get a
  one-line pointer to this spec.
- Tests: e2e wrapper `from-name`; helper respawn on rename (same key, new
  name, old socket gone); v1→v2 `/deliver` without `address`.

## 5. Acceptance (manual, after P4b is deployed)

Everything below is run once on mlab against two isolated daemons (the P3
harness under `/tmp/pdx-p3/` is the template) and once for real between
mlab and air-2026.

1. **Name and see.** In a tmux Claude Code session on mlab:
   `pdx msg name purdex-dev` → `pdx msg whoami` shows
   `mini-lab/purdex-dev:mtN-purdex-xx`; `pdx peers` on air shows the same
   row; `pdx peers --all` on mlab marks other sessions with `*`.
2. **Desktop tester.** Start a Claude Desktop session on air, send it one
   message so it registers, then tell it: "run `pdx msg name
   purdex-tester`". From mlab: `pdx msg send air/purdex-tester "ping"` is
   delivered; the reply arrives with `from-name="air/purdex-tester:purdex-xx"`;
   `ListAgents` on mlab lists `air/purdex-tester:purdex-xx`.
3. **Taken.** A second Desktop session on air runs
   `pdx msg name purdex-tester` → `label_taken`, output lists
   `purdex-tester` as held and shows the holder's suffix.
4. **Crash and come back.** Quit the first Desktop session. The second one
   repeats the claim → success. Resume the first (`claude --resume`,
   same `sessionId`): `pdx msg whoami` shows its default label.
5. **Stale full address.** From mlab, send to the first session's *old*
   full address `air/purdex-tester:<old suffix>` → `suffix_mismatch`, the
   error names the current holder.
6. **Helper rename.** After step 4, send from mlab to `air/purdex-tester`
   again; `ListAgents` on mlab shows exactly one `air/purdex-tester:…`
   entry with the new suffix, and `proxies.json` has no record for the old
   name.
7. **Fallback.** `pdx msg send mini-lab/mt0 "x"` from air still delivers
   (tier 2). `pdx msg send mini-lab/cc:purdex-49 "x"` fails with the
   `pdx peers --all` hint.
8. **Restart.** `pdx stop` / `pdx start` on air; the Desktop session's
   label is still `purdex-tester` (table survived; occupancy re-derived
   from the live registry).
9. `pdx msg selftest` passes on both hosts.

## 6. Risks

| Risk | Mitigation |
|---|---|
| Default label collision on one host (36⁶ space) | resolves as `ambiguous`, same as duplicate tmux names; naming one session ends it |
| Two sessions claim one label at the same moment | claim runs under the peers config mutex on the owning host; the second sees `label_taken` |
| A resumed session silently loses its name | by design (§3.3); `whoami` reports it; the operator's bootstrap sentence re-claims it |
| Helper respawn races a reply in flight | the reply fails loudly on the harness side and `ListAgents` shows the new name; no silent misdelivery because the old socket no longer exists |
| Operators keep typing `cc:` from old notes | explicit `ErrNotFound` message with the `pdx peers --all` hint |
| Label store grows with dead rows | lazy cleanup on claim; a time-based sweep is noted as future work |

## 7. Decisions recorded

1. **Label is Purdex-owned, keyed by Claude Code `sessionId`.** Not the
   registry name (Claude Code owns that file; only `/rename` and
   `--name` can change it, neither reachable by Purdex) and not tmux (absent
   for Desktop, ids float across restarts).
2. **Occupancy = presence in the live inventory.** No takeover of a live
   holder; no manual clean-up of a dead one; a resumed former holder comes
   back unnamed. Chosen over "reject until manually cleared" because the
   crash-and-rebuild path is the one the user hits most.
3. **Suffix is display, not identity.** Typing it is optional and only
   verifies; the daemon computes it fresh each read and never stores it.
4. **`:` as separator.** Two-level convention (`/` host, `:` detail) that
   needs no shell escaping and reads unambiguously to an LLM.
5. **Label charset is ASCII lowercase only.** Reduces near-collisions by
   construction; English labels were the user's preference.
6. **Removed tiers: session code and `cc:`.** The code is the tmux id the
   user does not want in addresses; `cc:` is subsumed by the default label.
   tmux *name* stays as the lowest tier for operators.
7. **Helper rename = reap and respawn**, not a new stdin command, keeping
   P3 D8 (daemon→helper is the first-line config only).
8. **Future hook — label hand-over.** `pdx msg name --release` already lets
   a departing session free its name deliberately. A later
   `pdx msg name <label> --inherit <sessionId>` (explicit, both parties
   alive) is the natural extension if the resume-loses-name rule turns out
   to bite in practice. Not in this spec.
