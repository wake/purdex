# Spec — Default peer label from the tmux session name

Status: draft v2 (codex spec review `task-mu3w0qhs-ehc5la`: 1 Blocker, 4 Majors,
3 Minors, 5 omissions — all accepted except the legacy-hash alias tier; §9 holds
the disposition)
Date: 2026-09-16
Branch: `worktree-peer-default-label`
Amends: `2026-09-14-peer-address-v2-spec.md` v3.3 (§3.1 "Default label", §3.2 tier 1)

> Scope: the **default** label only. User labels (`pdx msg name`), the claim
> matrix, the wire contract, delivery, authentication and the suffix are
> unchanged. Section numbers without a prefix refer to this document; "v2 §x"
> refers to the Peer Address v2 spec.

## 1. Problem

Peer Address v2 gives a conversation with no claimed label the default
`"_" + base36(FNV-1a-64(sessionId) mod 36⁶)` (v2 §3.1). On this fleet today
every single agent is still on its default — naming rate 0% across 14 live
agents — so the addresses users and agents actually see are:

```
mini-lab/_d4t5cs:ai-chat4-ai-chat-story-3a
mini-lab/_5wndni:purdex1-purdex-69
air/_n1es4k:bb2-barbox-0b
```

The two readable identities in that string — `ai-chat4` (the tmux session)
and `ai-chat-story-3a` (the Claude Code name) — are both in the **suffix**,
which v2 §3.1 defines as display-only and which the resolver discards. The
documented advice is that the suffix is optional: "打不打都一樣". So the
address that must be typed is exactly the unreadable half, and the readable
half is marked disposable. v2's benefit arrives only after a user names a
session; its cost — an opaque primary key — is paid unconditionally from the
first minute.

v2 §1 rejected tmux session names as *the* address, and that judgement
stands: a tmux name is not an identity the user controls when they need one,
which is why `pdx msg name` exists and stays the recommended path. But the
comparison that matters for an **unnamed** session is not "tmux name vs. user
label", it is "tmux name vs. `_d4t5cs`", and there the tmux name wins on
every axis a reader cares about.

## 2. Change

A live Claude Code conversation's default label is the **sanitized name of
the tmux session it is running in**, when that name is unambiguous. Anything
else keeps the v2 hash form, which becomes the fallback rather than the rule:

```
mini-lab/purdex1:purdex1-purdex-69      unnamed, tmux session "purdex1"
mini-lab/_5wndni:purdex1-purdex-69      unnamed, name unusable or ambiguous
mini-lab/purdex-tester:purdex1-purdex-69   named by the user (unchanged)
```

### 2.1 What a default label means

v2 gave default and user labels the same meaning (an identity for one
conversation) in two namespaces. This spec separates them:

| | addresses | changes when |
|---|---|---|
| **user label** | a conversation | only the user changes it |
| **default label** | a place — "the one live agent in that tmux session" | the tmux session is renamed, or the place stops being unambiguous |

This is not a new semantic: it is exactly what the `tmux:<name>` fallback
(v2 §3.2) has always meant, and tier 2 already resolves a bare tmux name the
same way. The change makes the default label say out loud what the fallback
already does, instead of minting a second, opaque identity for it.

Two consequences to state plainly: a default label is **not stable across a
tmux rename**, and a default label never names one of several agents sharing
a tmux session (§3.3 rule 2). `pdx peers` marks it with `*` /
`label_source: "default"` exactly as before. Callers that need an address
that survives a rename, or that must name a specific agent among several in
one session, claim one with `pdx msg name` — the unchanged v2 answer.

### 2.2 The invariant that must not break

v2 §3.1 guaranteed "a default label can never be claimed, never collides
with a user label" structurally, via the `_` prefix. A tmux-derived default
lives inside the user label charset, so that guarantee has to be restored by
a rule instead of by the alphabet:

> **A tmux-derived default label exists only while it names exactly one
> live conversation and nothing else.** The instant its name is also a live
> user label, or two live conversations would derive it, every conversation
> that would have used it falls back to its v2 hash label.

The user label therefore always wins, and tier 1 never gains an ambiguity —
or a hit it did not have before (§4 walks the cases).

