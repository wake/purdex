# Peer Address v3 — one address, plus a label that is only a label

Date: 2026-09-17 · Scope: daemon (`internal/peers`, `internal/module/peers`) + `pdx` CLI + one SPA line · Single phase

Supersedes the default-label half of `2026-09-16-peer-default-label-tmux-spec.md` (#1079, alpha.363).

Reviewed twice: one cross-model pass, then three parallel passes (attack / spec-drift / structure).
Findings are folded in; §3.2 records what the attack pass changed about the claim itself.

## 1. Goal

A conversation gets **exactly one address**, derived from its `sessionId`, immutable for its life.

The `label` column stays, but it stops being a second way to address anything. It becomes what its
name always suggested: a short name a conversation calls itself, for a human or another agent to
read when deciding who to talk to. Reading it is how you *choose* a peer; the address is how you
*reach* one.

That split is the whole of the collision story: **the only thing that must be unique is the only
thing nobody gets to choose.**

## 2. Evidence — what is broken today

### P1. A renamed tmux session leaves the address permanently stale

`Entry.Tmux` comes from the `tmux` field of Claude Code's own registry file
(`~/.claude/sessions/<pid>.json`, `registry.go:35`). Claude Code captures it at startup;
`TmuxSessionName()` only slices that frozen string (`registry.go:63`) and nothing refreshes it.

Measured 2026-09-16 on `mini-lab`, renaming this session's own tmux session:

| step | observation |
|---|---|
| before | registry `tmux = aigora2:@5.%5`, `pdx msg whoami` → `mini-lab/aigora2` |
| `tmux rename-session aigora2 aigora2zz` | live tmux name is now `aigora2zz` |
| after the registry file was **rewritten** (mtime advanced, `status` went `busy` → `shell`) | registry `tmux` **still** `aigora2:@5.%5` |

The file is rewritten on status changes and the `tmux` field is copied through unchanged, so the
staleness is not transient: it lasts until the agent exits.

### P2. A third party's new session evicts both conversations

A new tmux session reusing that stale name, plus a live agent, makes two conversations report the
same candidate. `ResolveDefaultLabels` rule 2 then skips both (`label.go:123`):

```go
if occupantsByLabel[candidate].otherThan(sid) { continue }
```

Covered by the existing test `"two unnamed conversations in one tmux session" → DefaultLabels{}`.
The incumbent did nothing; its address silently changes to the hash form. Nobody is told.

### P3. The hash fallback skips the collision rules

`DefaultLabels.For()` returns `DefaultLabel(sessionID)` unconditionally when no candidate survived
(`label.go:87`) — it never consults occupancy.

The failure is **safe but total**: `resolveTier` returns `*AmbiguousError` on multiple matches
(`address.go:137`) and `send.go:310` answers 409 `ambiguous` without picking a candidate. Delivery
is refused, never misrouted. Both conversations lose their address until one exits.

## 3. Decisions (user-confirmed, do not reopen)

| # | Decision |
|---|---|
| D1 | **One address per conversation**, the canonical id. No second naming scheme, no aliasing layer. |
| D2 | The canonical id is derived from `sessionId` **only**. No tmux name, no Claude Code `name`, no cwd. |
| D3 | `label` is a **self-declared display name**. Never resolved, never routed on, no uniqueness guarantee. |
| D4 | The tmux-derived default label is **removed**. `label` is empty until a conversation sets one. |
| D5 | Two conversations may hold the same `label`. Setting one already in use **warns and succeeds**. The serial-number convention (`purdex-tester-2`) stays, enforced by CLAUDE.md rather than by the daemon — §8. |
| D6 | No extra `note` / `role` field. `label` is that field, and calling it `label` is what keeps it written as a name rather than a sentence. |
| D7 | Address conflicts are removed at the root rather than adjudicated. Label conflicts are visible, harmless, and left alone. |

### 3.1 Why this removes the conflict problem rather than managing it

Every failure in §2 has the same shape: the address depended on a string someone else could change
or claim. D1 + D2 remove that dependency — an address is a function of an identity nobody issues,
requests, or competes for. There is no allocation, so there is nothing to allocate twice.

`label` can collide precisely because nothing depends on it being unique. Two `purdex-tester` rows
are two rows a reader can choose between, not a routing decision anyone has to make.

### 3.2 The trust boundary this rests on (added after the attack review)

An earlier draft said address conflicts were "structurally impossible". That overclaims, and the
correction matters more than the wording: the guarantee is **conditional on `sessionId` being
unique and unforgeable**, and the daemon does not verify that. It reads `sessionId` out of the
registry file and trusts it (`registry.go:306`, `registry.go:380`); `Build` aggregates entries by
`SessionID` with no reuse guard (`record.go:95`).

So the accurate claim is:

> Two conversations cannot end up sharing an address **unless something writes a registry file
> carrying another conversation's `sessionId`** — which requires write access to
> `~/.claude/sessions/`, i.e. the same UID as the agent itself.

That is the same trust boundary the whole peers module already sits on: an attacker with the user's
UID can bind the inbox socket, forge `proc_start`, or simply read the conversation. v3 does not
widen it, and it is not this spec's job to close it. What v3 does remove is the case that needed no
attacker at all — a user renaming a tmux session.

Two sub-cases are worth stating because they behave differently:

| | outcome |
|---|---|
| forged twin is **live** alongside the real one | two rows share a canonical → `*AmbiguousError` → 409, refused, not misrouted |
| real conversation is **dead**, twin appears after | the twin resolves at that address — indistinguishable from a legitimate resume, which is the same `sessionId` by design |

The second is not a defect to fix here: an address that follows `sessionId` across a resume is the
entire point of D2.

## 4. Data model

### 4.1 Canonical id (`internal/peers/label.go`)

```go
// CanonicalID derives a conversation's address from its Claude Code sessionId:
// "_" + base36(FNV-1a-64(sessionId) mod 36^8), 8 digits, zero-padded. Pure,
// deterministic across resumes and daemon restarts.
func CanonicalID(sessionID string) string
```

- Replaces `DefaultLabel`, deleted with the rest of §4.3.
- **Width 8, not 6.** 36⁸ ≈ 2.82e12. For 100 live conversations the birthday probability is
  ≈1.8e-9, against ≈2.3e-6 at width 6.
- The leading `_` keeps it disjoint from anything a human types: a `label` must match
  `^[a-z0-9][a-z0-9-]{1,31}$` and can never begin with `_`.
- `IsCanonicalID(s)` replaces `IsDefaultLabel`, pattern `^_[0-9a-z]{8}$`.

**Collision handling is the ambiguity refusal that already exists**, kept as a backstop rather than
a mechanism. A stateful allocator is rejected: it would make the id impure, and purity is the
property the design rests on.

**A safe failure has to say so.** `pdx msg send` must name every candidate an `*AmbiguousError`
carries, not print a bare "ambiguous" — otherwise the operator reads "my address stopped working"
and hunts a bug that is not there. This requires a wire change; see §6.

### 4.2 `label` and the claim path (`internal/module/peers/labels.go`)

The store and the route stay; the contract loosens:

| before | after |
|---|---|
| claiming a taken label → 409 `label_taken`, refused | **succeeds**, response carries a `label_in_use` warning with the other holders and `live_labels` |
| a label was the address head | display-only |
| `BlockingUnknown()` gate: unreadable registry files ⇒ 503, "a label cannot be proven free" | **removed** — nothing needs proving free any more (see below) |

**The `BlockingUnknown` gate goes with the guarantee it protected.** `claim` currently refuses when
the registry has files it cannot decode, on the grounds that a label "cannot be proven free"
(`labels.go:159`). Under D5 a label never needs to be free. Keeping the gate would leave a 503 on a
path that no longer has anything to lose by proceeding. Claiming becomes: validate grammar → find
origin → snapshot the label store → compute the warning → write.

Grammar is unchanged (`ValidateUserLabel`), so `<project>-<role>[-<n>]` keeps working — and is
still **asked for** (§8). What changed is who enforces it. The warning must be actionable in one
step: it carries `live_labels`, which is everything an agent needs to pick the next free serial
without a second lookup.

### 4.3 Deletions

| symbol | file | other dependents that must be updated in the same PR |
|---|---|---|
| `DefaultLabels`, `DefaultLabels.For` | `label.go` | `record.go` (7 `applyLabel` call sites), `module.go`, `labels.go:204` |
| `ResolveDefaultLabels` | `label.go` | same |
| `candidatesOf`, `occupantsOf`, `userLabelHoldersOf`, `sidSet`, `sidSet.otherThan` | `label.go` | none (unexported, single-use) |
| `qualifiesAsDefaultLabel` | `label.go` | none |
| `DefaultLabel`, `defaultLabelPattern`, `defaultLabelN`, `labelSpace` | `label.go` | replaced by §4.1 |
| `IsDefaultLabel` | `label.go` | **`ValidateWireAddress` (`wire.go:420`)** — see §6; this deletion is not local |
| `LabelSourceDefault` | `label.go` | **`applyLabel` (`record.go:261`)**, **`cmd/pdx/peers.go:514`** (the `*` marker), and SPA (§8.1) |

> This removes most of #1079. The occupancy reasoning it introduced is not being discarded because
> it was wrong — it is being discarded because D2 removes the mutable input that made it necessary.

**What is NOT being deleted.** The label store and its claim route survive (§4.2). What changes
there is the verdict on a duplicate, not the existence of the path. Alias uniqueness was never
implemented in `label.go` to begin with — it lives in `labels.go`, and a reader who takes "the
occupancy rules are gone" to mean "labels are gone" will delete too much.

### 4.4 `PeerRecord` (`internal/peers/record.go`)

```go
Canonical   string `json:"canonical"`    // NEW: "_3k9f2mq4"; "" when the row has no live cc agent
Label       string `json:"label"`        // CHANGED: self-declared display name; "" until one is set
LabelSource string `json:"label_source"` // CHANGED: "user" | ""   ("default" is gone)
LabelRev    int64  `json:"label_rev"`    // meaning narrowed, see below
Address     string `json:"address"`      // CHANGED head, unchanged FORMAT: <host>/<canonical>:<suffix>
```

**`Address` keeps its current format.** `applyLabel` builds `alias + "/" + <head> + ":" + Suffix`
(`record.go:265`) — that `alias` parameter is the **host** alias and stays so. Only the head
changes. Dropping the suffix would be breaking and is not proposed.

**`applyLabel` is not the only writer of `Address`**, and an earlier draft wrongly implied it was.
The complete set, all of which must stay consistent:

| writer | form | v3 |
|---|---|---|
| `applyLabel` (`record.go:265`) | `<host>/<head>:<suffix>` | head becomes the canonical id |
| `buildSessionRecord` (`record.go:151`), `agent: null` rows | `<host>/tmux:<session_name>` | unchanged |
| `EntryRecord` proxy branch (`record.go:316`) | `<host>/cc:<name>` — the retired form, deliberately unresolvable | unchanged |
| remote normalization (`module.go:582`) | rewrites the **host** half of a remote row's address to the local alias, keeping the session half | unchanged; works on whatever head the remote produced |

**`LabelRev` keeps meaning "how many times this conversation has set its label"** — it is a label
revision and stays one. What it stops implying is an *address* change, because the address no longer
moves. Two consequences, and an earlier draft got this wrong by saying "always 0":

- `applyLabel` keeps passing `info.Rev` through (`record.go:263`); the release path keeps writing a
  bumped rev (`labels.go:261`). No change.
- `WireFrom.AddressRev` / the helper-rename logic in `deliver.go:277` is the part that becomes
  inert: a v3 address never changes, so a v3 sender always reports rev 0 there and a helper is never
  renamed for a v3 origin. The field stays on the wire because v2 senders still populate it.

### 4.5 Field invariants (the consumer contract)

For every row whose agent is a **live** cc entry (`agent.type == "cc"` and `agent.pid != 0` — the
`hasLiveEntry` test the resolver uses):

| invariant | |
|---|---|
| `address == host + "/" + canonical + ":" + suffix` | |
| `canonical != ""`, immutable for the conversation's life | |
| `label` may be `""`, may change, **may be shared with another row** | never a key, never routed on |
| `label_source == (label != "" ? "user" : "")` | |

Three other row kinds keep today's behaviour and are **not** covered above — see §4.4's writer
table for their formats: `agent: null` session rows, proxy rows, and owner-fallback rows
(`inbox_dead` / `ambiguous`, `agent.pid == 0`). The last of these renders an address tier 1 will not
resolve; that is a **pre-existing wart, not introduced here**, and `reason` already says why.

> **Breaking change for consumers:** `label_source` no longer emits `"default"`, and `label` is now
> empty on a conversation that has not set one. Anything that displayed `label` as an identifier
> must display `canonical` or `address` instead.

## 5. Resolution

### 5.1 `Resolve` tiers (`internal/peers/address.go`)

Tier 1 stops matching labels and matches the canonical id:

```go
// Tier 1: the canonical id, over every row backed by a live entry.
rec, err := resolveTier(records, session, func(r PeerRecord) bool {
    return hasLiveEntry(r) && r.Canonical == head
})
```

- `cc:<x>` — still `ErrNotFound` wrapping `ErrLegacyCC`.
- `tmux:<name>` and tier 2 (a bare string as a tmux session name) — **kept, unchanged**. They
  address a *place*: they match `PeerRecord.SessionName`, which comes from the live tmux inventory
  and was never stale. Nobody issues or claims them, so D1 does not reach them. A bare label like
  `purdex-tester` now falls through tier 1, fails tier 2, and returns `ErrNotFound` — correct and
  legible.
  *Known limit, pre-existing, out of scope:* these tiers match `SessionName` without comparing
  `TmuxInstance`, so with two tmux servers holding same-named sessions they resolve the one the
  daemon can see, which may not be the one the operator meant.
- **Accepted conservatism, stated so it is not mistaken for an oversight:** a tier-1 miss under
  `snap.Partial` returns `ErrResolveNotReady` (`address.go:115`). A canonical id depends on neither
  the label store nor owner resolution, so a partial inventory can never be why it missed — yet it
  is still retried. That is the safe direction (a retry costs a round trip; a false "not found"
  costs a message). Splitting `Partial` by cause is out of scope.

### 5.2 Ordering constraint — these two changes cannot be split

Relaxing the duplicate-label claim (§4.2) **before** tier 1 stops matching labels (§5.1) opens a
window in which two conversations legitimately hold `purdex-tester` and every send to that label
resolves ambiguously. The two must land in the same commit, or the resolver change must go first.
This is a task-ordering requirement for the plan, not a preference.

### 5.3 Building the fields (`record.go`, `applyLabel`)

```
canonical = CanonicalID(sessionID)
label     = labelStore[sessionID].Label        // "" when none set

head      = canonical                          // always
address   = host + "/" + head + ":" + suffix
source    = label != "" ? "user" : ""
```

`applyLabel`'s `defaultLabel` parameter becomes `canonical`; the **7** call sites in `record.go`
(205, 211, 216, 229, 234, 239, 321) and the ones in `module.go` / `labels.go:204` drop the
`defaults.For(...)` argument. `applyLabel` becomes the single place that sets
`Canonical` / `Address` / `LabelSource`, which is what makes §4.5 checkable in one function.

### 5.4 Suffix must come from live tmux data

`Suffix` is display-only, but part of it comes from `Entry.TmuxSessionName()` — the same frozen
field as P1. Most call sites are already correct; only three are not:

| `record.go` | `tmuxName` argument | verdict |
|---|---|---|
| 205, 211, 229, 239 | `s.Name` — live `SessionSummary` | already correct, leave |
| **216, 234** | `candidates[0].TmuxSessionName()` / `paneMatches[0].TmuxSessionName()` — frozen | **fix**: `s.Name` is in scope and is the live name for the very session being rendered |
| **321** | `e.TmuxSessionName()` — frozen, `entry` rows | keep: an entry row with no session row behind it has no live name to use |

A two-line fix, not a rework.

**This leaves `suffix` with two provenances in one response**, and that must be stated rather than
left for the next reader to discover: live on a `session` row, possibly frozen on an `entry` row.
No field is added to tell them apart — `row_kind` already does, and `suffix` is display-only and
explicitly not an identity. But the doc comment on `PeerRecord.Suffix` must say exactly this,
because "one field, two meanings, no discriminator" is the shape of the `partial` defect found in
#1079's review.

## 6. Wire / API

### 6.1 `GET /api/peers`

Rows gain `canonical`; `label` / `label_source` change meaning per §4.4.

`Envelope.LabelsUnavailable` keeps its name but **narrows in meaning** and its comment must say so:
a label-store failure no longer affects whether an address is correct, only whether a display label
can be shown. Under v2 it could change a row's address (a default label is resolved over label
rows); under v3 it cannot.

