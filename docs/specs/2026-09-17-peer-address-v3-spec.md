# Peer Address v3 — one address, plus a label that is only a label

Date: 2026-09-17 · Scope: daemon (`internal/peers`, `internal/module/peers`) + `pdx` CLI · Single phase

Supersedes the default-label half of `2026-09-16-peer-default-label-tmux-spec.md` (#1079, alpha.363).

## 1. Goal

A conversation gets **exactly one address**, derived from its `sessionId`, immutable for its life.
Nothing a user or another process does can change it, take it away, or hand it to someone else.

The `label` column stays, but it stops being a second way to address anything. It becomes what its
name always suggested: a short name a conversation calls itself, for a human or another agent to
read when deciding who to talk to. Reading it is how you *choose* a peer; the address is how you
*reach* one.

This is the whole of the collision story: **the only thing that must be unique is the only thing
nobody can choose.**

## 2. Evidence — what is broken today

### P1. A renamed tmux session leaves the address permanently stale

`Entry.Tmux` comes from the `tmux` field of Claude Code's own registry file
(`~/.claude/sessions/<pid>.json`). Claude Code captures it at startup and never refreshes it.

Measured 2026-09-16 on `mini-lab`, renaming this session's own tmux session:

| step | observation |
|---|---|
| before | registry `tmux = aigora2:@5.%5`, `pdx msg whoami` → `mini-lab/aigora2` |
| `tmux rename-session aigora2 aigora2zz` | live tmux name is now `aigora2zz` |
| after the registry file was **rewritten** (mtime advanced, `status` went `busy` → `shell`) | registry `tmux` **still** `aigora2:@5.%5` |

The file is rewritten on status changes and the `tmux` field is copied through unchanged, so the
staleness is not transient: it lasts until the agent exits. The address names a tmux session that
no longer exists.

### P2. A third party's new session evicts both conversations

Because the stale name is still claimed, a new tmux session reusing that name plus a live agent
makes two conversations report the same candidate. `ResolveDefaultLabels` rule 2 then skips both:

```go
if occupantsByLabel[candidate].otherThan(sid) { continue }   // label.go
```

Covered by the existing test `"two unnamed conversations in one tmux session" → DefaultLabels{}`.
The incumbent did nothing; its address silently changes to the hash form, and anything holding the
old address stops resolving. Nobody is told.

### P3. The hash fallback skips the collision rules

`DefaultLabels.For()` returns `DefaultLabel(sessionID)` unconditionally when no candidate survived —
it never consults occupancy. With a 36⁶ space (≈2.18e9) a birthday collision between live
conversations is unlikely but unbounded by anything in the code.

The failure is **safe but total**: `resolveTier` sees two matching rows and returns
`*AmbiguousError` (`address.go:137`) and `send.go:309` answers 409 `ambiguous` without picking a
candidate, so delivery is refused rather than misrouted. Both conversations lose their address
until one exits.

## 3. Decisions (user-confirmed, do not reopen)

| # | Decision |
|---|---|
| D1 | **One address per conversation**, the canonical id. There is no second naming scheme and no aliasing layer. To reach a peer you use its address. |
| D2 | The canonical id is derived from `sessionId` **only**. No tmux name, no Claude Code `name`, no cwd — nothing a user or another process can change. |
| D3 | `label` is a **self-declared display name**. It is never resolved, never routed on, and carries no uniqueness guarantee. |
| D4 | The tmux-derived default label is **removed**. `label` is empty until a conversation sets one. |
| D5 | Two conversations may hold the same `label`. Setting one that is already in use **warns and succeeds**; it never refuses and never renames anyone. |
| D6 | No extra `note` / `role` field. `label` is that field, and calling it `label` is what keeps it written as a name rather than a sentence. |
| D7 | Address conflicts are made structurally impossible rather than adjudicated. Label conflicts are visible, harmless, and left alone. |

### 3.1 Why this removes the conflict problem rather than managing it

Every failure in §2 has the same shape: the address depended on a string someone else could change
or claim. D1 + D2 remove that dependency entirely — an address is a function of an identity nobody
issues, requests, or competes for. There is no allocation, so there is nothing to allocate twice.

`label` can collide precisely because nothing depends on it being unique. Two `purdex-tester` rows
are two rows a reader can see and choose between, not a routing decision anyone has to make.

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
  `^[a-z0-9][a-z0-9-]{1,31}$` and can never begin with `_`. A label can never be mistaken for an
  address by the resolver even though the resolver no longer looks at labels at all.
- `IsCanonicalID(s)` replaces `IsDefaultLabel`, pattern `^_[0-9a-z]{8}$`.

**Collision handling is the ambiguity refusal that already exists**, kept as a backstop rather than
a mechanism: two live rows sharing a canonical produce `*AmbiguousError` and a 409, so the failure
is a refusal to deliver, never a delivery to the wrong conversation. A stateful allocator is
rejected — it would make the id impure, and purity is the property the whole design rests on.

**A safe failure has to say so.** `pdx msg send` must print every candidate an `*AmbiguousError`
carries — address, agent name, pid, cwd — not a bare "ambiguous". Otherwise the operator reads "my
address stopped working" and goes hunting for a bug that is not there. §7 and §9.

### 4.2 `label` (`internal/module/peers/labels.go`)

The store and the `pdx msg name` route stay; their contract loosens:

| before | after |
|---|---|
| claiming a taken label → 409 `label_taken`, refused | **succeeds**, response carries `warning: "label_in_use"` plus `live_labels` and the other `holder`s |
| a label was the address head | a label is display-only |
| `label_rev` tracked address changes | see §4.4 — retained on the wire for v2 senders, always 0 for v3 |
| dead holder's row inert (releases the name) | unchanged — a dead conversation should not show a label |

Grammar is unchanged (`ValidateUserLabel`), so the `<project>-<role>[-<n>]` convention in the
project CLAUDE.md keeps working. The `-2` in `purdex-tester-2` is now a courtesy to readers rather
than a uniqueness requirement — which is why D5 warns instead of refusing: the convention is worth
nudging toward, not worth enforcing.

### 4.3 Deletions

With D4, everything that existed to mint a label from a tmux name goes:

| symbol | file |
|---|---|
| `DefaultLabels`, `DefaultLabels.For` | `label.go` |
| `ResolveDefaultLabels` | `label.go` |
| `candidatesOf`, `occupantsOf`, `userLabelHoldersOf`, `sidSet`, `sidSet.otherThan` | `label.go` |
| `qualifiesAsDefaultLabel` | `label.go` |
| `DefaultLabel`, `IsDefaultLabel`, `defaultLabelPattern`, `defaultLabelN`, `labelSpace` | `label.go` (replaced by §4.1) |
| `LabelSourceDefault` | `label.go` |

`userLabelHoldersOf` is doubly unnecessary now: §4.1's `_` prefix already made the namespaces
disjoint, and under D3 a label cannot block anything because nothing resolves through it.

> This removes most of #1079. The occupancy reasoning it introduced is not being discarded because
> it was wrong — it is being discarded because D2 removes the mutable input that made it necessary.

**What is NOT being deleted.** The label store and its claim route survive (§4.2). What changes
there is the verdict on a duplicate, not the existence of the path. A reader who takes "the
occupancy rules are gone" to mean "labels are gone" will delete too much.

### 4.4 `PeerRecord` (`internal/peers/record.go`)

```go
Canonical   string `json:"canonical"`    // NEW: "_3k9f2mq4"; "" when the row has no cc agent
Label       string `json:"label"`        // CHANGED: self-declared display name; "" until one is set
LabelSource string `json:"label_source"` // CHANGED: "user" | ""   ("default" is gone)
LabelRev    int64  `json:"label_rev"`    // retained for v2 wire compat; always 0 for v3
Address     string `json:"address"`      // CHANGED head, unchanged FORMAT: <host>/<canonical>:<suffix>
```

- **`Address` keeps its current format, suffix included.** `applyLabel` builds
  `alias + "/" + <head> + ":" + Suffix` (`record.go:265`) — that `alias` parameter is the **host**
  alias, and stays so. Only the head changes: it is now always the canonical id. Dropping the suffix
  would be a breaking change and is not proposed.
- `label` no longer participates in the address, so there is no "which head is it" question to
  answer and no field needed to answer it.
- `LabelRev` / `WireTo.AddressRev` stay on the wire because a v2 sender still sends them and
  `deliver.go:277` uses them to name the sender's proxy helper. A v3 address never changes, so a v3
  sender always sends 0 and the helper is never renamed.

### 4.5 Field invariants (the consumer contract)

For every row whose agent is a **live** cc entry (`agent.type == "cc"` and `agent.pid != 0` — the
`hasLiveEntry` test the resolver uses):

| invariant | |
|---|---|
| `address == host + "/" + canonical + ":" + suffix` | one shape, always |
| `canonical != ""`, immutable for the conversation's life | |
| `label` may be `""`, may change, **may be shared with another row** | never a key, never routed on |
| `label_source == (label != "" ? "user" : "")` | |

Two other row kinds keep today's behaviour and are **not** covered above:

| row | address | note |
|---|---|---|
| `row_kind: session`, `agent: null` | `<host>/tmux:<session_name>` (`record.go:151`) | all label fields `""` |
| owner-fallback rows (`inbox_dead` / `ambiguous`: `agent.type == "cc"`, `agent.pid == 0`) | `<host>/<canonical>:<suffix>` via `applyLabel` | **pre-existing wart, not introduced here**: renders an address tier 1 will not resolve, because `hasLiveEntry` is false. `reason` already says why. Out of scope; called out so the table is not read as covering it. |

> **Breaking change for consumers:** `label_source` no longer emits `"default"`, and `label` is now
> empty on a conversation that has not set one. Anything that displayed `label` as an identifier
> must display `canonical` or `address` instead.

## 5. Resolution

### 5.1 `Resolve` tiers (`internal/peers/address.go`)

Tier 1 stops matching labels and matches the canonical id instead:

```go
// Tier 1: the canonical id, over every row backed by a live entry.
rec, err := resolveTier(records, session, func(r PeerRecord) bool {
    return hasLiveEntry(r) && r.Canonical == head
})
```

- `cc:<x>` — still `ErrNotFound` wrapping `ErrLegacyCC`.
- `tmux:<name>` and tier 2 (a bare string as a tmux session name) — **kept, unchanged**. These
  address a *place*, not a conversation: they match `PeerRecord.SessionName`, which comes from the
  live tmux inventory and was never stale. They are not a second naming scheme — nobody issues or
  claims them — so D1 does not reach them. A bare label like `purdex-tester` now falls through
  tier 1, fails tier 2, and returns `ErrNotFound`, which is the correct and legible outcome.
- **Accepted conservatism, stated so it is not mistaken for an oversight:** a tier-1 miss under
  `snap.Partial` returns `ErrResolveNotReady` (`address.go:115`). A canonical id depends on neither
  the label store nor owner resolution, so a partial inventory can never be the reason it missed —
  yet it is still retried rather than reported missing. That is the safe direction (a retry costs a
  round trip, a false "not found" costs a message). Splitting `Partial` by cause is out of scope.

### 5.2 Building the fields (`record.go`, `applyLabel`)

```
canonical = CanonicalID(sessionID)
label     = labelStore[sessionID].Label        // "" when none set

head      = canonical                          // always
address   = host + "/" + head + ":" + suffix
source    = label != "" ? "user" : ""
```

`applyLabel`'s `defaultLabel` parameter becomes `canonical`; the call sites in `record.go`
(7 places) and `module.go` drop the `defaults.For(...)` argument. The function also sets
`Canonical`, so §4.5's invariants are established in exactly one place.

### 5.3 Suffix must come from live tmux data

`Suffix` is display-only, but part of it comes from `Entry.TmuxSessionName()` — the same frozen
field as P1 — so it can show a tmux name that no longer exists. Most call sites are already correct;
only three are not:

| `record.go` | `tmuxName` argument | verdict |
|---|---|---|
| 205, 211, 229, 239 | `s.Name` — live `SessionSummary` | already correct, leave |
| **216, 234** | `candidates[0].TmuxSessionName()` / `paneMatches[0].TmuxSessionName()` — frozen | **fix**: `s.Name` is in scope and is the live name for the very session being rendered |
| **321** | `e.TmuxSessionName()` — frozen, `entry` rows | keep: an entry row with no session row behind it has no live name to use |

So this is a two-line fix for session rows, not a rework.

**This leaves `suffix` with two provenances in one response**, and that must be stated rather than
left for the next reader to discover: live on a `session` row, possibly frozen on an `entry` row.
No field is added to tell them apart — `row_kind` already does, and `suffix` is display-only and
explicitly not an identity. But the doc comment on `PeerRecord.Suffix` must say exactly this,
because "one field, two meanings, no discriminator" is the shape of the `partial` defect found in
#1079's review.

## 6. Wire / API

- `GET /api/peers`: rows gain `canonical`; `label` / `label_source` change meaning per §4.4.
- **`ValidateWireAddress` must be widened (`wire.go:415`) — implementation blocker.** It gates
  `from.address`'s head through `IsDefaultLabel` (exactly 6 digits) or `ValidateUserLabel`. An
  8-digit canonical passes neither, so a v3 sender's own `DeliverRequest.Validate()` would reject it
  at `send.go:353` before anything left the host. The head must accept `IsCanonicalID`, **and keep
  accepting the 6-digit form** for as long as a v2 peer may still be sending. Concretely: accept
  `^_[0-9a-z]{6,8}$` for the head, alongside `ValidateUserLabel` (a v2 sender may still present a
  user label as its head).
- `POST /api/peers/send`: `to`'s head is a canonical id. No request-shape change.
- `POST /api/peers/deliver`: unchanged. `findTarget` matches on `agent_session_id` + `pid` +
  `proc_start` (`deliver.go:51`), never on the label, so a v2 `from.address` still reaches its
  target; it is used only to name the sender's proxy helper (`deliver.go:277`).
- `PUT /api/peers/self/label`: **no longer refuses a duplicate.** 200 with
  `warning: "label_in_use"`, `live_labels`, and the other holders, instead of 409 `label_taken`.
  `ErrLabelTaken` is removed from the error vocabulary.
- `POST /api/peers/self` (`whoami`): response gains `canonical`.

## 7. CLI

| command | change |
|---|---|
| `pdx peers` | `LABEL` moves to the **first** column, before `ADDRESS`, and is **blank** when unset (not `-`). No `CANONICAL` column is added — `ADDRESS` already carries it. The `*` default-label marker is removed; there is no longer such a thing. |
| `pdx msg whoami` | prints `canonical:` alongside `address:` and `label:` |
| `pdx msg name <label>` | on success prints **both** the label it set and the address, which is unchanged. An agent that has just named itself must see immediately that its address did not move — this is what stops "I named it, so I can send to that name". On a duplicate it prints a warning naming the other holders and exits 0. |
| `pdx msg send <host>/<x>` | `<x>` is a canonical id (or a `tmux:` form). On `*AmbiguousError` the candidates are listed one per line — address, agent name, pid, cwd — not summarised as a count (§4.1) |
| `peerNotFoundHint` (`send.go:39`) | still tells the operator that an unnamed session is addressed by its tmux session name. Rewrite: name the canonical id and `pdx peers` as the way to find one. |

### 7.1 `pdx peers` rendering

```
LABEL           ADDRESS                             AGENT  NAME        STATUS  DELIVERABLE  CWD
purdex-tester   mini-lab/_3k9f2mq4:aigora2-purdex-b0  cc   purdex-b0   busy    yes          ~/Workspace/wake/purdex
purdex-tester   mini-lab/_9x2pq0af:purdex1-purdex-69  cc   purdex-69   idle    yes          ~
                mini-lab/_1c4m7dkz:ff-firefly-be      cc   firefly-be  idle    yes          ~
                mini-lab/tmux:aigora3                 -    -           -       no_agent     ~/Workspace/wake/aigora
```

Reading order matches use: scan `LABEL` to find who you want, copy `ADDRESS` to reach them. The
first two rows share a label deliberately — that is legal under D5, it is visible, and their
addresses are still distinct. A blank `LABEL` means the conversation has not named itself; it is
addressed the same way as any other.

## 8. Compatibility

Alpha: no persistence migration (project convention). Addresses printed before this change are not
expected to keep working — the tmux-derived ones were already unreliable, which is the point.

The project CLAUDE.md "Peer addresses" section is the authoritative copy and must be rewritten in
the same PR. Specifically: the two bullets introduced by #1079 (the two kinds of default label, and
the "default label = position / user label = conversation" framing) go; and the "被要求「成為 X」時"
workflow must stop implying the claimed name is an address — it now sets a label and reports the
unchanged address.