`pdx msg name` needs no new refusal: claiming the name of your own tmux
session is the natural thing to do and is allowed — the default it displaces
is your own.

## 3. Derivation

### 3.1 Sanitizing a tmux session name

`SanitizeLabel(name) (label string, ok bool)`, a pure function:

1. fold `A-Z` to `a-z`;
2. replace every byte outside `[a-z0-9-]` with `-` (a multi-byte rune
   becomes one `-` per byte, then step 3 collapses it);
3. collapse every run of `-` to a single `-`;
4. trim leading and trailing `-`;
5. truncate to 32 bytes, then trim a trailing `-` again;
6. `ok` is false when the result is shorter than 2 bytes, or is `cc` or
   `tmux` (v2 §3.1 reserved words).

The output, when `ok`, satisfies the user label rule
`^[a-z0-9][a-z0-9-]{1,31}$` by construction — the same regexp validates it in
tests rather than the construction being trusted. Because every byte outside
the ASCII label charset is replaced *before* truncation, step 5 can never cut
a multi-byte rune in half.

Examples: `purdex1` → `purdex1`; `AI-Chat4` → `ai-chat4`; `my_proj.2` →
`my-proj-2`; `a` → not ok (too short); `專案` → not ok (collapses to empty);
`tmux` → not ok (reserved).

Sanitizing is lossy: `my_proj.2` and `my proj 2` both become `my-proj-2`.
That is a collision between two *different* tmux sessions, and §3.3 rule 2
resolves it the same way it resolves any other — both fall back to their hash
labels. No special case is needed, but the case is tested (§6).

### 3.2 The population

Both the inventory build and the self endpoints decide default labels over
**exactly one population: the live, non-proxy Claude Code registry entries**
of this host. Nothing else participates — not tmux sessions without a live
agent, not owner-fallback rows (`inbox_dead` / `ambiguous`), not label rows
whose session is not live.

This is a correctness requirement, not an optimization. `whoami` answers from
the registry and the label store alone (v2 §3.6) and must render the identical
address the listing renders; a rule that consumed the tmux inventory (which
`whoami` does not read) could make the two disagree about the caller's own
address.

Consequences, stated so they are not discovered later:

- an `inbox_dead` session row keeps the v2 hash label: its owner's session
  has *no* live entry at all, so it is not in the population. The row is
  inert for resolution either way (v2 §3.2), and its tmux name is already in
  its own `session_name` column.
- an `ambiguous` session row does **not** get a special case. Its owner's
  session does have live entries — that is precisely why it is ambiguous —
  so it is in the population and it renders whatever the population decides,
  exactly like the entry rows of that same conversation. Forcing a hash here
  would be worse than the ambiguity it tried to avoid: one conversation would
  show two different labels in one listing, and `whoami` (which has only the
  registry, and sees the same live entries) would disagree with the session
  row about the caller's own address. The row stays inert for resolution
  because `hasLiveEntry` rejects `pid: 0`, not because of its label.
- a row with no cc agent at all keeps `label: ""`, unchanged.

### 3.3 The rule

Input: the population (each entry contributes `sessionId` and the tmux
session name from its own registry `tmux` field, `""` outside tmux), and the
label rows, restricted to sessions in the population.

For each `sessionId` in the population, its default label is
`"_" + enc(sessionId)` (v2 §3.1, unchanged) unless **all** of the following
hold, in which case it is `candidate`:

1. every live entry of that `sessionId` reports the same tmux session name,
   and `SanitizeLabel` accepts it as `candidate`. (A conversation with live
   processes in two different tmux sessions has no single place, so it gets
   no place address.)
2. no **other** `sessionId` in the population derives the same `candidate` —
   whether or not that other session carries a user label.
3. no **other** `sessionId` in the population holds the user label
   `candidate`.

Rule 3 is what enforces §2.2's "the user label always wins". It says "other"
because a session's own user label must not block its own default: the two
belong to the same row and can never disagree about where a message goes.
Without "other", `pdx msg name --release` from an agent in tmux `purdex1`
that had claimed `purdex1` would read its own about-to-be-released label as a
competitor and hand back a hash.

