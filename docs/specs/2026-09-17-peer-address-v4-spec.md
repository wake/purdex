# Peer Address v4 — a readable address backed by an exact one

Date: 2026-09-17 · Scope: daemon (`internal/peers`, `internal/module/peers`, `internal/store`,
`internal/config`) + `pdx` CLI + SPA status bar · Three phases, shipped together

Amends `2026-09-17-peer-address-v3-spec.md` (#1091, alpha.367). v3's routing model is kept; its
address *rendering* is replaced. §3 states exactly which v3 decisions survive and which do not.

Reviewed once cross-model before planning. That review found three blockers in the first draft —
a name/ref namespace collision, a combined-form mismatch that delivered anyway, and a false claim
that the two namespaces were disjoint by construction. All three are folded in; §11 records what
changed and why, so the corrections are not silently re-litigated later.

## 1. Goal

An address a person can read, and an address that always resolves, as one printed string:

```
mlab/purdex-b0 [q34psn]
air26/barbox-a6 [n4zeqk]
```

The shape is Claude Code's own — the name IS the address, and a bracketed ref disambiguates when
the name is not enough — with a host segment in front, because pdx spans machines and Claude Code
does not.

**The two parts back each other up; neither is claimed to be infallible alone.** A name can drift
or collide, and the ref covers it. A ref can (very rarely) collide, and the name covers it. §3 gives
the honest degradation table rather than a guarantee.

## 2. Evidence — why v3 ended up unreadable, and how far that reason actually reaches

v3 shipped `mini-lab/_q34psn4f:aigora2-purdex-b0`. The readable part is present but sits *after* the
hash and *after* a colon, and the form people pass around is the truncated `mini-lab/_q34psn4f`.
The brief this work started from was `mlab/purdex-7c`: a readable head plus a disambiguator.

The reason recorded for dropping the readable head was that no stable source existed — Claude Code's
own `name` is `derived` and was believed to churn under the session.

**Measured on `mini-lab`, 2026-09-17, across every live registry file in `~/.claude/sessions/`:**

| observation | result |
|---|---|
| sessions still carrying the name they started with | **14 / 14** |
| `nameSince` ≠ `startedAt` | 3 rows, all by **1 ms** — write ordering, not a rename |
| distinct names among the 14 | **14** |

`nameSource: derived` distinguishes "the system chose it" from "the user set it". It does not mean
the value churns, and `nameSince` exists precisely to record when the current one was adopted.

**What this sample does and does not establish.** It establishes that the name is stable enough to
be the *everyday* address: on a real host, under real use, nothing renamed itself. It does **not**
establish that the name can never change, and three paths are known not to be covered by it:

- a session that resumes, compacts, or runs a different Claude Code version was not observed across
  that transition;
- a user can rename a session;
- **this repository itself can rewrite the field** — `RewriteRegistryName`
  (`internal/peers/ccuds/registry_write.go:175`) writes an arbitrary string into a registry file,
  and nothing validates what `ReadRegistryDiag` reads back (`registry.go:383` assigns
  `Name: wire.Name` directly).

So the name is specified as a **convenience alias that is usually stable**, never as an identity.
Everything that must survive a rename routes on the ref. That is why §5.8 makes every copy action
put the ref on the clipboard, and why §5.4 refuses rather than guesses when a typed name and a
typed ref disagree.

Two facts still make the name a good everyday head:

- **Claude Code already disambiguates its own names** — `nexen-f2` / `nexen-ec`, `purdex-b0` /
  `purdex-53` / `purdex-03`: a project stem plus a 2-character tail.
- **The name already crosses hosts intact.** `normalizeRemoteRows` (`module.go:573`) rewrites only
  the segment before the first `/`.

## 3. What v3 keeps, what it does not, and what still degrades

| v3 | v4 | |
|---|---|---|
| D1 one address per conversation | **amended** | one *exact* address (the ref) and one *convenient* address (the name). Both reach the same conversation; neither is infallible alone. |
| D2 the id derives from `sessionId` only | **kept** | the ref is still `FNV-1a-64(sessionId)`, pure, stable across resume and daemon restart. |
| D3 `label` is never routed on | **kept** | strengthened: §6 makes it a free-text title. The routable name comes from the registry, not the title store. |
| D4 no tmux-derived default label | **kept** | the tmux name is an identity nowhere in v4. |
| D5 two conversations may share a display name | **kept** | for titles. A *name* collision is resolved by the ref, not prevented. |
| D7 address conflicts removed at the root | **amended** | removed at the root for the ref's *derivation*. Collisions are adjudicated by refusal, never by picking. |

### 3.1 The honest degradation table

v3's §2 recorded three failures. None returns in its original form, but saying "they do not return"
overclaims — each has a residual mode, and naming it is what keeps a later reader from assuming a
guarantee that was never made.

| v3 failure | v4 behaviour | residual |
|---|---|---|
| **P1** tmux rename leaves the address permanently stale | the tmux name is not in any address | the *name* can still drift (§2). A stale `<host>/<name>` then misses, or — if a third party has taken that name — becomes ambiguous. It never silently reaches the wrong conversation: §5.4 refuses on a name/ref mismatch, and the ref address is unaffected. |
| **P2** a third party's session evicts both conversations | nothing is evicted; both rows keep their ref | both lose the *bare-name* form until one exits, and are told so with both candidates and their refs. Readability degrades; reachability does not. |
| **P3** the hash fallback skips the collision rules | the ref is checked by the same tier machinery as everything else | two live conversations sharing a ref make the **ref address** ambiguous — independently of their names, because `<host>/_<ref>` never consults a name. They remain reachable by name. |

**Unreachable requires both layers to fail for the same pair**: a shared name *and* a shared ref.
At width 6 (§5.1) the ref half of that is ≈2.3e-6 for 100 live conversations on one host.

## 4. Decisions (user-confirmed, do not reopen)

| # | Decision |
|---|---|
| V1 | The address is `<host>/<name>`, displayed with its ref as `<host>/<name> [<ref>]`. |
| V2 | `<name>` is Claude Code's registry `name` — not the tmux name, not the cwd, not the title. |
| V3 | The ref is `_` + **6** base36 digits from `FNV-1a-64(sessionId)`. It never changes for a conversation. |
| V4 | Display prints the ref on **every** row. Typing it is required only when the name is not enough. |
| V5 | `label` is renamed `title`: free text, human-facing, never routed on. |
| V6 | A title already in use still warns (`title_in_use` + `live_titles`) and still succeeds. |
| V7 | A host publishes its own alias; a peer adopts it at pairing time unless it collides locally. |
| V8 | Rows with no live Claude Code entry keep the `<host>/tmux:<session>` form, unchanged. |
| V9 | Pairing UI, token rotation and return-path verification are **out of scope** — §10. |
| V10 | A registry name is routable only if it passes `RoutableName` (§5.2). An unroutable name is displayed but never becomes an address. |
| V11 | A/B/C ship in one release. The portability claim in §1 holds only once C lands — §7. |

### 4.1 Where v4 diverges from Claude Code's own model, and why

| Claude Code | v4 | reason |
|---|---|---|
| "A ref you did not just read from a listing or an error will not resolve" | the ref is **stable for the conversation's life** | a pdx address is written into a handoff message and used hours later, after a daemon restart or a resume. An ephemeral ref fails every one of those. |
| flat namespace, no machine qualifier | `<host>/` prefix | pdx spans hosts; two hosts may each hold a `purdex-b0`. |
| `to` is a JSON string, so `name [ref]` costs nothing | `<addr>` is a shell argument | one display form, three accepted input forms — §5.4. |

## 5. Phase A — the address

### 5.1 The ref (`internal/peers/label.go` → `ref.go`)

```go
// RefID derives a conversation's ref from its Claude Code sessionId:
// "_" + base36(FNV-1a-64(sessionId) mod 36^6), 6 digits, zero-padded.
func RefID(sessionID string) string

// IsRef reports whether s has the ref form, "_" + exactly 6 base36 digits.
func IsRef(s string) bool
```

`CanonicalID` → `RefID`, `IsCanonicalID` → `IsRef`: §5.2 renames the field they produce, and leaving
the functions called "canonical" is how the old reading survives a rename. In substance only
`canonicalN` (8 → 6), `canonicalSpace` (36⁸ → 36⁶) and the pattern (`^_[0-9a-z]{8}$` →
`^_[0-9a-z]{6}$`) change. The hash, the alphabet and the purity are untouched.

**Width 6 carries a different budget than v3's.** 36⁶ ≈ 2.18e9; for 100 live conversations on one
host the birthday probability is ≈2.3e-6. v3 needed 8 because the id was the *only* way to reach a
conversation. In v4 the name covers a ref collision exactly as the ref covers a name collision — see
§3.1, which states the residual plainly rather than claiming the ref "always works".

**Every existing address changes.** `_q34psn4f` and `_q34psn` are different strings. Accepted: peer
messaging is not yet in wide use, alpha carries no migration obligation, and §8.3 specifies what a
mixed-version window does — refuse, never guess.

### 5.2 Names, and which ones may be addresses

Claude Code's registry `name` is an unvalidated JSON string (`registry.go:383`), and this repo can
write an arbitrary one (`ccuds/registry_write.go:175`). A routable address therefore cannot simply
be `<host>/` + whatever that field holds — it would admit strings containing `/`, `:`, spaces and
brackets, all of which the address grammar, `SplitAddress` and the combined-form parser assign
meaning to, and it would admit ref-shaped strings that shadow the ref namespace.

```go
// RoutableName reports whether a registry name may be used as an address
// head: ^[a-z0-9][a-z0-9-]{1,63}$ and NOT ^[0-9a-z]{6}$.
func RoutableName(s string) bool
```

Two clauses, each closing one hole:

- **the pattern** excludes `/`, `:`, whitespace, `[`, `]` and a leading `_`, so a name address always
  parses and can never be mistaken for a ref;
- **the 6-digit exclusion** is what makes §5.4's tier 3 safe. Without it a conversation named
  `q34psn` would shadow the ref `_q34psn` for anyone who copied the bracket text, and nothing
  prevents such a name from existing.

The observed corpus passes: `purdex-b0`, `nexen-f2`, `ai-chat-story-3a`, `at-inwin-plugin-2e`.

**A row whose name fails `RoutableName` gets no name address.** Its `Address` is `<host>/_<ref>`, the
name still renders in the table (sanitized, as today), and `Reason` carries `name_unroutable` so the
state is visible rather than looking like a missing row.

### 5.3 `PeerRecord` (`internal/peers/record.go`)

```go
Ref     string `json:"ref"`     // "_q34psn"; "" when the row has no live cc agent
Address string `json:"address"` // "<host>/<name>", "<host>/_<ref>", or "<host>/tmux:<session>"
Title   string `json:"title"`   // was Label; free text, "" until set
```

`Canonical` is renamed `Ref` — it is no longer "the canonical address", it is the disambiguator.
`Suffix` is **deleted**: its two jobs were readability (now the name's) and provenance-by-`RowKind`
(which `RowKind` already carries).

```go
TmuxName string `json:"tmux_name"` // display-only; "" when unknown
```

**`TmuxName` is new, and it exists because deleting `Suffix` would otherwise lose real information.**
`SessionName` is set only on session rows (`record.go:156`); `EntryRecord` never sets it, because an
entry row has no session row behind it. Under v3 the tmux name still reached the screen through
`Suffix`, whose two provenances `RowKind` told apart. Drop `Suffix` without replacing that and the
`TMUX` column in §5.7 renders empty for exactly the rows whose session is hardest to find.

`TmuxName` carries `s.Name` on a session row and `e.TmuxSessionName()` on an entry row — the same
two provenances, in a field that says what it is. **It is display-only and routed on by nothing.**
`SessionName` keeps its current meaning untouched, so tier 4 and the `tmux:<name>` form are
unaffected: an entry row's frozen registry value must never become a way to reach anything, because
that value going stale is v3 §2's P1.

§5.7 marks an entry row's value with a trailing `?`, because a frozen name is a place that may no
longer exist and a reader deciding where to attach is entitled to know which of the two they are
looking at.

**No top-level `Name` field is added.** The registry name already crosses the wire as
`AgentInfo.PeerName` (`record.go:22`), in v3 and v4 alike; adding a second copy would create a field
that v3 daemons do not send and that could disagree with the one they do. `Resolve` reads
`r.Agent.PeerName`. This is also why §8.3's version refusal is load-bearing rather than belt-and-
braces: a v3 row *does* carry a usable name, so without the refusal a bare-name send would resolve
against rows whose refs the sender could never have verified.

**`Address` holds no ref and no brackets.** It is the everyday, typeable form; the display string
`<host>/<name> [<ref>]` is composed at render time from `Address` and `Ref`, never stored. That keeps
`SplitAddress`, `normalizeRemoteRows` and every URL-path assumption working on a space-free value.

### 5.4 `Resolve` (`internal/peers/address.go`)

Forms in order; the first with ≥1 match decides, and several matches in one tier is
`*AmbiguousError` that never falls through.

| order | form | matches |
|---|---|---|
| 0 | `cc:<x>` | retired — `ErrNotFound` wrapping `ErrLegacyCC`, as in v3 |
| 0 | `tmux:<name>` | explicit, bypasses everything — `SessionName == name` |
| 1 | `<name>` | `Agent.PeerName == head`, over rows with a live cc entry **and a routable name** |
| 2 | `_<ref>` | `Ref == head`, over rows with a live cc entry |
| 3 | `<ref>` without the underscore | as tier 2 — safe only because `RoutableName` forbids a 6-digit name (§5.2) |
| 4 | `<name>` | bare tmux session name, complete inventory only (v3's tier 2) |

**Tiers 1 and 2 are disjoint because `RoutableName` makes them so** — not because registry names
happen to look a certain way. The first draft asserted the latter; it is false, and §11 records it.
Tier 1 is restricted to routable names so that an unroutable one can never win a tier it should not
be in.

**The combined form `<name> [<ref>]` refuses on mismatch.** The bracket group is stripped, the ref
resolves, and the typed name is compared against the resolved row's. A mismatch is
`ErrNameMismatch`, naming the typed name, the resolved name and the ref. It does **not** deliver
with a warning:

> The name in a combined address is a human check digit, not decoration. An address of the form
> `trusted-name [attackerRef]` is the exact string an attacker would want pasted, and delivering it
> because "the ref is authoritative" spends the only check the reader had. Legitimate drift has an
> explicit escape hatch — `<host>/_<ref>` says "the ref, whatever it is called now" outright.

**The stale-version check sits above every tier, not before the fallback.** In v3 it guarded a tier-1
miss, because the only thing below it was a tmux-name guess. In v4 the tier immediately below it
would *succeed*: a v3 row carries a usable name in `agent.peer_name`, so a bare name would resolve
against a row whose ref the sender could never have verified. The gate therefore runs straight after
the explicit `cc:` / `tmux:` forms and before tier 1 — one stale row condemns the batch, because
`Resolve` is called per host and every row in it comes from the same daemon.

`tmux:<name>` stays above the gate. It names a place outright, a v3 daemon reports `SessionName`
exactly as a v4 one does, and it is the escape hatch the refusal points the caller at.

The remaining v3 conservatisms carry over verbatim: `snap.Partial` on a miss is
`ErrResolveNotReady`; `snap.RegistryIncomplete` on a single hit is `ErrResolveNotReady`. **Both apply
to the combined form too** — it resolves its ref through the same helper the bare-ref tiers use, so
it cannot quietly acquire a weaker rule set than the address it contains.

### 5.5 Input forms accepted by `pdx msg send`

```
pdx msg send mlab/purdex-b0 "..."               # everyday, no quoting
pdx msg send "mlab/purdex-b0 [q34psn]" "..."    # verbatim copy; the name is checked
pdx msg send mlab/_q34psn "..."                 # exact, survives a rename
```

### 5.6 Wire (`internal/peers/wire.go`)

`WireFrom.Address` becomes `_<ref>` — the exact form, so a reply routes by the stable id and a rename
between send and reply cannot break it. `WireFrom.PeerName` already carries the name a receiver
renders; no new field.

`ValidateWireAddress`'s accepted heads, as a matrix the tests mirror one row each:

| class | head | accepted | rationale |
|---|---|---|---|
| v4 | `^_[0-9a-z]{6}$` (`IsRef`) | yes | current |
| v3 | `^_[0-9a-z]{8}$` (`legacyV3Head`) | yes, **one release** | a peer mid-upgrade; `Resolve` still refuses it via §8.3, so acceptance here only changes *which* error the operator sees |
| v2 | its default head, **also `IsRef`** | yes | v2's default label was `_` plus six base36 digits — the identical shape a v4 ref has, so `isLegacyV2Head` is deleted and `IsRef` covers both. A coincidence of format, not of meaning, and harmless here because this function checks grammar only; §8.3 is what tells the versions apart, and it refuses a pre-v4 batch whole |
| v2 | a bare user label | yes | unchanged from v3, via `ValidateUserLabel` |
| v1 | `""` | yes | a v1 sender, unchanged |

Only the v3 row is time-limited, so there is **one** `// TODO(v5)` arm, not two; it names the
release and both retired forms so the deletion is not lost.

**`ValidSuffix` and `suffixWirePattern` are kept, not deleted with `Suffix`.** An earlier draft said
otherwise and was wrong in a way worth recording, because the two names look like one thing:
`Suffix` was the *producer*, a field v4 stops writing, while `ValidSuffix` is the *receiver's* check
on what a legacy sender still puts on the wire. Retiring a sender while relaxing the matching
receiver is how a field quietly stops being validated at all — so the suffix arm stays, and a
malformed legacy suffix is still `bad_address`.

### 5.7 `pdx peers` table (`cmd/pdx/peers.go`)

```
TITLE             ADDRESS                   AGENT  STATUS  DELIVERABLE  TMUX      CWD
                  mlab/purdex-b0 [q34psn]   cc     idle    yes          aigora2   ~/Workspace/wake/aigora
Purdex Tester 01  mlab/purdex-53 [d8dc4a]   cc     busy    yes          purdex7   ~
                  mlab/nexen-f2 [df25d0]    cc     idle    inbox_dead   nexen     ~
                  mlab/tmux:aigora3         -      -       no_agent     aigora3   ~
                  air26/barbox-a6 [n4zeqk]  cc     idle    yes          bb2       ~
```

`--all` keeps its leading `HOST` column. Two columns go, one arrives:

| | |
|---|---|
| `HOST` (single-host form) | dropped — it is the address's first segment |
| `NAME` | dropped — it **is** the address's second segment |
| `TMUX` | **added** — the tmux name left `Address` with `Suffix`; without this column it is no longer on screen at all. Renders `TmuxName` (§5.3), with a trailing `?` on an entry row to mark a frozen value that may name a session that has since been renamed or gone |

`AGENT` stays: it will carry non-`cc` types as cross-agent messaging lands. `DELIVERABLE` stays
separate from `STATUS` although `Deliverable == true` ⟺ `Reason == ""` (verified at
`record.go:241/259`) — folding them would force a reader to know which of `idle` and `inbox_dead`
means "cannot send", and a boolean-shaped column answers that without inference.

### 5.8 SPA

`usePeerStore` follows the renames (`canonical` → `ref`, `label` → `title`, `label_source` →
`title_source`, `labels_unavailable` → `titles_unavailable`) and drops `suffix`.

`StatusBar.tsx` declines to render when `row.label === ''` (`StatusBar.tsx:140`). That guard is wrong
twice under v4: a title is now usually empty and never identified a row anyway, while the name always
does. The peer segment becomes **display `<name> [<ref>]`, copy `<host>/<name> [<ref>]`**.

**The clipboard carries the ref even though the display could get away without it.** What a person
copies is what gets pasted into a handoff and used hours later, which is exactly the window in which
a name drifts or is taken. Copying the prettier `<host>/<name>` would put the weakest form in the
place with the longest shelf life. The title, when set, renders beside the name rather than instead
of it.

`peer.titles_unavailable_note` is re-keyed with the rename; #1094 tracks its wording drift and is not
resolved here.

## 6. Phase B — `label` becomes `title`

`label` is renamed because it no longer describes the field. A title is `Purdex Tester 01`: free
text, for a person. Keeping the name `label` would keep inviting the reading that it is a short tag
one can route on — the confusion v3 removed, and the one v4 must not reintroduce now that a
*different* string has become routable.

| | before | after |
|---|---|---|
| grammar | `^[a-z0-9][a-z0-9-]{1,31}$` | free text: ≤64 bytes, printable UTF-8, no control characters |
| reserved words | `cc`, `tmux` | **dropped** — a title reaches nothing, so nothing can be shadowed |
| collision compare | exact | normalized: casefold + collapse whitespace runs |
| warning | `label_in_use` + `live_labels` | `title_in_use` + `live_titles`, behaviour unchanged |
| store | `peer_labels` table | unchanged on disk; Go symbols renamed |

**The schema needs no change.** v3 already removed the `UNIQUE` constraint and the evicting
`DELETE` — `meta.go:117` records why, and `Claim` (`peer_label.go:63`) is a plain upsert. This phase
is a rename and a grammar loosening, not a data-model change.

**`title_in_use` is kept deliberately.** Routing does not care that two conversations share a title;
a human conversation does — "Purdex Tester has finished" is unusable if two exist. The serial
convention stays in CLAUDE.md, and `live_titles` keeps giving an agent what it needs to pick the next
free serial in one step.

### 6.1 `whoami`'s unset-title line

`renderSelfRecord` prints `label:       (, rev 0)` when nothing is set
(`cmd/pdx/msg.go:656`) — an empty value, an empty source and a revision that means nothing yet, in a
tuple that reads like a malfunction. Under v4 the unset case becomes the *common* case, because the
title no longer has a derived fallback and nothing routes on it.

Unset renders `title:      (none)`; set keeps `title:      <title> (user, rev N)`. A `ref:` line
joins the block, because an agent cannot compute its own ref — it is a hash of a `sessionId` the
agent never handles, so asking is the only way to learn it.

Renames: `ValidateUserLabel` → `ValidateTitle`, `LabelSourceUser` → `TitleSourceUser`,
`PeerRecord.Label/LabelSource/LabelRev` → `Title/TitleSource/TitleRev`, `LabelInfo` → `TitleInfo`,
`LabelStore` → `TitleStore`, `labels.go` → `titles.go`, `label.go` → `ref.go`. `pdx msg name` keeps
its verb. JSON keys follow, closing #1095's rename item.

## 7. Phase C — a host publishes its own alias

A host's name is chosen unilaterally by whoever ran `pdx peers host add`, stored only in that
machine's config, and stamped onto every remote row by `normalizeRemoteRows`, whose comment says so
outright: *"how WE have the peer configured, never the remote's own self-reported value"*. The remote
is never asked. `verifyHost` already fetches its envelope and learns `host_id` (`hosts.go:199`), but
the envelope carries no alias.

Consequence: **an address is not portable, and readability without portability is worth little.**
`mlab/purdex-b0` pasted into a handoff means nothing on a machine that calls this host `mini-lab`.

```go
type Envelope struct {
    HostID string `json:"host_id"`
    Alias  string `json:"alias"` // NEW: what this host calls itself (config PeerAlias())
    ...
}
```

1. `verifyHost` learns `env.Alias` beside `env.HostID`, validating it with `ValidateAlias`.
2. `POST /api/peers/hosts` with no `alias` adopts the learned one; an explicit `alias` still wins.
3. A learned alias colliding with an existing local alias (or this host's own) is **not**
   auto-suffixed — `mlab-2/...` is unportable in a new way. The add returns 409 naming both, and the
   operator supplies one explicitly.
4. `HostResult` gains `SelfAlias`, and **`pdx peers --all`** marks a host whose self-reported name
   differs from the local one. Drift is surfaced, never followed: the local alias stays authoritative
   for routing.

   **Not `pdx peers host list`.** That route (`GET /api/peers/hosts`) renders `cliHostRow` straight
   out of local config and never contacts anyone, so it has no self-reported value to show and could
   only display a stale one learned at pairing time. The fan-out already fetches each host's envelope
   on every call, which makes `--all` the one place the comparison is live rather than remembered.

**C is not optional for the goal.** §1's premise is an address that can be handed to someone else.
A and B alone produce a readable address that is still only locally meaningful, so V11 ships the
three together and §9.4 does not accept the release until the cross-host paste works.

## 8. Testing

### 8.1 Unit

- `RefID`: width 6, zero-padded, base36 alphabet, determinism, and a pinned vector so a refactor
  cannot silently change every address.
- `RoutableName`: accepts the observed corpus; rejects `/`, `:`, space, `[`, `]`, a leading `_`,
  empty, 64-byte boundary, and every 6-digit base36 string shape.
- `Resolve`: one case per row of §5.4, plus — a 6-digit-shaped name rejected by `RoutableName` and
  therefore *not* shadowing tier 3; an unroutable name never winning tier 1; the combined form
  matching, and mismatching into `ErrNameMismatch`; ambiguity at tier 1 returning both candidates
  with their refs; ambiguity at tier 2 (two rows, one ref) still reachable by name; every v3
  conservatism still firing.
- `ValidateTitle`: 64-byte boundary, control characters, multi-byte UTF-8 across the boundary, the
  dropped reserved words now accepted.
- Title collision normalization: `Purdex Tester` vs `purdex  tester`.
- `ValidateWireAddress`: one case per row of §5.6's matrix.
- Phase C: `verifyHost` learning an alias; the 409 collision path; `SelfAlias` drift rendering.

### 8.2 Table rendering

Golden tests for both `pdx peers` forms: a titled row, an untitled row, an agentless `tmux:` row, an
unroutable-name row, a non-deliverable row per `Reason`, and a remote row — pinning that `TMUX` is
populated and that the ref renders without its underscore.

### 8.3 Mixed-version behaviour

The two daemons are deployed together, but not atomically.

**The signal is precise, and must stay precise.** §5.3 renames the JSON key, so a v3 daemon's
`"canonical"` is not read and every v3 row decodes with `Ref == ""`. That alone is not the test: a
legitimate **v4** row also has an empty `Ref` when it is an owner-fallback row (`inbox_dead` /
`ambiguous`, `Agent.PID == 0`), a proxy row, or `agent: null`. The check is v3's `hasLiveEntry`
conjunction and nothing looser:

```go
Agent != nil && Agent.Type == "cc" && Agent.PID != 0 && Ref == ""
```

`hasPreV3Rows` is renamed `hasStaleVersionRows`; the logic is unchanged and the comment is updated to
name the new cause.

**Why the refusal is load-bearing here and was belt-and-braces in v3.** A v3 row carries a usable
name in `agent.peer_name` (§5.3), so v4's tier 1 would *succeed* against it — delivering to a row
whose ref the sender could never have verified. Refusing is what keeps that from being a silent
wrong delivery.

Tests drive a v3-shaped batch (`canonical` populated, `ref` absent) through `Resolve` and assert the
refusal for a bare name, a ref and the combined form, while `tmux:<name>` still resolves; plus one
case each for an owner-fallback row, a proxy row and an `agent: null` row proving a v4 batch is not
misjudged as stale.

## 9. Real-machine acceptance (must be run, not assumed)

1. `pdx msg whoami` on mlab prints `address`, `ref`, and a legible `title` line when unset.
2. `pdx peers --all` renders §5.7's table on both hosts.
3. `pdx msg send mlab/<name>`, `pdx msg send "mlab/<name> [<ref>]"` and `pdx msg send mlab/_<ref>`
   all deliver.
4. `pdx msg send "mlab/<wrong-name> [<ref>]"` is **refused**, naming both names and the ref.
5. Cross-host by bare name: air26 → mlab and mlab → air26.
6. Rename a tmux session; all three forms above still deliver.
7. Two conversations sharing a name: the bare name is refused with both candidates and their refs;
   each ref delivers to the right one.
8. Set a title with a space and mixed case; set the same title on a second conversation; the warning
   fires and both keep their titles.
9. Phase C: re-pair air26 with no explicit alias, confirm the adopted name; change `[peers] alias` on
   one side and confirm the drift is shown, not followed. **Then paste an address produced on one
   host into the other and confirm it resolves** — this is §1's premise and the release does not ship
   without it.

## 10. Out of scope

**Pairing UI, token rotation, return-path verification** — a separate spec, because it depends on
Phase C's alias model. Recorded so the boundary is deliberate:

- a Hosts → Pair page listing the daemons the App holds an admin token for, which is exactly the set
  it can pair *both* directions of; rows known only through a daemon's peer list must be marked
  half-pairable, or the UI manufactures the silent one-way pairings it exists to prevent;
- return-path verification, which does not exist (`verifyHost` proves only the outbound direction, so
  a half-configured pair reports success);
- `inbound_token_prev` and a rotation flow, because rotating a single token has no safe ordering —
  whichever side is written first, the other is locked out until the second write lands.

Also out of scope: config hot-reload (no watcher exists; a hand-edited `config.toml` is overwritten
by the next API-driven write), and #1092, #1093, #1094, #1096.

## 11. Corrections folded in from review

Recorded because each was a claim the first draft made confidently and wrongly; a later reader
re-deriving them from scratch would land in the same place.

| first draft | why it was wrong | now |
|---|---|---|
| "tiers 1 and 2 are disjoint by grammar — a registry name cannot begin with `_`" | nothing validates the registry `name`; `registry.go:383` assigns it raw and `ccuds/registry_write.go:175` can write any string | disjointness is **created** by `RoutableName` (§5.2), not assumed |
| a bare 6-digit ref may be shadowed by a same-shaped name, "which is correct" | it is inducible: anything that can write a registry name can steal the ref namespace | `RoutableName` forbids 6-digit names, so tier 3 cannot be shadowed |
| a combined form whose name does not match "delivers and warns" | `trusted-name [attackerRef]` is precisely the string an attacker wants pasted; the warning arrives after delivery | mismatch is refused; `_<ref>` is the explicit override (§5.4) |
| "a ref collision is reached only after a name collision" | `<host>/_<ref>` never consults a name; two rows sharing a ref are ambiguous on their own | §3.1 states it as an independent residual |
| a top-level `Name` field on `PeerRecord` | v3 daemons do not send one, so it would decode empty and mis-model the mixed-version risk | `Resolve` reads `Agent.PeerName`, which both versions send (§5.3) |
| "every v3 row decodes with `Ref == ''`" | true but too loose — legitimate v4 owner-fallback, proxy and `agent: null` rows do too | the signal is the `hasLiveEntry` conjunction (§8.3) |
| §2's 14-file sample supports the name as an address head | it supports "stable in practice", not "cannot change"; resume, compact, user rename and this repo's own rewriter are uncovered | §2 scopes the claim; the name is a convenience alias and every copy action carries the ref (§5.8) |
| Phase C as a follow-on | A+B alone give a readable address that is not portable, which is not the goal | V11 ships the three together; §9.9 gates the release on a cross-host paste |
| "`ValidSuffix` … deleted with `Suffix`" (§5.6) | `Suffix` is the producer v4 stops writing; `ValidSuffix` is the receiver's check on what a legacy sender still sends. Deleting the check with the field would leave the value unvalidated | the suffix arm stays; a malformed legacy suffix is still `bad_address` |
| `isLegacyV2Head` as a distinct class (§5.6) | v2's default head and a v4 ref are the same six-digit shape, so it was a second name for one regex | deleted; `IsRef` covers both |