### 8.1 SPA dependency (confirmed with purdex-69, 2026-09-17)

The SPA does not parse or assemble addresses: `address` is displayed and copied verbatim, and rows
are joined on `session_code`, not on the label. §4.5 keeps that true.

Two changes are required on the SPA side:

- `label_source === 'default'` is used to draw a marker (one site, `RenamePopover.tsx`). That value
  is gone; the marker either goes or keys off `label !== ''`.
- Anywhere `label` is shown as the peer's identifier must show `address` (or `canonical`) instead,
  since `label` is now empty by default and non-unique when set.

**Sequencing constraint.** PR #1085 (peer info in the tab panel and status bar) is in review and its
tests touch that component. This change must **not** land before #1085 merges. Agreed with
purdex-69, 2026-09-17; confirm its state before landing.

## 9. Testing

Existing tests that assert tmux-derived default labels are **deleted, not adapted** — the behaviour
is gone. `label_test.go`'s `ResolveDefaultLabels` table goes with §4.3.

- `CanonicalID`: deterministic for a fixed sessionId; 8 base36 digits after `_`; differs for
  different sessionIds; unaffected by tmux name, cwd or pid.
- `IsCanonicalID`: accepts `_3k9f2mq4`; rejects the 6-digit form, a bare label, `""`, uppercase, `-`.
- Namespace disjointness: `ValidateUserLabel` rejects every string `IsCanonicalID` accepts.
- `Resolve`: a canonical resolves; **a label does not resolve** (falls through tier 1 and tier 2 to
  `ErrNotFound`); two rows sharing a canonical → `*AmbiguousError`; `tmux:<name>` and bare-tmux
  tier 2 still resolve; `Partial` / `RegistryIncomplete` behaviour unchanged.