### 6.2 `ValidateWireAddress` (`wire.go:415`) — implementation blocker

It gates `from.address`'s head through `IsDefaultLabel` (exactly 6 digits) or `ValidateUserLabel`.
An 8-digit canonical passes neither, so a v3 sender's own `DeliverRequest.Validate()` would reject
it at `send.go:353` before anything left the host.

The head must accept **exactly 6 or exactly 8** digits, not a 6–8 range — a 7-digit head is neither
a v2 nor a v3 id and accepting it would be accepting nothing real:

```go
// canonicalWireHead accepts a v3 id (8) and, for as long as a v2 peer may
// still be sending, a v2 one (6). Never 7: that is not an id either version
// ever minted.
var canonicalWireHead = regexp.MustCompile(`^_([0-9a-z]{6}|[0-9a-z]{8})$`)
```

`ValidateUserLabel` stays as the other accepted head — a v2 sender may still present a user label.

### 6.3 The self routes need an envelope

`PUT /api/peers/self/label` must return 200 **with a warning**, and today there is nowhere to put
one: `writeSelfResult` encodes the bare `PeerRecord` (`labels.go:320`) and the CLI unmarshals
straight into one (`msg.go:686`). `holder` / `live_labels` currently live on `APIError`
(`wire.go:290`), reachable only on a non-200.

