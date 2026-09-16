# Spec — Default peer label from the tmux session name

Status: draft v1
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
| **default label** | a place — "whoever is the live agent in that tmux session" | the tmux session is renamed, or the place stops being unambiguous |

This is not a new semantic: it is exactly what the `tmux:<name>` fallback
(v2 §3.2) has always meant, and tier 2 already resolves a bare tmux name the
same way. The change makes the default label say out loud what the fallback
already does, instead of minting a second, opaque identity for it.

A consequence to state plainly: a default label is **not stable across a
tmux rename**, and `pdx peers` marks it with `*` / `label_source: "default"`
exactly as before. Callers that need an address that survives a rename claim
one with `pdx msg name` — the unchanged v2 answer.

### 2.2 The invariant that must not break

v2 §3.1 guaranteed "a default label can never be claimed, never collides
with a user label" structurally, via the `_` prefix. A tmux-derived default
lives inside the user label charset, so that guarantee has to be restored by
a rule instead of by the alphabet:

> **A tmux-derived default label exists only while it is unambiguous.**
> The instant its name is also a live user label, or two live conversations
> would derive it, every conversation that would have used it falls back to
> its v2 hash label.

The user label therefore always wins, and tier 1 never gains an ambiguity it
did not have before (§4 proves the cases). `pdx msg name` needs no new
refusal: claiming the name of your own tmux session is the natural thing to
do and is allowed — the default it displaces is your own.

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
tests rather than the construction being trusted.

Examples: `purdex1` → `purdex1`; `AI-Chat4` → `ai-chat4`; `my_proj.2` →
`my-proj-2`; `a` → not ok (too short); `專案` → not ok (collapses to empty);
`tmux` → not ok (reserved).

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

- a session row with no live entry (`inbox_dead`, `ambiguous`) keeps the v2
  hash label. Those rows are inert for resolution (v2 §3.2) and their tmux
  name is already in their own `session_name` column.
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
2. no other `sessionId` in the population, **which is not itself
   user-labelled**, derives the same `candidate`. A user-labelled session
   does not compete for a place address it is not using.
3. no `sessionId` in the population holds the user label `candidate`.

Rule 3 is what enforces §2.2. Rule 2 makes two agents sharing one tmux
session both fall back — deliberately: they are two conversations in one
place, the place address cannot name either, and both revert to exactly the
behaviour that ships today while `tmux:<name>` continues to address the
session's owner.

The rule is a pure function of the population plus the label rows, so it is
deterministic and testable in isolation; `enc` and its golden vectors are
untouched.

### 3.4 Where it is applied

- `Build` (`internal/peers/record.go`): computes the map once from
  `in.Entries` + `in.Labels` before joining, then `applyLabel` consumes the
  resolved default instead of calling `DefaultLabel(sid)`.
- `EntryRecord`, used both by `Build`'s entry rows and by
  `whoami`/`claim`/`release` (`internal/module/peers/labels.go`): takes the
  same resolved default. `whoami` computes it over the entries it already
  reads in `origin()`, filtered by the same proxy rule.
- No new I/O, no new store column, no persistence: the default is derived on
  every read, exactly as the suffix is.

## 4. Resolution

`Resolve` (v2 §3.2) is **unchanged**, including tier 2. What changes is which
strings tier 1 matches. The cases:

| Address typed | Before | After |
|---|---|---|
| `purdex1`, one live agent in tmux `purdex1` | tier 1 misses, tier 2 matches the session row | tier 1 matches that row directly |
| `purdex1`, two live agents there | tier 2 matches the session row (its owner) | both defaults fell back (§3.3 rule 2), so tier 1 misses and tier 2 matches the session row — identical |
| `purdex1`, claimed as a user label by another agent | tier 1 matches that agent | unchanged: the tmux default yielded (rule 3) |
| `_5wndni` | tier 1 matches | still matches whenever that hash is the live default; a session that now has a tmux-derived label no longer answers to its hash |
| `tmux:purdex1` | session row | unchanged |

The one behaviour that is removed: an address recorded while a session was
unnamed (`mini-lab/_5wndni`) stops resolving once that session acquires a
tmux-derived default. Defaults were already documented as unstable, and a
`peer_not_found` naming `pdx peers --all` is the failure mode; nothing
mis-delivers.

`ValidateWireAddress` (`internal/peers/wire.go`) already accepts any valid
user label as a head, so a tmux-derived default passes unchanged;
`IsDefaultLabel` stays as the recogniser of the hash form only, and keeps its
current name and meaning.

## 5. Out of scope

- The suffix, `pdx peers` column layout, and the `*` / `label_source`
  marking of default labels: unchanged.
- SPA: does not read `label_source` today and needs no change. (The peer
  panel work in the follow-up task C consumes `GET /api/peers` as-is.)
- Cross-host collisions: `<host>/` already disambiguates; two hosts may both
  have a session named `purdex1` with no interaction.
- Any change to `pdx msg name`, the claim matrix, or the label store schema.

## 6. Acceptance

1. `SanitizeLabel` unit tests cover the six rules and the examples in §3.1,
   and assert every accepted output matches the user label regexp.
2. Default-label resolution unit tests cover: the happy path; a conversation
   with two live entries in different tmux sessions; two conversations in one
   tmux session; a candidate equal to a live user label; a candidate equal to
   a user label held by a *dead* session (no yield — dead rows are inert); an
   entry outside tmux; a name that sanitizes to something invalid.
3. `Build` and `whoami` return the **same** address for the same live entry,
   asserted by a test that runs both paths over one fixture.
4. `Resolve` tests for each row of §4's table.
5. `pdx peers --all` on this host shows tmux-derived labels for live agents,
   with `*` still marking them as defaults, and `pdx msg send` to one of them
   delivers.
6. Existing peers tests pass unchanged except where they assert a hash
   default that is now a tmux-derived one; each such change is reviewed as a
   deliberate expectation update, not a test rewrite.

## 7. Phases

One phase. The change is a single pure-function swap plus its two call sites;
splitting it would produce a commit in which the two paths disagree about an
address, which §3.2 exists to prevent.

## 8. Docs

`CLAUDE.md` (project) — the peer address section states
"`_xxxxxx` 開頭＝尚未命名"; it gains the tmux-derived default and keeps the
hash form as the fallback. The v2 spec is amended by reference (this file's
header), not edited in place.