- CLI rendering of `*AmbiguousError` names every candidate (§4.1). Asserted on the rendered string,
  not the error value — the requirement is about what the operator reads.
- `applyLabel` / `Build`, asserted as §4.5's table: `address == host+"/"+canonical+":"+suffix` on
  every live-cc row; `canonical` non-empty there and `""` on a row with `agent: null`;
  `label_source == "user"` exactly when `label != ""`.
- **Two live rows may hold the same `label`** and both keep their own distinct `canonical` and
  `address`, and both still resolve. This is D5's regression test and the point of the whole change.
- `PUT /api/peers/self/label` with a label another live session holds: 200, label is set, response
  carries `label_in_use` and the other holder; the other session is unaffected.
- Suffix: a session row whose registry `tmux` name differs from the live inventory name renders the
  **live** name (the P1 regression test).
- `ValidateWireAddress`: accepts an 8-digit canonical head; **still** accepts a 6-digit v2 head and
  a user label head; rejects 5 and 9 digits, uppercase, and the reserved `cc` / `tmux`. A v3
  `DeliverRequest` carrying an 8-digit `from.address` passes `Validate()`.
- End to end (`e2e_test.go`): send to a canonical; rename the target's tmux session and confirm the
  address still resolves; set a label on the target and confirm the address is unchanged and the
  label is not resolvable.
- Gates: `go build ./...`, `go test ./...`, `go vet ./...`.

## 10. Out of scope

- **Same-host delivery through pdx.** `send.go`'s D1 (`local_target`) is unchanged here. Unifying
  the two namespaces that exist on one host — Claude Code's own session name vs the pdx address — is
  a separate spec that depends on this one landing first.
- Removing `tmux:` / bare-tmux location addressing (§5.1).
- Splitting `Partial` by cause (§5.1).
- Fixing the owner-fallback rows' unresolvable address (§4.5).
- A `note` / `role` field (D6).