All three self routes move to one envelope, because `doSelfRequest` is shared by `name` and
`whoami` — changing all three is less churn than special-casing one:

```json
{ "peer": { …PeerRecord… },
  "warning": { "code": "label_in_use", "detail": "…",
               "holders": [ …PeerRecord… ], "live_labels": ["…"] } }
```

`warning` is absent on a clean result. `ErrLabelTaken` leaves the error vocabulary.

### 6.4 `*AmbiguousError` must carry enough to name candidates

§4.1 requires the CLI to print address, agent name, pid and cwd per candidate. `APIError.Candidates`
is `[]string` and carries addresses only (`wire.go:287`), so the CLI cannot do it today. Change it:

```go
Candidates []AmbiguousCandidate `json:"candidates,omitempty"`
// AmbiguousCandidate: Address, AgentName, PID, Cwd
```

A breaking change to an error body, with one consumer (the CLI), in alpha.

### 6.5 Unchanged

- `POST /api/peers/send`: `to`'s head is a canonical id. No request-shape change.
- `POST /api/peers/deliver`: `findTarget` matches on `agent_session_id` + `pid` + `proc_start`
  (`deliver.go:51`), never on the label, so a v2 `from.address` still reaches its target.

## 7. CLI

| command | change |
|---|---|
| `pdx peers` | `LABEL` moves to the **first** column, before `ADDRESS`, and is **blank** when unset (currently `-`, `peers.go:506`). No `CANONICAL` column — `ADDRESS` carries it. The `*` default marker goes (`peers.go:514`). `--all` keeps `HOST` first, then `LABEL`, then `ADDRESS`. |
| `pdx msg whoami` | `renderSelfRecord` (`msg.go:631`) gains a `canonical:` line |
| `pdx msg name <label>` | success currently prints `named: <address>` (`msg.go:719`); it must print **both** the label set and the unchanged address. An agent that has just named itself is the most likely reader to assume the name is reachable. On a duplicate it prints the warning naming the other holders and exits 0. |
| `pdx msg send <host>/<x>` | `<x>` is a canonical id or a `tmux:` form. Usage text still says `<host>/<label>` (`msg.go:41`, `msg.go:294`) — update. On `*AmbiguousError` list candidates one per line with the §6.4 fields (`msg.go:372`). |
| `peerNotFoundHint` (`send.go:39`) | still teaches the tmux-name form; rewrite to name the canonical id and `pdx peers`. |