Rule 2 counts user-labelled sessions as competitors even though they will
never display the candidate themselves, and that is deliberate. If a
user-labelled session were exempt, then with agents A (labelled `foo`) and B
(unnamed) both in tmux `purdex1`, B alone would derive `purdex1` — and
`host/purdex1` would resolve at tier 1 to **B**, while before this change it
fell through to tier 2 and reached the session's **owner**, which may be A.
That silently re-points an address at a different agent, which §2.1's "a
default label addresses a place" must never do. With rule 2 as written, A and
B both fall back, tier 1 misses, and tier 2 answers exactly as it does today.

Rule 2 therefore makes two agents sharing one tmux session both fall back:
they are two conversations in one place, the place address cannot name
either, and both revert to precisely the behaviour that ships today while
`tmux:<name>` continues to address the session's owner.

The rule is a pure function of the population plus the label rows, so it is
deterministic and testable in isolation; `enc` and its golden vectors are
untouched.

### 3.4 Where it is applied

A single exported helper computes the whole map, and **every** path that
renders a label calls it — no path may call `DefaultLabel(sid)` directly any
more:

```go
// internal/peers
func ResolveDefaultLabels(entries []Entry, proxyPIDs map[int]bool,
    labels map[string]LabelInfo) map[string]string   // sessionId -> default label
```

| Caller | File | How |
|---|---|---|
| `Build` | `internal/peers/record.go` | computes once from `in.Entries` / `in.ProxyPIDs` / `in.Labels`, passes the map down to `applyLabel` |
| `EntryRecord` | `internal/peers/record.go` | takes the resolved default as a parameter (it no longer derives one), for `Build`'s entry rows and for the self routes alike |
| `whoami` | `internal/module/peers/labels.go` | over the `entries` `origin()` already read, with the same proxy filter and the store snapshot it already fetches |
| `claim` | `internal/module/peers/labels.go` | same, including the `holder` record in the 409 `label_taken` body |
| `release` | `internal/module/peers/labels.go` | same — a release must render the default the listing will show, not a hash. `release` reads no label snapshot today; it gains one, taken **before** the write and failing with `store_unavailable` if it errors, so the response is never a default the daemon cannot vouch for (v2 §3.6) and no write happens on a failed read |

`send` needs no change of its own: its origin record comes from
`localEnvelope` → `Build`, so it inherits the resolved default and puts it on
the wire via `rec.WireAddress()` (`internal/module/peers/send.go`).

No new I/O, no new store column, no persistence: the default is derived on
every read, exactly as the suffix is.

## 4. Resolution

`Resolve` (v2 §3.2) is **unchanged**, including tier 2. What changes is which
strings tier 1 matches:

| Address typed | Before | After |
|---|---|---|
| `purdex1`, one live agent in tmux `purdex1`, unnamed | tier 1 misses, tier 2 matches the session row | tier 1 matches that row directly — same row |
| `purdex1`, two live unnamed agents there | tier 2 → the session row (its owner) | both defaults fell back (rule 2); tier 1 misses, tier 2 → the session row — identical |
| `purdex1`, two live agents there, one user-labelled | tier 2 → the session row (its owner) | both fall back (rule 2 counts the labelled one as a competitor); tier 1 misses, tier 2 → the session row — identical |
| `purdex1`, claimed as a user label by another live agent | tier 1 → that agent | unchanged: the tmux default yielded (rule 3) |
| `purdex1`, held as a user label by a **dead** session | tier 2 → the session row | tier 1 → the live agent in tmux `purdex1`. Dead label rows are inert (v2 §3.3), so they do not block, and the row reached is the same one tier 2 would have reached |
| `_5wndni` | tier 1 matches | matches only while that hash is still the live default (§4.1) |
| `tmux:purdex1` | session row | unchanged |

`ValidateWireAddress` (`internal/peers/wire.go`) already accepts any valid
user label as a head, so a tmux-derived default passes unchanged;
`IsDefaultLabel` stays as the recogniser of the hash form only, and keeps its
current name and meaning.

### 4.1 What breaks on upgrade, and what does not

**Every unnamed agent whose tmux name sanitizes cleanly changes address the
moment the daemon restarts** — not later, when something is renamed. A
`mini-lab/_5wndni` an agent wrote down this morning stops resolving that
afternoon, with `peer_not_found` pointing at `pdx peers --all`. Nothing
mis-delivers, and nothing is silent, but the fleet's addresses do all move at
once.

