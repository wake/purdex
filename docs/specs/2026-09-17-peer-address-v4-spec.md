# Peer Address v4 — a readable address that is still an exact one

Date: 2026-09-17 · Scope: daemon (`internal/peers`, `internal/module/peers`, `internal/store`,
`internal/config`) + `pdx` CLI + SPA status bar · Three phases

Amends `2026-09-17-peer-address-v3-spec.md` (#1091, alpha.367). v3's routing model is kept; its
address *rendering* is replaced. §3 states exactly which v3 decisions survive and which do not.

## 1. Goal

An address a person can read, and an address that always works, as the same string:

```
mlab/purdex-b0 [q34psn]
air26/barbox-a6 [n4zeqk]
```

The shape is Claude Code's own — the name IS the address, and a bracketed ref disambiguates only
when the name is not enough — with a host segment in front, because pdx spans machines and Claude
Code does not.

## 2. Evidence — why v3 ended up unreadable, and why that reason does not hold

v3 shipped `mini-lab/_q34psn4f:aigora2-purdex-b0`. The readable part is present but sits *after* the
hash and *after* a colon, and the form people actually pass around is the truncated
`mini-lab/_q34psn4f`. The brief this line of work started from was `mlab/purdex-7c`: a readable
prefix plus a disambiguator.

The reason recorded for dropping the readable head was that no stable source existed for it —
Claude Code's own `name` is `derived` and was believed to change under the session.

**Measured on `mini-lab`, 2026-09-17, across every live registry file in `~/.claude/sessions/`:**

| observation | result |
|---|---|
| sessions still carrying the name they started with | **14 / 14** |
| `nameSince` ≠ `startedAt` | 3 rows, all by **1 ms** — write ordering, not a rename |
| distinct names among the 14 | **14** |

`nameSource: derived` distinguishes "the system chose it" from "the user set it". It does not mean
the value churns. `nameSince` exists to record when the current name was adopted, and on this host
it still equals `startedAt` for every session.

Two further facts make the name a better address head than it looks:

- **Claude Code already disambiguates its own names.** `nexen-f2` / `nexen-ec`, `purdex-b0` /
  `purdex-53` / `purdex-03` — a project stem plus a 2-character tail. Same-name collisions are the
  exception, not the rule.
- **The name already travels between hosts intact.** `normalizeRemoteRows` (`module.go:573`)
  rewrites only the segment before the first `/`; everything after it is passed through from the
  remote. Nothing new is needed to carry a remote row's name.

## 3. What v3 keeps, and what it does not

| v3 | v4 | |
|---|---|---|
| D1 one address per conversation | **amended** | one *exact* address (the ref) plus one *convenient* address (the name). Both reach the same conversation; only the ref is guaranteed not to change. |
| D2 the id derives from `sessionId` only | **kept** | the ref is still `FNV-1a-64(sessionId)`, still pure, still stable across resume and daemon restart. |
| D3 `label` is never routed on | **kept** | and strengthened — §6 turns `label` into a free-text title, which nothing could sensibly route on. The routable name comes from Claude Code's registry, not from the label store. |
| D4 no tmux-derived default label | **kept** | the name head is the registry `name`. The tmux session name is not an identity anywhere in v4. |
| D5 two conversations may share a display name | **kept** | for titles. For *names*, a collision is resolved by the ref, not prevented. |
| D7 address conflicts removed at the root | **amended** | removed at the root *for the ref*. A name collision is adjudicated — visibly, by refusing with both candidates and their refs. |

**The failure v3 was built to prevent does not return.** §2 of the v3 spec records it: two
conversations collide on a derived label and *both* lose their address, because the label was the
only head and it came from a mutable, third-party-claimable tmux name. In v4 the colliding name is
not the only head — `_<ref>` resolves unconditionally and is derived from an identity nobody issues.
A name collision costs six characters of typing, not reachability.

## 4. Decisions (user-confirmed, do not reopen)

| # | Decision |
|---|---|
| V1 | The address is `<host>/<name>`, displayed with its ref as `<host>/<name> [<ref>]`. |
| V2 | `<name>` is Claude Code's registry `name`. It is not the tmux name, not the cwd, not the title. |
| V3 | The ref is `_` + **6** base36 digits from `FNV-1a-64(sessionId)`. It never changes for a conversation. Width 6, not 8 — it is now a tiebreaker, not the sole carrier of the collision budget. |
| V4 | Display prints the ref on **every** row. Typing it is required only when the bare name is ambiguous. |
| V5 | `label` is renamed `title`: free text, human-facing, never routed on. |
| V6 | A title already in use still warns (`title_in_use` + `live_titles`) and still succeeds. The serial convention stays. |
| V7 | A host publishes its own alias; a peer adopts it at pairing time unless it collides locally. |
| V8 | Rows with no live Claude Code entry keep the `<host>/tmux:<session>` form, unchanged. |
| V9 | Pairing UI, token rotation and return-path verification are **out of scope** — see §9. |

### 4.1 Where v4 diverges from Claude Code's own model, and why

| Claude Code | v4 | reason |
|---|---|---|
| "A ref you did not just read from a listing or an error will not resolve" — refs are ephemeral | the ref is **stable for the conversation's life** | a pdx address is written into a handoff message and used hours later, after a daemon restart or a resume. An ephemeral ref fails every one of those. The cost is that a stale ref still reaches the conversation it named; that is the property, not a leak. |
| flat namespace, no machine qualifier | `<host>/` prefix | pdx spans hosts. Two hosts may each hold a `purdex-b0`. |
| `to` is a JSON string, so `name [ref]` costs nothing | `<addr>` is a shell argument | one display form, three accepted input forms — §5.3. |

## 5. Phase A — the address

### 5.1 The ref (`internal/peers/label.go`)

```go
// RefID derives a conversation's ref from its Claude Code sessionId:
// "_" + base36(FNV-1a-64(sessionId) mod 36^6), 6 digits, zero-padded.
func RefID(sessionID string) string

// IsRef reports whether s has the ref form.
func IsRef(s string) bool
```

`CanonicalID` → `RefID` and `IsCanonicalID` → `IsRef`, because §5.2 renames the field they produce:
leaving the function called "canonical" while the field is `Ref` is how the old reading survives a
rename. Only `canonicalN` (8 → 6), `canonicalSpace` (36⁸ → 36⁶) and the pattern
(`^_[0-9a-z]{8}$` → `^_[0-9a-z]{6}$`) change in substance. The hash, the alphabet and the purity are
untouched.

**Width 6 is chosen against a different budget than v3's.** 36⁶ ≈ 2.18e9; for 100 live
conversations on one host the birthday probability is ≈2.3e-6. v3 needed 8 because the id was the
*only* way to reach a conversation, so a collision meant total loss. In v4 a ref collision is
reached only after a name collision has already occurred between the same two conversations, and
its outcome is the same refusal-with-candidates as any other ambiguity — never a misroute.

**Every existing address changes.** `_q34psn4f` becomes `_q34psn`-shaped and is a different string.
This is accepted: peer messaging is not yet in wide use, the alpha stage carries no migration
obligation, and §8.3 states what a mixed-version window does (refuse, never guess).

### 5.2 `PeerRecord` (`internal/peers/record.go`)

```go
Ref     string `json:"ref"`     // "_q34psn"; "" when the row has no live cc agent
Name    string `json:"name"`    // the registry name; "" when the row has no live cc agent
Address string `json:"address"` // "<host>/<name>", or "<host>/tmux:<session>" for an agentless row
Title   string `json:"title"`   // was Label; free text, "" until set
```

`Canonical` is renamed `Ref` — the field is no longer "the canonical address", it is the
disambiguator, and keeping the old name would keep the old reading. `Suffix` is **deleted**: its two
jobs were readability (now the name's) and provenance-by-`RowKind` (which `RowKind` already carries).
`SessionName` continues to hold the tmux session name for consumers that need it, and Phase A adds
the `TMUX` column so it stays visible on screen.

**`Address` holds no ref and no brackets.** It is the everyday, typeable form. The display string
`<host>/<name> [<ref>]` is composed at the point of rendering (CLI table, SPA) from `Address` and
`Ref`, never stored. This keeps `SplitAddress`, `normalizeRemoteRows` and every URL-path assumption
working on a space-free value.

### 5.3 `Resolve` (`internal/peers/address.go`)

Forms, in order. The first that matches decides; several matches in one tier is `*AmbiguousError`
and never falls through.

| order | form | matches |
|---|---|---|
| 0 | `cc:<x>` | retired — `ErrNotFound` wrapping `ErrLegacyCC`, as in v3 |
| 0 | `tmux:<name>` | explicit, bypasses everything — `SessionName == name` |
| 1 | `<name>` | `Name == head` over rows with a live cc entry |
| 2 | `_<ref>` | `Ref == head` over rows with a live cc entry |
| 3 | `<ref>` (no underscore) | same as tier 2, tolerated — see below |
| 4 | `<name>` | bare tmux session name, complete inventory only (v3's tier 2) |

**Tiers 1 and 2 are disjoint by grammar.** A registry name cannot begin with `_` (it is rendered
from a project stem), and a ref always does. So the `_` form is unambiguous and order between them
is a formality — stated explicitly so a later reader does not "fix" it.

**Tier 3 exists because the display drops the underscore.** The table prints `[q34psn]`, so an
operator copying from brackets types `q34psn`. That falls through tier 1 (no such name) into tier 3.
A conversation literally named `q34psn` would shadow it at tier 1; that is correct — the operator
typed a name-shaped string and a name exists — and `_q34psn` remains available to say otherwise.

**The combined form `<name> [<ref>]`.** Parsed by stripping the bracket group: the ref decides, and
the name is checked against the resolved row. A mismatch **delivers and warns** (`name_drifted`,
carrying both names). It does not refuse: the ref names the conversation exactly, and refusing
because the conversation renamed itself would destroy the stability the ref exists to provide.

The v3 conservatisms are carried over verbatim: `snap.Partial` on a tier miss is
`ErrResolveNotReady`; `snap.RegistryIncomplete` on a single hit is `ErrResolveNotReady`;
`hasPreV3Rows` refuses ahead of any fallback.

### 5.4 Input forms accepted by `pdx msg send`

```
pdx msg send mlab/purdex-b0 "..."               # everyday, no quoting
pdx msg send "mlab/purdex-b0 [q34psn]" "..."    # verbatim copy from a table or an error
pdx msg send mlab/_q34psn "..."                 # exact, always works
```

### 5.5 Wire (`internal/peers/wire.go`)

`WireFrom.Address` becomes `_<ref>` — the exact form, so a reply is routed by the stable id and a
rename between send and reply cannot break it. `WireFrom.PeerName` already carries the registry name
and is what a receiver renders; no new field is needed.

`ValidateWireAddress` simplifies to: `""` (a v1 sender), or a head matching `^_[0-9a-z]{6}$`, or —
for one release — `isLegacyV2Head` and the v3 8-digit pattern, so a not-yet-upgraded peer is
diagnosed rather than silently refused. `ValidSuffix` and the suffix arm are deleted with `Suffix`.

### 5.6 `pdx peers` table (`cmd/pdx/peers.go`)

```
TITLE             ADDRESS                   AGENT  STATUS  DELIVERABLE  TMUX      CWD
                  mlab/purdex-b0 [q34psn]   cc     idle    yes          aigora2   ~/Workspace/wake/aigora
Purdex Tester 01  mlab/purdex-53 [d8dc4a]   cc     busy    yes          purdex7   ~
                  mlab/nexen-f2 [df25d0]    cc     idle    inbox_dead   nexen     ~
                  mlab/tmux:aigora3         -      -       no_agent     aigora3   ~
                  air26/barbox-a6 [n4zeqk]  cc     idle    yes          bb2       ~
```

`--all` keeps its leading `HOST` column. Two columns are dropped and one is added:

| | |
|---|---|
| `HOST` (single-host form) | dropped — it is the address's first segment |
| `NAME` | dropped — it **is** the address's second segment |
| `TMUX` | **added** — the tmux session name left `Address` with `Suffix`; without this column it is no longer on screen at all |

`AGENT` stays: it will carry non-`cc` agent types as cross-agent messaging lands.
`DELIVERABLE` stays separate from `STATUS` although `Deliverable == true` ⟺ `Reason == ""`
(verified at `record.go:241/259`): folding them would force a reader to know which of `idle` and
`inbox_dead` means "cannot send". A boolean-shaped column answers that without inference.

`--json` is unchanged in shape beyond the renamed/added fields, and stays complete.

### 5.7 SPA

`usePeerStore` follows the field renames (`canonical` → `ref`, `label` → `title`,
`label_source` → `title_source`, `labels_unavailable` → `titles_unavailable`) and drops `suffix`.

`StatusBar.tsx` currently displays the label and copies the address, and declines to show anything
when `row.label === ''` (`StatusBar.tsx:140`). Under v4 that guard is wrong twice over: a title is
now usually empty and is no longer what identifies a row, while the name always identifies it. The
peer segment becomes **display `<name> [<ref>]`, copy `<host>/<name>`** — the same split the CLI
table uses, for the same reason. The title, when set, renders beside it rather than in place of it.

`peer.labels_unavailable_note` is re-keyed with the rename; #1094 tracks its wording drift and is
not resolved here.

## 6. Phase B — `label` becomes `title`

`label` is renamed because it no longer describes the field. A title is `Purdex Tester 01`: free
text, for a person to read. Keeping the name `label` would keep inviting the reading that it is a
short tag one can route on — which is exactly the confusion v3 removed and v4 must not reintroduce
now that a *different* string (the name) has become routable.

| | before | after |
|---|---|---|
| grammar | `^[a-z0-9][a-z0-9-]{1,31}$` | free text: ≤64 bytes, printable UTF-8, no control characters |
| reserved words | `cc`, `tmux` | **dropped** — a title reaches nothing, so nothing can be shadowed |
| collision compare | exact | normalized: casefold + collapse runs of whitespace |
| warning | `label_in_use` + `live_labels` | `title_in_use` + `live_titles`, unchanged in behaviour |
| store | `peer_labels` table | unchanged on disk; Go symbols renamed |

**The schema needs no change.** v3 already removed the `UNIQUE` constraint and the evicting
`DELETE` — `meta.go:117` records why, and `Claim` (`peer_label.go:63`) is a plain upsert. This phase
is a rename plus a grammar loosening, not a data-model change.

**The `title_in_use` warning is kept deliberately.** Routing does not care that two conversations
share a title; a human conversation does. "Purdex Tester has finished" is unusable if two of them
exist. The serial convention (`Purdex Tester 02`) stays in CLAUDE.md, and `live_titles` keeps giving
an agent everything it needs to pick the next free serial in one step.

Renames: `ValidateUserLabel` → `ValidateTitle`, `LabelSourceUser` → `TitleSourceUser`,
`PeerRecord.Label/LabelSource/LabelRev` → `Title/TitleSource/TitleRev`, `LabelInfo` → `TitleInfo`,
`LabelStore` → `TitleStore`, `labels.go` → `titles.go`, `label.go` → `ref.go` (it now holds the ref
derivation and the title grammar). `pdx msg name` keeps its verb — it still names the conversation.
JSON keys follow (`label` → `title`, `label_source` → `title_source`, `label_rev` → `title_rev`,
`labels_unavailable` → `titles_unavailable`), which closes #1095's rename item.

## 7. Phase C — a host publishes its own alias

Today a host's name is chosen unilaterally by whoever ran `pdx peers host add`, stored only in that
machine's config, and stamped onto every remote row by `normalizeRemoteRows` — whose comment says so
outright: *"how WE have the peer configured, never the remote's own self-reported value"*. The
remote is never asked. `verifyHost` already fetches the remote's envelope and learns its `host_id`
(`hosts.go:199`), but the envelope carries no alias.

Consequence: **an address is not portable.** `mlab/purdex-b0` pasted into a handoff is meaningless on
a machine that calls this host `mini-lab`. Portability is the whole reason for making it readable.

```go
type Envelope struct {
    HostID string `json:"host_id"`
    Alias  string `json:"alias"` // NEW: what this host calls itself (config PeerAlias())
    ...
}
```

1. `verifyHost` learns `env.Alias` alongside `env.HostID`, validating it with `ValidateAlias`.
2. `POST /api/peers/hosts` with no `alias` adopts the learned one. An explicit `alias` still wins.
3. A learned alias that collides with an existing local alias (or with this host's own) is **not**
   auto-suffixed — auto-suffixing produces `mlab-2/...`, which is unportable in a new way. The add
   returns 409 naming both, and the operator supplies a local alias explicitly.
4. Because a peer may rename itself later, `HostResult` gains `SelfAlias` and `pdx peers host list`
   shows `ALIAS` beside `SELF_ALIAS`, marking a row where they differ. Drift is surfaced, never
   silently followed: the local alias remains authoritative for routing.

`PeerAlias()` already returns `Peers.Alias` or the `host_id` stem, so a host can set its own name
today (`[peers] alias = "mlab"`); Phase C is what makes that setting mean something off-machine.

## 8. Testing

### 8.1 Unit

- `CanonicalID`: width 6, zero-padded, base36 alphabet, determinism, and a pinned vector so a
  refactor cannot silently change every address.
- `Resolve`: one case per row of §5.3's table, plus — disjointness of tiers 1/2; tier 3 shadowed by
  a same-shaped name; the combined form with a matching and a drifted name; ambiguity at tier 1
  returning both candidates with their refs; every v3 conservatism still firing.
- `ValidateTitle`: 64-byte boundary, control characters, multi-byte UTF-8 at the boundary, the
  dropped reserved words now accepted.
- Title collision: normalization cases (`Purdex Tester` vs `purdex  tester`).
- `ValidateWireAddress`: the new pattern, `""`, the v3 8-digit legacy arm.
- Phase C: `verifyHost` learning an alias; the 409 collision path; `SelfAlias` drift rendering.

### 8.2 Table rendering

Golden tests for both `pdx peers` forms covering: a titled row, an untitled row, an agentless
`tmux:` row, a non-deliverable row with each `Reason`, and a remote row — pinning that `TMUX` is
populated and that the ref renders without its underscore.

### 8.3 Mixed-version behaviour

The two daemons are deployed together, but not atomically. During the window a v4 host reads a v3
host's rows, and the detection is free because §5.2 renames the JSON key: a v3 daemon emits
`"canonical"`, a v4 decoder reads `"ref"`, so **every v3 row decodes with `Ref == ""`**. That is
exactly the signal `hasPreV3Rows` already keys on — "a row with a live cc entry and no id can only
come from a daemon that does not know the field" — so the existing function is renamed
`hasStaleVersionRows` and its comment updated to name the new cause; the logic is unchanged.

A tier miss over such a batch returns `ErrRemoteTooOld`: refuse and name the version, never fall
through to a name or tmux guess. This matters more in v4 than it did in v3, because v4 adds a tier
that would otherwise *succeed* — a v3 row still carries the registry name, so a bare-name send could
land on a row whose ref the sender could not have verified. The refusal is what keeps that from
being a silent wrong delivery.

A test drives a v3-shaped batch (`canonical` populated, `ref` absent) through `Resolve` and asserts
the refusal for a bare name, for a ref, and for the combined form; `tmux:<name>` still resolves, as
it does in v3.

### 8.4 Real-machine acceptance (must be run, not assumed)

1. `pdx msg whoami` on mlab prints `address`, `ref` and an empty-but-legible `title` line.
2. `pdx peers --all` renders the §5.6 table on both hosts.
3. `pdx msg send mlab/<name> "..."` delivers; `pdx msg send "mlab/<name> [<ref>]"` delivers;
   `pdx msg send mlab/_<ref>` delivers.
4. Cross-host: air26 → mlab and mlab → air26, by bare name.
5. Rename a tmux session; every address above still delivers.
6. Two conversations given the same registry-name shape: the bare name is refused with both
   candidates and their refs; each ref delivers to the right one.
7. Set a title with a space and mixed case; set the same title on a second conversation; the
   warning fires and both keep their titles.
8. Phase C: re-pair air26 with no explicit alias and confirm the adopted name; then change
   `[peers] alias` on one side and confirm the drift is shown, not followed.

## 9. Out of scope

**Pairing UI, token rotation, return-path verification** — a separate spec. It depends on Phase C's
alias model, so it follows rather than accompanies this work. Recorded here so the boundary is
deliberate:

- a Hosts → Pair page listing the daemons the App holds an admin token for, which is exactly the set
  it can pair *both* directions of; rows known only through a daemon's peer list must be marked as
  half-pairable, or the UI will manufacture the silent one-way pairings it exists to prevent;
- return-path verification, which does not exist today (`verifyHost` proves only the outbound
  direction, so a half-configured pair reports success);
- `inbound_token_prev` and a rotation flow, because rotating a single token has no safe ordering —
  whichever side is written first, the other is locked out until the second write lands.

Also out of scope: config hot-reload (there is no watcher; a hand-edited `config.toml` is overwritten
by the next API-driven write), and the existing follow-ups #1092, #1093, #1094, #1096.

## 10. `pdx msg whoami`'s empty title line

Unrelated to the model, fixed here because this spec touches every line of that block:
`renderSelfRecord` prints `label:       (, rev 0)` when no label is set (`msg.go:656`). It becomes
`title:      (none)` when unset, and keeps `<title> (user, rev N)` when set.