### 7.1 `pdx peers` rendering

```
LABEL           ADDRESS                               AGENT  NAME        STATUS  DELIVERABLE  CWD
purdex-tester   mini-lab/_3k9f2mq4:aigora2-purdex-b0  cc     purdex-b0   busy    yes          ~/Workspace/wake/purdex
purdex-tester   mini-lab/_9x2pq0af:purdex1-purdex-69  cc     purdex-69   idle    yes          ~
                mini-lab/_1c4m7dkz:ff-firefly-be      cc     firefly-be  idle    yes          ~
                mini-lab/tmux:aigora3                 -      -           -       no_agent     ~/Workspace/wake/aigora
```

Reading order matches use: scan `LABEL` to find who you want, copy `ADDRESS` to reach them. The
first two rows share a label deliberately — legal under D5, visible, and their addresses differ.

## 8. Compatibility

Alpha: no persistence migration (project convention). Addresses printed before this change are not
expected to keep working — the tmux-derived ones were already unreliable, which is the point.

The project CLAUDE.md "Peer addresses" section is authoritative and must be rewritten in the same
PR: the two bullets from #1079 (the two kinds of default label; the "default label = position /
user label = conversation" framing) go, and the 「被要求「成為 X」時」 workflow must stop implying the
claimed name is an address.