This is accepted rather than mitigated. A legacy tier that kept resolving the
old hash was considered and rejected: it would give every row two live
addresses, contradict "`Resolve` unchanged", and preserve exactly the opaque
identity this change exists to retire. Defaults are documented as unstable,
`pdx peers --all` is one command, and the failure is loud.

Operationally: hosts upgrade independently, and cross-host rows are passed
through with only their host prefix rewritten
(`normalizeRemoteRows`, `internal/module/peers/module.go`), so a fleet
mid-upgrade shows tmux-derived defaults for upgraded hosts and hash defaults
for the rest, in one listing, correctly. No coordinated restart is needed.

### 4.2 `label_rev` and remote helper names (accepted limit)

`label_rev` counts **user label** writes (v2 §3.3) and is unchanged here: a
default label can change — on a tmux rename, when a competitor appears or
goes away, or at this upgrade — while `label_rev` stays 0.

v2 §3.5 already accepts that a change which does not advance `rev` does not
rename a remote peer's local helper: `helperManager.ApplyAddress` ignores any
`rev <= appliedRev` (`internal/module/peers/helpers.go`). v2 scoped that
statement to suffix-only changes; this spec widens it to the head as well.
The concrete effect: a remote host that has already delivered from an agent
at rev 0 keeps showing that agent's **old** helper display name until the
agent claims a user label (which does advance `rev`) or the helper is
released. Delivery, addressing and the listing are unaffected — only a
display name on the other host goes stale.

Widening the limit rather than fixing it is deliberate: a freshness signal
for default labels means a second revision counter for a value that is
already defined as unstable. If the stale name proves to matter in practice,
the fix belongs with helper freshness as a whole, not here.

## 5. Out of scope

- The suffix, `pdx peers` column layout, and the `*` / `label_source`
  marking of default labels: unchanged. The `*` is now the *only* thing that
  distinguishes a default from a user label on screen, since the two now
  share a shape — §6 covers it.
- SPA: `rg "api/peers" spa/src electron` returns zero hits — no renderer or
  main-process code consumes peer records today, so there is nothing to
  update. (The follow-up peer-panel task consumes `GET /api/peers` as-is and
  will see whichever label the daemon renders.)
- Cross-host collisions: `<host>/` already disambiguates; two hosts may both
  have a session named `purdex1` with no interaction.
- Any change to `pdx msg name`, the claim matrix, or the label store schema.

## 6. Acceptance

1. `SanitizeLabel` unit tests cover the six rules and the examples in §3.1,
   assert every accepted output matches the user label regexp, and include
   the truncation boundary (33 bytes, and a 33-byte input whose 32nd byte is
   a hyphen) and the lossy-collision pair from §3.1.
2. `ResolveDefaultLabels` unit tests cover: the happy path; a conversation
   with two live entries in different tmux sessions; two unnamed
   conversations in one tmux session; **one unnamed and one user-labelled in
   one tmux session** (rule 2 — both fall back); a candidate equal to a live
   user label; a candidate equal to a user label held by a *dead* session (no
   yield); an entry outside tmux; a proxy entry (excluded from the
   population); a name that sanitizes to something invalid.
