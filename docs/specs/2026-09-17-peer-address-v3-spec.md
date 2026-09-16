# Peer Address v3 — canonical + alias

Date: 2026-09-17 · Scope: daemon (`internal/peers`, `internal/module/peers`) + `pdx` CLI · Single phase

Supersedes the default-label half of `2026-09-16-peer-default-label-tmux-spec.md` (#1079, alpha.363).

## 1. Goal

Give every conversation **one address that never changes** and, optionally, **one address that
means something**. Today it has exactly one address and it is neither: it is derived from the tmux
session name, which goes stale the moment the user renames the session and can be taken away by a
third party's unrelated action.

- **canonical** — `mini-lab/_3k9f2mq4`. Derived from the Claude Code `sessionId`. Immutable for the
  life of the conversation, survives resume and daemon restart, unaffected by anything the user
  does to tmux. Always resolvable.
- **alias** — `mini-lab/purdex-tester`. The existing user label, claimed with `pdx msg name`.
  Unique among live conversations, released when the conversation dies, freely re-pointed.

Both resolve. Neither can take the other away.

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
`*AmbiguousError`, so delivery is refused rather than misrouted. Both conversations lose their
canonical address until one exits.

## 3. Decisions (user-confirmed, do not reopen)

| # | Decision |
|---|---|
| D1 | Two layers: an immutable **canonical** address and an optional **alias**. Both are accepted by every resolver. |
| D2 | Canonical is derived from `sessionId` **only**. No tmux name, no Claude Code `name`, no cwd — nothing the user or another process can change. |
| D3 | Alias is the existing user label. Claim (`pdx msg name`), uniqueness among live sessions, release on death, `label_taken` + `live_labels` on conflict — all unchanged. |
| D4 | The tmux-derived default label is **removed**, not kept as a third tier. A conversation with no alias has only its canonical address. |
| D5 | Collision is handled by making it negligible (widen the id) plus the existing ambiguity refusal, **not** by stateful assignment. Canonical must stay a pure function of `sessionId`. |
| D6 | No `note` / `role` field. A conversation's role is its alias, which is already addressable. |
| D7 | Conflicts must surface **at claim time** (`pdx msg name` → `label_taken`), never by silently changing an address that already works. |

## 4. Data model

### 4.1 Canonical id (`internal/peers/label.go`)

```go
// CanonicalID derives a conversation's immutable address component from its
// Claude Code sessionId: "_" + base36(FNV-1a-64(sessionId) mod 36^8), 8 digits,
// zero-padded. Pure, deterministic across resumes and daemon restarts.
func CanonicalID(sessionID string) string
```

- Replaces `DefaultLabel`, which is deleted along with the rest of §4.3.
- **Width 8, not 6.** 36⁸ ≈ 2.82e12. For 100 live conversations the birthday probability is ≈1.8e-9,
  against ≈2.3e-6 at width 6. This is the whole of D5's collision handling; P3 needs no machinery.
- The leading `_` is what keeps the two namespaces disjoint: a user label must match
  `^[a-z0-9][a-z0-9-]{1,31}$` and therefore can never begin with `_`. An alias can never shadow a
  canonical id and a canonical id can never block an alias.
- `IsCanonicalID(s)` replaces `IsDefaultLabel`, pattern `^_[0-9a-z]{8}$`.

**A safe failure has to say so.** D5 leans on `*AmbiguousError` being the outcome of a collision, so
that outcome must be legible as a collision and not as a malfunction. `AmbiguousError` already
carries `Candidates`; `pdx msg send` must print every candidate — address, agent name, pid, cwd —
rather than a bare "ambiguous". Without that, the operator sees "my address stopped working" and
goes looking for a bug that is not there. Covered in §7 and §9.

### 4.2 Alias — unchanged

`internal/module/peers/labels.go` and the `LabelInfo` store keep their current behaviour: claim,
`label_taken` with `holder` + `live_labels`, release, `label_rev`, dead rows inert.

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

`userLabelHoldersOf` is no longer needed because §4.1's `_` prefix makes the two namespaces
disjoint by construction rather than by a runtime check.

> This removes most of #1079. The occupancy reasoning it introduced is not being discarded because
> it was wrong — it is being discarded because D2 removes the mutable input that made it necessary.

**What is NOT being deleted, and where it lives.** Alias uniqueness among live conversations is
unaffected by this section. It was never implemented here: it is enforced at claim time by
`internal/module/peers/labels.go` — the claim matrix, `label_taken` with `holder` + `live_labels`,
and the rule that a dead holder's row is inert and releases the name. None of that appears in the
table above, and none of it changes.

The three occupancy rules deleted here answered a different question — "which conversation may mint
a label from this tmux session name" — and that question stops existing under D2. A reader who
takes "the occupancy rules are gone" to mean "alias uniqueness is gone" will be tempted to add a
duplicate check back into the record build. Do not: the claim path already refuses a taken alias
before it can ever reach a record.

### 4.4 `PeerRecord` (`internal/peers/record.go`)

Two fields are **added**; `label` and `address` keep the meaning consumers already rely on.

```go
Canonical   string `json:"canonical"`    // NEW: "_3k9f2mq4"; "" when the row has no cc agent
Alias       string `json:"alias"`        // NEW: the claimed user label; "" when none
Label       string `json:"label"`        // the head used in Address: Alias if claimed, else Canonical
LabelSource string `json:"label_source"` // CHANGED: "user" | "canonical" | ""  ("default" is gone)
LabelRev    int64  `json:"label_rev"`    // unchanged; 0 when no alias
Address     string `json:"address"`      // unchanged: always <host>/<label>
```

- `Canonical` and `Alias` are bare ids, like `Label`. `Address` stays the only full-form field;
  a consumer needing the canonical address builds `<host>/<canonical>`.
- `Suffix` keeps its shape (`san(tmux)-san(cc)`) but **must be built from the live tmux inventory**,
  not `Entry.TmuxSessionName()` — see §5.3.
- `WireAddress()` is unchanged: `Label + ":" + Suffix`. Because `Label` is always a resolvable head,
  a v3 sender's `from.address` needs no new handling on the receiving side.

### 4.5 Field invariants (the consumer contract)

These hold for every row with a live cc agent, and exist so no consumer has to guess which kind of
address it is looking at:

| invariant | |
|---|---|
| `address == host + "/" + label` | always, no exceptions — an address is always pasteable into `pdx msg send` |
| `label != ""` | always |
| `label == (alias != "" ? alias : canonical)` | the only rule for which head is in play |
| `label_source == (alias != "" ? "user" : "canonical")` | one field answers "is this the claimed name or the machine one" |
| `canonical != ""` and immutable for the conversation's life | the address that always works |
| `alias` may change or become `""` at any time | claiming, releasing, or the holder dying |

A row with no live cc agent (`row_kind: session` with `agent: null`) has all five as `""` / `0`,
exactly as today.

> **Breaking change for consumers:** `label_source` no longer emits `"default"`. Anything switching
> on that string must move to `"canonical"`. This is the only value change in the record; `label`
> and `address` are shape-compatible with v2.

## 5. Resolution

### 5.1 `Resolve` tiers (`internal/peers/address.go`)

Tier 1's predicate gains the canonical arm. Everything else is untouched:

```go
// Tier 1: alias OR canonical, over every row backed by a live entry.
rec, err := resolveTier(records, session, func(r PeerRecord) bool {
    return hasLiveEntry(r) && ((r.Label != "" && r.Label == head) || r.Canonical == head)
})
```

- `cc:<x>` — still `ErrNotFound` wrapping `ErrLegacyCC`.
- `tmux:<name>` — unchanged. It already matches `PeerRecord.SessionName`, which comes from the
  **live** tmux inventory, so it is the one form that was never stale.
- Tier 2 (bare string as a tmux session name) — unchanged.
- The ambiguity, `RegistryIncomplete` and `Partial` rules are unchanged and now also cover the
  canonical arm, which is D5's safety net for P3.

### 5.2 Building the label fields (`record.go`, `applyLabel`)

```
canonical = CanonicalID(sessionID)
alias     = labelStore[sessionID].Label        // "" when unclaimed

if alias != "" { label, source = alias,     "user"      }
else           { label, source = canonical, "canonical" }

address = host + "/" + label
```

`applyLabel`'s `defaultLabel` parameter is replaced by `canonical`; call sites in `record.go`
(3 places) and `module.go` drop the `defaults.For(...)` argument. The function now also sets
`Canonical` and `Alias`, so §4.5's invariants are established in exactly one place.

### 5.3 Suffix must come from live tmux data

`Suffix` is display-only, but it is built from `Entry.TmuxSessionName()` — the same frozen field as
P1 — so it shows a tmux name that may no longer exist. For a `row_kind: session` row the live name
is already in the same build (`SessionSummary.Name`, from the tmux inventory). Use it.

For an `entry` row with no session row behind it there is no live name available; keep the registry
value, since such a row is by definition not attached to a listed tmux session.

**This makes `suffix` carry two provenances in one response**, and that must be stated rather than
left for the next reader to discover: on a `session` row it is live, on an `entry` row it may be the
value frozen at the agent's startup. No field is added to tell them apart — `row_kind` already
does, and `suffix` is display-only and explicitly not an identity (§4.4). But the doc comment on
`PeerRecord.Suffix` must say exactly this, because "one field, two meanings, no discriminator" is
the shape of the `partial` defect found in #1079's review.

## 6. Wire / API

- `GET /api/peers`: rows gain `canonical` and `alias`; `label` and `address` keep their meaning
  (§4.5); `label_source` emits `"canonical"` where it used to emit `"default"`.
- `POST /api/peers/send`: `to` accepts an alias or a canonical id. No request-shape change —
  §5.1 does the work.
- `POST /api/peers/deliver`: unchanged. `findTarget` already matches on
  `agent_session_id` + `pid` + `proc_start`, never on the label.
- `POST /api/peers/self` (`whoami`): response gains `canonical`; it must print **both** addresses,
  because an agent cannot derive its canonical id itself.
- `PUT/DELETE /api/peers/self/label`: unchanged.
- No version negotiation. A v2 peer sending `from.address` with a stale tmux head simply fails to
  resolve on the receiver, which is the same outcome as today for a renamed session.

## 7. CLI

| command | change |
|---|---|
| `pdx peers` | the `LABEL` column is replaced by `CANONICAL` (same width budget). `ADDRESS` already shows the alias when one is claimed, so the pair reads "the name it answers to" + "the name it always answers to". The `*` default-label marker is removed — there is no longer such a thing. |
| `pdx peers --json` | per §6 |
| `pdx msg whoami` | prints `canonical:` and `alias:` lines; `address:` stays as the preferred form |
| `pdx msg send <host>/<x>` | `<x>` may be an alias or a canonical id. On `*AmbiguousError` the candidates are listed one per line (address, agent name, pid, cwd), not summarised as a count — §4.1 |
| `pdx msg name` | unchanged |

## 8. Compatibility

Alpha: no persistence migration (project convention). Addresses printed before this change are not
expected to keep working — the tmux-derived ones were already unreliable, which is the point.

`docs`/`CLAUDE.md` text that describes the default label as "the tmux session name" must be updated
in the same PR; the project CLAUDE.md "Peer addresses" section is the authoritative copy. The two
bullets introduced by #1079 (the two kinds of default label, and the "default label = position /
user label = conversation" framing) are removed: a canonical id is neither a position nor a name,
it is an identity.

### 8.1 SPA dependency (confirmed with purdex-69, 2026-09-17)

The SPA work in flight does **not** parse or assemble addresses: `address` is displayed and copied
verbatim, and rows are joined on `session_code`, not on the label. §4.5 is what keeps that true.

One change is required on the SPA side:

- `label_source === 'default'` is used to draw a marker (one site, `RenamePopover.tsx`). That value
  becomes `'canonical'`.

**Sequencing constraint.** PR #1085 (peer info in the tab panel and status bar) is in review and
its tests touch that component. This change must **not** land before #1085 merges, or #1085 goes red
on a field unrelated to it. Either #1085 merges first and the one-line SPA change rides in this PR,
or purdex-69 lands it as a follow-up immediately after. Agreed with purdex-69, 2026-09-17.

`suffix` is unused by the SPA, so §5.3 does not affect it. If the wording of any `reason` value
changes, `spa/src/lib/peer-display.ts` and the `peer.*` i18n namespace are the single copies to
update — this spec changes no `reason` value.

## 9. Testing

Existing tests that assert tmux-derived default labels are **deleted, not adapted** — the behaviour
is gone. `label_test.go`'s `ResolveDefaultLabels` table goes with §4.3.

- `CanonicalID`: deterministic for a fixed sessionId; 8 base36 digits after `_`; differs for
  different sessionIds; unaffected by tmux name, cwd or pid.
- `IsCanonicalID`: accepts `_3k9f2mq4`, rejects the 6-digit form, a bare label, `""`, and anything
  with an uppercase or `-`.
- Namespace disjointness: `ValidateUserLabel` rejects every string `IsCanonicalID` accepts.
- `Resolve`: canonical resolves; alias resolves; a row with an alias still resolves by its
  canonical; two rows sharing a canonical → `*AmbiguousError`; `tmux:<name>` still bypasses tier 1;
  `Partial` / `RegistryIncomplete` behaviour unchanged for both arms.
- CLI rendering of `*AmbiguousError` names every candidate (§4.1). Asserted on the rendered string,
  not on the error value — the point of the requirement is what the operator reads.
- `applyLabel` / `Build`, asserted as §4.5's table: `address == host+"/"+label` on every row;
  alias claimed → `label == alias`, `label_source == "user"`; no alias → `label == canonical`,
  `label_source == "canonical"`; `canonical` non-empty and `alias` possibly empty on every row with
  a live cc agent; all five empty on a row with `agent: null`.
- A row keeps the same `canonical` across an alias being claimed and then released, and the
  canonical still resolves throughout (the "both addresses work at once" case).
- Suffix: a session row whose registry `tmux` name differs from the live inventory name renders the
  **live** name (the P1 regression test).
- `labels.go`: claim / `label_taken` + `live_labels` / release / dead-holder-inert all still pass
  unchanged — this layer is untouched and its tests prove it.
- End to end (`e2e_test.go`): send to a canonical id, send to an alias, send to an alias after the
  holder released it (→ not found), rename the target's tmux session and confirm **both** addresses
  still resolve.
- Gates: `go build ./...`, `go test ./...`, `go vet ./...`.

## 10. Out of scope

- **Same-host delivery through pdx.** `send.go`'s D1 (`local_target`) is unchanged here. Unifying
  the two namespaces that exist on one host — Claude Code's own session name vs the pdx alias — is a
  separate spec that depends on this one landing first.
- SPA surfacing of `/api/peers` (in flight elsewhere).
- A `note` / `role` field (D6).
- Reviving the `cc:` form.