**The serial-number convention survives as a convention.** D5 removes the lock, not the habit.
Draft text (final wording is the PR's business; these are the points it must make):

```markdown
- **被賦予角色時**：`pdx msg name <專案>-<角色>`，例如 `pdx msg name purdex-tester`。
- **回應帶 `label_in_use` 時**：label 已經設好了（不會被拒絕），但請照慣例加序號重設一次 ——
  看回應的 `live_labels` 挑下一個沒被用的序號，`pdx msg name purdex-tester-2`。
  這是慣例不是限制：不加也能運作，但兩個同名的 agent 在 `pdx peers` 上分不出誰是誰。
- **label 只給人和 agent 讀，不能拿來送訊。** 送訊一律用 `address`（`pdx msg whoami` 看自己的、
  `pdx peers` 看別人的）。address 綁 sessionId，改 tmux 名、改 label 都不會變。
```

The third bullet is load-bearing, and §7's requirement that `pdx msg name` print the unchanged
address is the same guard delivered at the moment of the misconception.

### 8.1 SPA (verified on `main`, 2026-09-17)

The SPA does not parse or assemble addresses: `address` is displayed and copied verbatim, and rows
join on `session_code`. §4.5 keeps that true.

**`label_source: ''` widens in meaning, and this is the part the SPA has to be told about.** Under
v2 it had exactly one source: a row with no cc agent, because a live cc conversation always received
a default label. Under v3 it has two:

| `label_source == ""` because | tell them apart with |
|---|---|
| a live cc conversation that has not set a label (the common case) | `canonical != ""` |
| a row with no cc agent at all | `canonical == ""`, or equivalently `agent == null` |

So any SPA logic that read `labelSource === ''` as "this is not an agent row" is now wrong, and must
key off `agent`/`canonical` instead. This is the same one-value-two-meanings shape as §5.4's
`suffix`; it is called out here rather than papered over with a third `label_source` value, because
`canonical` already discriminates and a new enum value would not.

**Sites (full inventory taken on `main` at alpha.365 by purdex-69, 2026-09-17):**

| site | change |
|---|---|
| `spa/src/components/RenamePopover.tsx:132` | `row.labelSource === 'default'` draws the default-label marker. **The only behavioural use.** The marker goes. |
| `spa/src/stores/usePeerStore.ts:28` | type comment `// user \| default \| ''` → `// user \| ''` |
| `spa/src/stores/usePeerStore.ts:93` | passes `label_source` straight through — **no change** |
| `spa/src/stores/usePeerStore.test.ts` :20 :82 :131 | fixtures |
| `spa/src/components/RenamePopover.test.tsx` :224 :333 :483 | fixtures |
| `spa/src/components/StatusBar.test.tsx` :69 | fixture |
| `spa/src/hooks/usePeerInfo.test.ts` :20 | fixture |
| `spa/src/lib/host-lifecycle.test.ts` :933 | fixture inside a cascade test, unrelated to label semantics — the easiest one to miss |

The two tests that assert `labelSource: ''` (`RenamePopover.test.tsx:483`, `usePeerStore.test.ts:131`)
**stay**, but their meaning changes: they now pin the no-agent case specifically, so they must also
assert `canonical === ''` to keep saying what they were written to say.

**Sequencing constraint — satisfied.** PR #1085 merged as alpha.365 (#1087); main also carries
#1088, which touches only `StatusBar.tsx` classes and not `label_source`. Confirmed by purdex-69,
2026-09-17. `origin/main` is merged into this branch.

## 9. Testing

Existing tests asserting tmux-derived default labels are **deleted, not adapted**.

**Test files this change makes red, all of which must be updated in the same PR:**
`internal/peers/label_test.go`, `address_test.go`, `record_test.go`, `wire_test.go`;
`internal/module/peers/labels_test.go`, `module_test.go`, `send_test.go`, `reply_test.go`,
`e2e_test.go`, `helpers_test.go`; `cmd/pdx/peers_test.go`, `cmd/pdx/msg_test.go`.

- `CanonicalID`: deterministic for a fixed sessionId; 8 base36 digits after `_`; differs across
  sessionIds; unaffected by tmux name, cwd or pid.
- `IsCanonicalID`: accepts `_3k9f2mq4`; rejects 6-digit, 7-digit, bare label, `""`, uppercase, `-`.
- Namespace disjointness: `ValidateUserLabel` rejects every string `IsCanonicalID` accepts.
- `ValidateWireAddress`: accepts 8-digit and 6-digit heads and a user label head; **rejects 7**;
  rejects 5, 9, uppercase, and the reserved `cc` / `tmux`. A v3 `DeliverRequest` with an 8-digit
  `from.address` passes `Validate()`.
- `Resolve`: a canonical resolves; **a label does not** (falls through both tiers to `ErrNotFound`);
  two rows sharing a canonical → `*AmbiguousError`; `tmux:<name>` and bare-tmux tier 2 still
  resolve; `Partial` / `RegistryIncomplete` unchanged.
- `applyLabel` / `Build`, asserted as §4.5's table on every live-cc row; `canonical` `""` on rows
  with `agent: null`; the three other address writers (§4.4) keep their formats.
- **Two live rows may hold the same `label`**, keep distinct `canonical` and `address`, and both
  still resolve. This is D5's regression test and the point of the change.
- `PUT /api/peers/self/label` with a label another live session holds: 200, label set, envelope
  carries `label_in_use`, the other holder, **and `live_labels`** — the last is what makes the
  convention one-step, so it is asserted, not assumed. Re-setting to `purdex-tester-2` succeeds with
  no warning and leaves the first holder untouched.
- `claim` with unreadable registry files no longer 503s (§4.2's removed gate).
- Suffix: a session row whose registry `tmux` name differs from the live inventory renders the
  **live** name (the P1 regression test).
- CLI: `*AmbiguousError` rendering names every candidate with the §6.4 fields — asserted on the
  rendered string, not the error value, because the requirement is about what the operator reads.
- End to end: send to a canonical; rename the target's tmux session and confirm the address still
  resolves; set a label and confirm the address is unchanged and the label is not resolvable.
- Gates: `go build ./...`, `go test ./...`, `go vet ./...`, and for the SPA line
  `cd spa && npx vitest run && pnpm run lint && pnpm run build`.

## 10. Out of scope

Each of these was raised by a review pass and deliberately deferred; open an issue rather than
widening this PR.

- **Same-host delivery through pdx** (`send.go`'s `local_target`). Unifying Claude Code's own
  session name with the pdx address is a separate spec that depends on this one landing.
- **Renaming `label.go`.** After §4.3 its contents are address-string grammar, not labels, so the
  name becomes inaccurate — but renaming adds diff noise to an already large change.
- **Splitting `PeerRecord`, `buildSessionRecord`, or `localEnvelope`.** All are already fat; none
  gets materially worse here.
- Removing `tmux:` / bare-tmux location addressing, and their two-tmux-server ambiguity (§5.1).
- Splitting `Partial` by cause (§5.1).
- The owner-fallback rows' unresolvable address (§4.5).
- Host alias / host_id confusion in `HostMatches` when config is misconfigured (attack review, Medium).
- `findTarget`'s `sessionId`+pid+`proc_start` triple under second-granularity PID reuse
  (attack review, Low) — same trust boundary as §3.2.
- A `note` / `role` field (D6).