3. `Build` and `whoami` return the **same** address for the same live entry,
   asserted by a test that runs both paths over one fixture; `claim`'s
   `label_taken` holder record and `release`'s record are covered too.
   Specifically: an agent in tmux `purdex1` that claimed `purdex1` and then
   releases gets `purdex1` back as its **default** (rule 3's "other"), not a
   hash; and a `release` whose label-store read fails is `store_unavailable`
   with no write.
4. `Resolve` tests for every row of §4's table, including the dead-user-label
   row and both two-agent rows.
5. A test pins §4.2: a default-label head change at unchanged `label_rev`
   does not rename the helper — asserted as the documented limit, so a future
   change to it is a deliberate one.
6. `pdx peers --all` output test: a default tmux-derived label still renders
   with `*`, so it is distinguishable from a user label of the same shape.
7. Live check on this host: `pdx peers --all` shows tmux-derived labels, and
   `pdx msg send mini-lab/<tmux name> "..."` delivers to that agent.
8. Existing peers tests pass; every changed expectation is reviewed as a
   deliberate update (a hash default that is now tmux-derived), not a test
   rewrite. `internal/module/peers` and `internal/peers` both stay green.

## 7. Phases

One phase — one indivisible vertical change. It cannot be split: any commit
that converts `Build` without the self routes (or the reverse) ships a daemon
whose listing and whose `whoami` disagree about a caller's own address, which
§3.2 exists to prevent.

The surface is not small, and calling it "two call sites" would understate
it. It is: `SanitizeLabel` + `ResolveDefaultLabels` (new, pure), `applyLabel`
and `EntryRecord` (signature change), `Build`, `whoami`, `claim`, `release`,
plus the expectation updates across `record_test.go`, `label_test.go`,
`labels_test.go`, `send_test.go`, `reply_test.go` and `e2e_test.go`, plus the
§6.5/§6.6 tests, plus §8.

## 8. Docs

`CLAUDE.md` (project) currently states "`_xxxxxx` 開頭＝尚未命名（由 sessionId
導出的預設值）", which this change makes wrong: it gains the tmux-derived
default as the normal unnamed form and keeps the hash as the fallback, and it
says that a default label names a place while `pdx msg name` names a
conversation. The v2 spec is amended by reference (this file's header), not
edited in place.

## 9. Codex spec review disposition (`task-mu3w0qhs-ehc5la`)

| # | Severity | Finding | Disposition |
|---|---|---|---|
| 1 | Blocker | Rule 2 exempting user-labelled sessions lets a bare tmux name resolve to a non-owner agent | **Accepted** — §3.3 rule 2 now counts every live session as a competitor; §4 row 3 and §6.2 pin it |
| 2 | Major | §4 table missing the mixed named/unnamed case | **Accepted** — added as §4 row 3, plus the dead-user-label row |
| 3 | Major | Spec did not hard-require the self routes to use the same derived map | **Accepted** — §3.4 names `ResolveDefaultLabels` and tables every caller |
| 4 | Major | A head change at unchanged `rev` leaves remote helper names stale; v2 only accepted this for suffix-only changes | **Accepted** — §4.2 states and widens the limit, with the rationale for not fixing it here; §6.5 pins it |
| 5 | Major | Upgrade impact understated: old hash addresses break immediately, fleet-wide | **Accepted as documented**, mitigation rejected — §4.1. A legacy alias tier would re-introduce the opaque identity this change retires |
| 6 | Minor | Sanitize collisions and truncation boundaries need tests | **Accepted** — §3.1 closing paragraph, §6.1 |
| 7 | Minor | Acceptance missing the mixed case and helper freshness | **Accepted** — §6.2, §6.5 |
| 8 | Minor | "single pure-function swap plus two call sites" understates the surface | **Accepted** — §7 rewritten with the full surface |
| — | Omission | `CLAUDE.md` must change | **Accepted** — §8 |
| — | Omission | `pdx peers` `LABEL` column now shows user-label-shaped defaults | **Accepted** — §5, §6.6 |
| — | Omission | Cross-host fan-out mixes old and new defaults mid-upgrade | **Accepted** — §4.1 closing paragraph |
| — | Omission | `label_rev` semantics for default labels | **Accepted** — §4.2 |
| — | Omission | The "SPA unaffected" claim needed evidence | **Accepted** — §5 cites the zero-hit search |

## 10. Codex plan review disposition (`task-mu3waw1z-u8bm7k`)

Only the findings that changed this spec; the rest are recorded in the plan.

| # | Severity | Finding | Disposition |
|---|---|---|---|
| 1 | Blocker | §3.2 claimed `ambiguous` session rows keep the hash "because no live entry backs them" — false: an `ambiguous` row's owner *does* have live entries (`record.go`, the `consumed` and multi-candidate branches) | **Fact accepted, fix rejected.** The wrong statement was this spec's, not the plan's. Forcing a hash on `ambiguous` rows — codex's proposed fix — would make one conversation render two different labels in one listing and would put `whoami` at odds with the session row, which is exactly what §3.2 exists to prevent. §3.2 now derives both fallback kinds from the same population with no special case, and says why. §6.2 gains the case |
