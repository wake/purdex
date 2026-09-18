# Peer Pairing D4 — SPA pair / unpair / rotate from the page: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the operator pair two App hosts, unpair a row, and rotate either direction's inbound token from the Peers sub-page, with the page never deciding Commit/Cancel from its own memory (spec D-7, §7.3) and never holding a token value outside one flow's component state (D-8).

**Architecture:** D4 adds to D2's three layers and does not rewrite them. `host-api.ts` gains five wrappers over the D0/D3 routes (no logic). `lib/peer-pairing.ts` gains one pure rule, `rotationOffer`. `lib/peer-pairing-load.ts` gains two things the page needs before it can act: the list of pair candidates (available App hosts with no entry on X, each with whether *they* already hold an entry for X — the repair case), and a **post-dial re-read** of any row whose entry has `rotation_pending` (spec §7.3: "re-verifies the return path, then re-reads the row" — the initial list is pre-dial). A new `lib/peer-pairing-actions.ts` holds the three flows (`pairHosts`, `rotateDirection`, `unpairHosts`) as async functions over an injected API that report each step through a callback, testable without React. `PeersSection.tsx` wires the buttons, the confirm dialog, the inline alias prompt and the step display.

**Split into two stacked PRs** (spec D-9 allows it; D2 was split the same way): **D4a** = Tasks 1–4 (lib layer), **D4b** = Tasks 5–8 (page, i18n, mutation record). D4b's branch is cut at D4a's last commit and its PR is based on D4a's branch.

**Tech Stack:** React 19, Zustand 5 (read-only), Vitest + `@testing-library/react`, Phosphor Icons, flat-key i18n.

**Spec:** `docs/specs/2026-09-18-peer-pairing-ui-spec.md` — §3 D-7/D-8/D-9, §6.3 (routes), §6.4 (the flow + failure table), §7 (this phase), §8.4 (tests + mutation), §9 D4 (real-machine acceptance).

## Global Constraints

- Worktree root: `/Users/wake/Workspace/wake/purdex/.claude/worktrees/peer-pairing-d4`. Prefix every Bash call with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/peer-pairing-d4 && `.
- Commit with `git commit --only <files>` naming each file; never `-A`, never `-am`. The repo root has a version-controlled `pdx` binary.
- SPA: pnpm. Tests `cd spa && npx vitest run <path>`; lint `cd spa && pnpm run lint`; build `cd spa && pnpm run build`. Baseline at `fc39dcc5` (alpha.392): 6870 tests green.
- **D-8, enforced:** a token value (`inbound_token` from POST 201 or rotate) lives in a local variable of the flow that consumes it and nowhere else. It is never put in a Zustand store, `localStorage`, a `PairingSnapshot`, a flow *outcome* object, a step message, an error message, a `data-*` attribute, or a log. §8.4's test asserts `JSON.stringify(useHostStore.getState())` and every `localStorage` value contain no `pdxp_` after each flow, and the actions tests assert the same over every outcome and step reported.
- **D-7, enforced:** the page never sends `force`. `commitRotation`/`cancelRotation` send **no body** (the daemon treats an empty body as `{force:false}`, `hosts_rotate.go:60`). §8.4's test asserts no request body seen by the mock contains `force`.
- **§7.3, enforced:** Commit/Cancel are offered only from a row read **after** the evidence dial. `rotationOffer` is a pure function of the row; the *freshness* of the row is a fact the loader records (`PairingRow.gateStale`) and the component obeys. The mutation in Task 7 removes this and must turn a test red.
- `outbound-only` vs `return-unknown` (D2 rule 2) is untouched; Rotate/Pair need an available App counterpart; a CLI-started pending rotation on a row without one still gets Commit/Cancel by the same rule with the "as of the peer's last dial" note.
- **Wording:** after a *commit*, a note for the old token derives to `''`, not `'prev'` (D3 record design). No string in this phase says or implies "prev after commit".
- `MemoryMonitorDisabled.test.tsx` mocks all of host-api without `importOriginal`: nothing new may touch a host-api export at module load. `PeersSection` keeps building its api tables inside handlers.
- Every daemon-supplied string is rendered as text only.
- Commit messages: one task = one commit, ending with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.

## Facts the plan relies on (measured at `fc39dcc5`)

- `hostRow` now has `rotation_pending: bool` and `last_inbound_auth: "" | "current" | "prev"` (`internal/module/peers/hosts.go:45–46`). The SPA `PeerHostRow` (`spa/src/lib/host-api.ts:132`) does not yet declare them. A daemon < alpha.391 omits both keys → the wrapper normalises `undefined` to `false` / `''` (no rotation, no evidence — the same fact).
- `POST /api/peers/hosts` body `{alias?, url, token?}` → 201 `{alias, url, host_id, inbound_token, verified}` (`hosts.go:64–82`); 400 (alias invalid / url invalid / "no alias given and the peer published none" / "cannot pair a host with itself"), 409 (`alias %q is already used by another host; …` or `alias changed concurrently`), 502 (verify failed, message = transport/auth error). **Order inside the handler: the verify dial runs before the alias-uniqueness check**, so a 409 on X's POST means X *did* dial Y with `tY` (Y's record for its new entry reads `current`).
- `DELETE /api/peers/hosts/{alias}` → 204; 404 `{error:"unknown alias"}` (`hosts.go:563`).
- `POST …/{alias}/rotate` → 200 `{alias, inbound_token}`; 409 `rotation already pending`; 404 (`hosts_rotate.go:81–122`). `rotate` clears the in-memory record: the row reads `''` right after.
- `POST …/rotate/commit` / `…/rotate/cancel`: body optional; empty body = no force; → 200 `hostRow`; commit 409 `rotation unconfirmed` unless `last_inbound_auth == "current"` (no-op 200 when nothing pending); cancel 409 `no rotation pending` / `rotation unconfirmed` unless `"prev"` (`hosts_rotate.go:125–210`).
- `PUT …/{alias} {token}` on Y verifies Y→X with that token before storing (`hosts.go:420–560`); 502 on a failed verify, 409 `entry changed concurrently` / `host_id mismatch`.
- Which entry a token lives on: the token X presents to Y is Y's entry-for-X `inbound_token`; the token Y presents to X is X's entry-for-Y `inbound_token`. So **rotating the X→Y direction = `rotate` on Y's entry, push to X's entry; rotating Y→X = `rotate` on X's entry, push to Y's entry**. The evidence dial for a rotation on X's entry is Y→X = `verifyPeerHost(Y, E'.alias)`; for a rotation on Y's entry it is X→Y = `verifyPeerHost(X, E.alias)`.
- `loadPairings` (`spa/src/lib/peer-pairing-load.ts`) lists X (step 1) and counterparts (step 3) **before** the verify dials (step 4): every `rotation_pending`/`last_inbound_auth` in a D2 snapshot is pre-dial.
- `ConfirmDialog` (`spa/src/components/ConfirmDialog.tsx`): `{testIdPrefix, title, body, confirmLabel, busy?, onCancel, onConfirm}`, test ids `${prefix}-dialog|-cancel|-confirm`, uses `common.cancel`.
- Tokens are `pdxp_` + 32 hex (`internal/config/config.go:94`). Test fixtures use that prefix so a leak is greppable.
- i18n flat keys in `spa/src/locales/en.json` + `zh-TW.json`; `locale-completeness.test.ts` fails on a one-sided key.

## File map

| file | D4 responsibility |
|---|---|
| `spa/src/lib/host-api.ts` | `PeerHostRow` +2 fields; `PeerHostAdded`; `addPeerHost`, `deletePeerHost`, `rotatePeerHost`, `commitRotation`, `cancelRotation` |
| `spa/src/lib/host-api.peers.test.ts` | wrapper tests (extend) |
| `spa/src/lib/peer-pairing.ts` | `rotationOffer` + `RotationOffer` type |
| `spa/src/lib/peer-pairing.test.ts` | its table |
| `spa/src/lib/peer-pairing-load.ts` | `candidates` in the snapshot; step 5 post-dial re-read; `gateStale` |
| `spa/src/lib/peer-pairing-load.test.ts` | candidates, repair detection, re-read order and failure |
| `spa/src/lib/peer-pairing-actions.ts` (new) | `pairHosts`, `rotateDirection`, `unpairHosts` over `ActionApi`, reporting steps |
| `spa/src/lib/peer-pairing-actions.test.ts` (new) | §8.4 flow tests incl. the "push's verify passed but Y failed to persist" fixture |
| `spa/src/components/hosts/PeersSection.tsx` | Pair section, Unpair + dialog, Rotate / Commit / Cancel / Retry return path / Create return entry, step display, alias prompt |
| `spa/src/components/hosts/PeersSection.test.tsx` | §8.4 component tests incl. store/localStorage/force asserts |
| `spa/src/locales/en.json`, `zh-TW.json` | new `peers.*` keys |
| `docs/plans/2026-09-18-peer-pairing-d4-mutations.md` (new) | mutation record |

---

# PR D4a — lib layer

### Task 1: `host-api.ts` — the five D0/D3 wrappers and the two new row fields

**Files:** modify `spa/src/lib/host-api.ts`; extend `spa/src/lib/host-api.peers.test.ts`.

**Interfaces produced:**

```ts
export interface PeerHostRow {
  alias: string; url: string; host_id: string
  verified: boolean; has_token: boolean; has_inbound_token: boolean; allow_bypass: boolean
  rotation_pending: boolean                       // inbound_token_prev is set (spec §6.1)
  last_inbound_auth: '' | 'current' | 'prev'      // which token the peer LAST presented, derived at read time (§6.2)
}
/** POST 201: the first of the two responses that carry a live token value (D-8). */
export interface PeerHostAdded { alias: string; url: string; host_id: string; inbound_token: string; verified: boolean }
export function addPeerHost(hostId: string, body: { alias?: string; url: string; token?: string }): Promise<PeerHostAdded>
export function deletePeerHost(hostId: string, alias: string): Promise<void>          // 204; 404 throws HostApiError(404)
export function rotatePeerHost(hostId: string, alias: string): Promise<{ alias: string; inbound_token: string }>
export function commitRotation(hostId: string, alias: string): Promise<PeerHostRow>   // POST, NO body
export function cancelRotation(hostId: string, alias: string): Promise<PeerHostRow>   // POST, NO body
```

- [ ] **Step 1: failing tests** (append to `host-api.peers.test.ts`, same `fetchMock`/`jsonResponse` helpers; `ROW` gains `rotation_pending: false, last_inbound_auth: ''`):
  - `listPeerHosts` normalises a pre-391 row: body `{hosts:[{…ROW without the two keys}]}` → `rotation_pending === false`, `last_inbound_auth === ''`; a 391 row `{rotation_pending:true,last_inbound_auth:'prev'}` passes through unchanged.
  - `addPeerHost` POSTs `/api/peers/hosts` with `Content-Type: application/json`, body exactly the fields given (`{url, token}` → no `alias` key; `{alias, url}` → no `token` key), returns the 201 body verbatim; 409 → `HostApiError{status:409, detail:'alias "mini-lab" is already used by another host; pass an explicit alias for this one'}`; 502 → `HostApiError{status:502, detail}`.
  - `deletePeerHost` sends `DELETE /api/peers/hosts/<encoded alias>` and resolves `undefined` on 204 (`new Response(null,{status:204})`); 404 rejects `HostApiError{status:404, detail:'unknown alias'}` (the caller decides that 404 = done; the wrapper does not hide it).
  - `rotatePeerHost` POSTs `…/<alias>/rotate` with no body (`init.body === undefined`) and returns `{alias, inbound_token}`; 409 `rotation already pending` surfaces in `detail`.
  - `commitRotation` / `cancelRotation` POST `…/rotate/commit` / `…/rotate/cancel` with **`init.body === undefined`** and **no `Content-Type` header**, return the row; 409 `rotation unconfirmed` surfaces in `detail`.
  - "no wrapper ever sends `force`": run all five once and assert no `init.body` string contains `force`.
- [ ] **Step 2:** run `cd spa && npx vitest run src/lib/host-api.peers.test.ts` → FAIL (not exported / type errors).
- [ ] **Step 3: implement.** Extend `PeerHostRow` (both new fields required). In `listPeerHosts`, map rows: `{ ...r, rotation_pending: r.rotation_pending === true, last_inbound_auth: r.last_inbound_auth === 'current' || r.last_inbound_auth === 'prev' ? r.last_inbound_auth : '' }` with a comment naming the pre-391 daemon. Add the five wrappers next to `updatePeerHost`, each through `peerHostJson`; `deletePeerHost` checks `res.ok` and returns (no JSON on 204). Update the doc comment on `updatePeerHost` ("`token` is here for D4" → "D4 passes `token` for the push step"). Update every existing `PeerHostRow` fixture in the repo that now fails `tsc` (`peer-pairing-load.test.ts`, `PeersSection.test.tsx`, `host-api.peers.test.ts`) by adding the two fields — no other change to those files in this task.
- [ ] **Step 4:** tests green; `cd spa && pnpm run lint` clean; `npx tsc --noEmit -p spa/tsconfig.app.json` (or `pnpm run build`) clean.
- [ ] **Step 5:** commit `--only` the files: `feat(peers): host-api wrappers for add/delete/rotate/commit/cancel; rotation fields on PeerHostRow`.

### Task 2: `peer-pairing.ts` — `rotationOffer`

**Files:** modify `spa/src/lib/peer-pairing.ts`, `spa/src/lib/peer-pairing.test.ts`.

```ts
export type RotationOffer = 'commit' | 'cancel' | 'none'
/** Spec §7.3, exactly one of the three, from the ROW ALONE. null = no rotation pending. */
export function rotationOffer(row: { rotation_pending: boolean; last_inbound_auth: '' | 'current' | 'prev' }): RotationOffer | null
```

- [ ] **Step 1: failing tests** (table): `{false, ''}`→`null`; `{false,'current'}`→`null` (a note for the current token with nothing pending — after a commit — is not a rotation); `{true,'current'}`→`'commit'`; `{true,'prev'}`→`'cancel'`; `{true,''}`→`'none'`. Plus one assertion that the function has no other input (`'commit'` and `'cancel'` are never both offered — trivially true by type, keep the table).
- [ ] **Step 2:** red. **Step 3:** implement (five lines). **Step 4:** green, lint. **Step 5:** commit `feat(peers): rotationOffer — the §7.3 rule as a pure function of the row`.

### Task 3: `peer-pairing-load.ts` — pair candidates and the post-dial re-read

**Files:** modify `spa/src/lib/peer-pairing-load.ts`, `spa/src/lib/peer-pairing-load.test.ts`.

**Interfaces produced (additive):**

```ts
export interface PairCandidate {
  hostId: string; name: string; url: string; host_id: string
  /** Y's existing entry for X when Y already holds one (the one-way repair case, spec §7.1 last para); null otherwise. */
  returnEntry: PeerHostRow | null
  /** Why Y's entries could not be read this run; '' when they could. Non-empty ⇒ the page must not offer Pair (codex F4: a Y that already holds an entry for X must go through the repair path, and an unread Y might). */
  listError: string
}
export interface PairingRow {
  … (unchanged fields) …
  /**
   * §7.3: Commit/Cancel may only be offered from a row read AFTER the evidence dial.
   * Per side (codex F5 — both entries can be pending in one row and are re-read
   * from different hosts): `entry` = X's row, `returnEntry` = Y's row. true while a
   * rotation is pending on that side and this run has NOT yet re-read it after its
   * dial (pre-dial emits), or the re-read failed / the alias vanished. false when
   * nothing is pending on that side or the post-dial re-read landed.
   */
  gateStale: { entry: boolean; returnEntry: boolean }
}
export interface PairingSnapshot { self; error; rows; candidates: PairCandidate[] }
```

Behaviour:
- **Candidates** (after step 3): every `Meta` with `available: true` whose `hostId` is not the `counterpart.hostId` of any row → `listOf(hostId)` (the same memo as step 3; a candidate is listed once) → success: `returnEntry = matchReturnEntry({host_id: self.host_id, url: x.url}, theirs)`, `listError: ''`; failure: `returnEntry: null`, `listError: <msg>`. Unavailable hosts are not candidates (§7.1: "a host whose info could not be fetched is not listed"). Candidates are in the pre-dial emit and every later emit.
- **Step 5, post-dial re-read:** after `await Promise.all(dials)`, collect the hosts to re-list: X when any `row.entry.rotation_pending`; each `row.counterpart.hostId` when `row.returnEntry?.rotation_pending`. For each such host call `api.list(hostId)` **once** (a fresh call, not `listOf`'s memo — the memo is the pre-dial read). On success replace `row.entry` / `row.returnEntry` with the row of the same alias from the fresh list and set that side's `gateStale` false (alias gone → keep the old row, side stays `true`). On failure that side stays `true` on every row that needed that host. Emit once at the end. Sides with nothing pending are `false` and cause no extra call. **The re-read happens after `Promise.all(dials)`, never before or concurrently** — the test pins the order.
- The pre-dial emit and every settle emit carry `gateStale.entry = entry.rotation_pending`, `gateStale.returnEntry = !!returnEntry?.rotation_pending` — a pending side is stale by definition until step 5 has re-read it, so the component never offers a button from a pre-dial row. Name this in a comment.
- Candidates are **not** re-read in step 5 (X cannot dial a host it has no entry for; codex F3's candidate controls read "as of the peer's last dial" and the daemon gate is the backstop).

- [ ] **Step 1: failing tests** (extend the file's fake-API harness; fixtures already carry the two fields):
  - "candidates: an available host with no entry on X is listed with `returnEntry: null`, `listError: ''` and cost exactly one extra `list` call for it" (X has zero entries; others = [A connected] → `candidates` = `[{hostId:A, …}]`, `list` called for X and A once each).
  - "candidates: the repair case — Y already holds an entry for X → `returnEntry` is that row".
  - "candidates: a host that is some row's counterpart is not a candidate" (the D2 fixture → `candidates` empty).
  - "candidates: an unavailable host is not a candidate" (A disconnected → empty; `list` never called for A).
  - "candidates: a failing `list(Y)` lists Y with `returnEntry: null` and `listError` = the message".
  - "post-dial re-read: a pending rotation on X's entry re-lists X after BOTH dials settle, and the row shows the fresh `last_inbound_auth`" — **stateful fake** (this is the §8.4 fixture, reused by Task 6): `list(X)` returns `{…AIR_ROW, rotation_pending:true, last_inbound_auth:'current'}` until `verify(A,'mini-lab')` has been called, then `{…, 'prev'}`; record the call sequence (`['list:X','list:A','verify:X:air','verify:A:mini-lab','list:X']`) and assert the second `list:X` comes after both verifies; final row `entry.last_inbound_auth === 'prev'`, `gateStale.entry === false`; the pre-dial emit's row has `gateStale.entry === true`. **Mutation M-A (read before the dial) makes this row read `'current'` and the test red.**
  - "post-dial re-read: pending on Y's entry re-lists Y, not X" (exactly one `list:X`, two `list:A`; `gateStale.returnEntry` ends false).
  - "both sides pending, only Y's re-list fails → `gateStale` is `{entry:false, returnEntry:true}` and X's side shows the fresh value" (codex F5).
  - "the second `list` failing leaves that side `true` and keeps the pre-dial row".
  - "no pending rotation anywhere → no extra list call, both sides false everywhere" (D2 fixture: `list` call count unchanged from D2's test).
  - "the alias vanished between the two lists → old row kept, side stays `true`".
- [ ] **Step 2:** red. **Step 3:** implement. **Step 4:** green; `npx vitest run src/components/hosts/PeersSection.test.tsx` still green (`candidates` is empty in the D2 fixture). Lint.
- [ ] **Step 5:** commit `feat(peers): loadPairings lists pair candidates and re-reads pending rows after the dials (§7.3)`.

### Task 4: `peer-pairing-actions.ts` — the three flows

**Files:** new `spa/src/lib/peer-pairing-actions.ts`, `spa/src/lib/peer-pairing-actions.test.ts`.

**Design after codex F1:** no flow ever calls `commit`. A rotation from the page is *mint → push*, then the page refreshes (§7.4), and the refresh's post-dial re-read (Task 3 step 5) is what offers Commit / Cancel / neither; the operator clicks Commit. `commitRotation`/`cancelRotation` are called only from the button handlers in Task 6. This is §6.4's "Commit offered and accepted" and §7.3's "offers exactly one of" read literally.

**Interfaces produced:**

```ts
export interface ActionApi {
  add: (hostId: string, body: { alias?: string; url: string; token?: string }) => Promise<PeerHostAdded>
  update: (hostId: string, alias: string, patch: { alias?: string; token?: string }) => Promise<PeerHostRow>
  delete: (hostId: string, alias: string) => Promise<void>
  rotate: (hostId: string, alias: string) => Promise<{ alias: string; inbound_token: string }>
}
/** Every step a flow reports. Never carries a token. */
export type FlowStep =
  | 'mint' | 'push'                                                          // rotateDirection
  | 'create-on-y' | 'rotate-on-y' | 'create-on-x' | 'push-to-y' | 'undo-on-y' // pairHosts
  | 'delete-x' | 'delete-y'                                                  // unpairHosts
export type Report = (step: FlowStep) => void
export interface Ref { hostId: string; alias: string }

/**
 * Spec §6.4 steps 1–2 with the roles named: `holder` is the entry whose inbound
 * token is rotated; `push(token)` stores the new token on the presenter (a PUT on
 * its existing entry, or — "Create return entry" — a POST that creates it). What
 * the peer then presents, and therefore which of Commit/Cancel is safe, is NOT
 * this function's business: the caller refreshes and reads the row (§7.3).
 */
export function rotateDirection(holder: Ref, push: (token: string) => Promise<void>, api: ActionApi, report: Report): Promise<RotateOutcome>
export type RotateOutcome =
  | { kind: 'pushed' }
  | { kind: 'push-failed'; error: string }     // holder accepts both tokens; the refresh decides
  | { kind: 'rotate-failed'; error: string }   // nothing changed

export function pairHosts(x: { hostId: string; url: string; selfAlias: string }, y: { hostId: string; url: string; returnEntry: PeerHostRow | null }, aliases: { onY?: string; onX?: string }, api: ActionApi, report: Report): Promise<PairOutcome>
export type PairOutcome =
  | { kind: 'paired'; aliasOnX: string; aliasOnY: string }                       // on the repair path Y's rotation is still pending; the refresh offers Commit
  | { kind: 'alias-conflict'; side: 'x' | 'y'; error: string }                  // nothing left behind on the non-repair path
  | { kind: 'step-failed'; step: 'create-on-y' | 'rotate-on-y' | 'create-on-x'; error: string; undoError: string }
  | { kind: 'return-failed'; aliasOnX: string; aliasOnY: string; error: string }   // both entries exist → one-way

export function unpairHosts(x: Ref, y: Ref | null, api: ActionApi, report: Report): Promise<UnpairOutcome>
export type UnpairOutcome = { xError: string; yError: string }   // '' = deleted or was already gone (404)
```

Behaviour, exactly:

**`rotateDirection`:** `report('mint')`; `rotate(holder)` → `tok` (throw → `rotate-failed`). `report('push')`; `await push(tok)` (throw → `push-failed` with the message). → `pushed`. `tok` is a local `const` and appears nowhere else.

**`pairHosts`** (spec §7.1):
1. If `y.returnEntry` (repair): `report('rotate-on-y')`; `rotate(y.hostId, y.returnEntry.alias)` → `tY`, `aliasOnY = y.returnEntry.alias`; throw → `step-failed / rotate-on-y`.
   Else: `report('create-on-y')`; `add(y.hostId, {alias: aliases.onY ?? x.selfAlias, url: x.url})` → `tY`, `aliasOnY = res.alias`; 409 → `alias-conflict / y`; other throw → `step-failed / create-on-y`.
2. `report('create-on-x')`; `add(x.hostId, {url: y.url, token: tY, ...(aliases.onX ? {alias: aliases.onX} : {})})` → `tX`, `aliasOnX = res.alias`. On any throw: non-repair → `report('undo-on-y')`; `delete(y.hostId, aliasOnY)` (404 fine; other error → `undoError`); then 409 → `alias-conflict / x` (Y's entry was removed so the retry re-runs step 1 cleanly — the undo is unconditional on the non-repair path); other → `step-failed / create-on-x`. Repair → no undo possible without `force` → `step-failed / create-on-x`, `undoError: ''`; Y's rotation stays pending and the page's candidate line offers Commit/Cancel by the rule (Task 6, codex F3).
3. `report('push-to-y')`; `update(y.hostId, aliasOnY, {token: tX})`; throw → `return-failed` (both entries exist; the page shows `one-way` with "Retry return path" = `rotateDirection(holder X/E, push = PUT on Y)`).
4. `paired`. On the repair path Y's E' is still `rotation_pending`; the refresh dials Y (outbound verify) and re-reads Y's row → Commit is offered on the outbound line by the ordinary rule.
`tY`, `tX` are local `const`s; outcomes carry aliases only.

**`unpairHosts`:** `report('delete-x')`; `delete(x)`; `HostApiError` 404 → `''`, other → `xError`. If `y`: `report('delete-y')`; same → `yError`. Both attempted regardless of the first's result (§7.2).

- [ ] **Step 1: failing tests** — a `fake()` harness building an `ActionApi` from `vi.fn`s and a `calls: string[]` log (`'rotate:X:air'`, `'update:A:mini-lab:token=same'` — log **whether** a token was passed and whether it equals the one the fake minted, never the value); tokens `pdxp_` + 32 hex; a `steps: FlowStep[]` collector. After **every** test: `expect(JSON.stringify({outcome, steps, calls})).not.toMatch(/pdxp_/)`.
  - rotateDirection happy: `rotate:X:air` then push (fake `update:A:mini-lab:token=same`); outcome `pushed`; steps `['mint','push']`; the `ActionApi` fake has **no** `commit`/`cancel` member and the flow compiles — the type forbids a flow from committing.
  - push throws (`HostApiError(409,'Conflict','entry changed concurrently')`) → `push-failed` with that detail; `rotate` called once; nothing else.
  - `rotate` 409 `rotation already pending` → `rotate-failed`; push never called.
  - pairHosts happy: `add:A {alias:'mini-lab', url:X url}` (no token key), `add:X {url:A url, token=same-as-A's inbound_token}` (no alias key), `update:A:mini-lab token=same-as-X's inbound_token`; `paired`; steps in order.
  - `aliases.onX` → X's add carries `alias`; `aliases.onY` → Y's add uses it instead of `selfAlias`.
  - step 1 409 → `alias-conflict / y`, nothing else called.
  - step 2 502 → `delete:A:mini-lab`, `step-failed / create-on-x`, `undoError:''`; no update on A.
  - step 2 409 → undo delete on A, then `alias-conflict / x`.
  - undo delete 404 → `undoError:''`; undo delete 500 → `undoError` set, kind still `step-failed`.
  - step 3 502 → `return-failed` with both aliases; no delete anywhere.
  - repair: `rotate:A:mini-lab` instead of add; add on X with that token; update on A; `paired`.
  - repair, step 2 fails → `step-failed / create-on-x`, no delete, `undoError:''`.
  - unpair: X 404 + Y 204 → `{xError:'', yError:''}`; X 500 → `xError` set **and Y still deleted**; `y === null` → one call.
- [ ] **Step 2:** red. **Step 3:** implement. **Step 4:** green, lint, typecheck. **Step 5:** commit `feat(peers): pair / rotate / unpair flows as pure orchestration over an injected API (spec §7.1–7.3)`.

**D4a PR checkpoint:** full `npx vitest run`, lint, build; `git diff --stat fc39dcc5..HEAD` ≤ 800 lines / 20 files. Open PR `feat(peers): D4a — lib layer for pair/unpair/rotate` with body: spec sections, the token-handling invariant, "no flow commits" (codex F1), the pre-391 normalisation note, and that `HOST_SUB_PAGES` (`host-routes.ts`) is still deliberately untouched.

---

# PR D4b — the page

### Task 5: i18n keys

**Files:** `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`.

Keys (en; zh-TW in the same order):
`peers.pair_heading` "Pair with…"; `peers.pair` "Pair"; `peers.pairing` "Pairing…"; `peers.pair_repair_note` "{{name}} already has an entry for this host — Pair will rotate its token and complete the return path"; `peers.pair_blocked_pending` "{{name}}'s entry for this host has a rotation pending — commit or cancel it first"; `peers.pair_blocked_unread` "{{name}}'s entries could not be read ({{cause}}) — refresh before pairing"; `peers.no_candidates` "Every other connected host is already paired."; `peers.alias_prompt_y` "{{name}} already uses that alias. Alias for this host on {{name}}:"; `peers.alias_prompt_x` "This host already uses that alias. Alias for {{name}} here:"; `peers.alias_retry` "Retry with this alias"; `peers.unpair` "Unpair"; `peers.unpair_title` "Unpair {{x}} and {{y}}?"; `peers.unpair_body_both` "Deletes {{x}}'s entry “{{ex}}” and {{y}}'s entry “{{ey}}”. Both directions stop working until paired again."; `peers.unpair_body_one` "Deletes {{x}}'s entry “{{ex}}”. {{y}} is not a host in this App, so its side is left as is."; `peers.unpairing` "Unpairing…"; `peers.rotate` "Rotate token"; `peers.rotate_unavailable` "Rotation needs the counterpart to be a host in this App"; `peers.retry_return` "Retry return path"; `peers.create_return` "Create return entry"; `peers.commit` "Commit"; `peers.cancel_rotation` "Cancel rotation"; `peers.rotation_pending` "rotation pending"; `peers.rotation_current` "the peer is on the new token"; `peers.rotation_prev` "the peer is still presenting the old token"; `peers.rotation_none` "the peer has not dialled since the rotation; both tokens stay valid"; `peers.rotation_as_of_last_dial` "(as of the peer's last dial)"; `peers.rotation_stale` "(could not re-read after the dial — refresh)"; `peers.step.mint` "minting…"; `peers.step.push` "pushing to the peer…"; `peers.step.create-on-y` "creating the entry on the peer…"; `peers.step.rotate-on-y` "rotating the peer's token…"; `peers.step.create-on-x` "creating the entry here…"; `peers.step.push-to-y` "pushing the return token…"; `peers.step.undo-on-y` "removing the half-made entry…"; `peers.step.delete-x` "deleting here…"; `peers.step.delete-y` "deleting on the peer…"; `peers.flow_error` "{{step}} failed: {{error}}"; `peers.flow_undo_error` "and the cleanup failed too: {{error}}"; `peers.return_failed_hint` "The return path was not stored — use Retry return path."; `peers.pushed_hint` "Token pushed — the row says whether the peer is on it."

- [ ] Add both files; the locale-completeness test green; commit `i18n(peers): D4 keys`.

### Task 6: `PeersSection.tsx` — the controls

**Files:** modify `spa/src/components/hosts/PeersSection.tsx`, `spa/src/components/hosts/PeersSection.test.tsx`. Extract `spa/src/components/hosts/peers/PairWithSection.tsx` and `spa/src/components/hosts/peers/RotationControls.tsx` if the main file passes ~450 lines (same PR).

Wiring rules (all state in `useState`, dies with the component):
- `actionApi(): ActionApi` built **inside handlers** from the host-api wrappers (never at module load).
- A single `flow: { key: string; step: FlowStep | null; error: string; hint: string } | null` state shows the current step / last error under the affected row or candidate; every flow ends with `run()` (§7.4) and then clears `step` but keeps `error`/`hint` until the next flow on that key.
- **Pair section** (below the rows, `data-testid="peers-pair"`): one line per `snap.candidates`: name, url; note `pair_repair_note` when `returnEntry`; **Pair** button (`peers-pair-<hostId>`) disabled while `busy`/a flow runs, when `listError` (`pair_blocked_unread`, codex F4), or when `returnEntry?.rotation_pending` (`pair_blocked_pending`). **Codex F3:** when `returnEntry?.rotation_pending`, the candidate line also renders `RotationControls` for holder `{Y, returnEntry.alias}` with `evidenceDialled: false` (X has no entry to dial with) — Commit/Cancel by `rotationOffer(returnEntry)` + `rotation_as_of_last_dial`; after either, refresh. Click Pair → `pairHosts({hostId, url: getDaemonBase(hostId), selfAlias: snap.self.self_alias}, {hostId: c.hostId, url: c.url, returnEntry: c.returnEntry}, aliases, api, report)`. `alias-conflict` → inline `<input data-testid="peers-alias-input-<hostId>">` + `peers-alias-retry-<hostId>` re-running with `{onY}` or `{onX}` (the other side's typed alias, if any, is kept). `return-failed` → refresh + `return_failed_hint`. `step-failed` → `flow_error` (+ `flow_undo_error`). `paired` → refresh. Empty list → `no_candidates`.
- **Unpair** per row (`peer-unpair-<alias>`) → `ConfirmDialog` (`testIdPrefix="peer-unpair"`, body `unpair_body_both` when `row.counterpart && row.returnEntry`, else `unpair_body_one`) → `unpairHosts({hostId, alias: entry.alias}, counterpart && returnEntry ? {hostId: counterpart.hostId, alias: returnEntry.alias} : null, …)` → errors shown, refresh.
- **`RotationControls`** (one per direction line, and on a pending candidate):
  ```ts
  interface RotationProps {
    holder: Ref                      // whose entry's inbound token this direction presents
    row: PeerHostRow                 // the holder's row: `entry` for the inbound line, `returnEntry` for the outbound line
    stale: boolean                   // the side's gateStale from the loader
    evidenceDialled: boolean         // did THIS run dial the holder from the presenter? inbound line: `row.inbound` is a verify outcome (an object); outbound line: true; candidate: false
    push: ((token: string) => Promise<void>) | null   // null = presenter not an App host / unavailable → Rotate absent + `rotate_unavailable` tooltip
    label: 'rotate' | 'retry_return' | 'create_return'
    testId: string                   // 'peer-inbound' | 'peer-outbound' | 'peers-cand-<hostId>'
    busy: boolean
    onDone: () => void               // refresh
  }
  ```
  - outbound line (X→Y): holder `{Y, returnEntry.alias}`, row `returnEntry`, stale `gateStale.returnEntry`; only when `counterpart && returnEntry`. push = `updatePeerHost(X, entry.alias, {token})`.
  - inbound line (Y→X): holder `{X, entry.alias}`, row `entry`, stale `gateStale.entry`. push when the counterpart is available: `returnEntry` ? `updatePeerHost(Y, returnEntry.alias, {token})` (label `retry_return` when `row.inbound` is a failed verify outcome, else `rotate`) : `addPeerHost(Y, {alias: self.self_alias, url: X url, token})` (label `create_return`). Not an App host / unavailable → `push: null`.
  - Rendering: `offer = rotationOffer(row)`. `offer !== null` → `rotation_pending` badge + `rotation_current`/`_prev`/`_none` by `last_inbound_auth`; then, **only when `!stale`**, exactly one of `<testId>-commit` / `<testId>-cancel` / nothing; when `stale` → neither + `rotation_stale`; when `!evidenceDialled` → the button by the rule + `rotation_as_of_last_dial`. Commit → `commitRotation(holder)`, Cancel → `cancelRotation(holder)`; a 409 → the daemon text inline (`<testId>-gate-error`); both end in `onDone()`.
  - `offer === null && push` → the Rotate button (`<testId>-rotate`, label by `label`); click → `rotateDirection(holder, push, api, report)`; `pushed` → `pushed_hint`; `push-failed`/`rotate-failed` → `flow_error`; always `onDone()`.
  - `push === null && offer === null` → `<testId>-rotate-unavailable` (disabled, `title` = `rotate_unavailable`).
- All buttons disabled while `busy` or any flow runs.

- [ ] **Step 1: failing tests** (extend `PeersSection.test.tsx`; add `addPeerHost, deletePeerHost, rotatePeerHost, commitRotation, cancelRotation` to the mock; tokens `pdxp_` + 32 hex; `afterEach`: `assertNoTokenLeak()` = `JSON.stringify(useHostStore.getState())` and every `localStorage` key/value contain no `pdxp_`; `assertNoForce()` = no mocked call's args JSON contains `"force"`; `assertNoTokenInDom()` = `document.body.innerHTML` contains no `pdxp_`):
  - Pair happy: X no entries, A available, no entry → `peers-pair-hA` → `addPeerHost(hA, {alias:'mini-lab', url:'http://100.64.0.2:7860'})` → `addPeerHost(hM, {url:'http://100.64.0.4:7860', token:<A's inbound_token>})` → `updatePeerHost(hA,'mini-lab',{token:<M's inbound_token>})` in `invocationCallOrder`; the list mocks flip to the paired fixture; row `bidirectional`.
  - Step 2 502 → `deletePeerHost(hA,'mini-lab')`; error shown; no row.
  - Step 3 502 → row `one-way` + `return_failed_hint`; inbound line shows **Retry return path**; click → `rotatePeerHost(hM,'air')` → `updatePeerHost(hA,'mini-lab',{token})` → then the refresh: `verifyPeerHost(hA,'mini-lab')` → `listPeerHosts(hM)` (post-dial) → Commit appears (row `'current'`) → click → `commitRotation(hM,'air')` with **two args**; assert `commitRotation`'s order > the post-dial `listPeerHosts(hM)` > `verifyPeerHost(hA,…)` > `updatePeerHost`.
  - 409 on step 1 → `peers-alias-input-hA`; type `mlab-2` + retry → `addPeerHost(hA, {alias:'mlab-2', …})`.
  - `listError` on a candidate → Pair disabled + `pair_blocked_unread` (codex F4).
  - Repair candidate with `returnEntry.rotation_pending` + `last_inbound_auth:'current'` → Pair disabled, `peers-cand-hA-commit` present + `rotation_as_of_last_dial`; click → `commitRotation(hA,'mini-lab')` → refresh (codex F3). Same with `'prev'` → cancel only; `''` → neither.
  - Unpair: dialog names both; confirm → both deletes; 404 on one → no error; non-App counterpart → only X deleted, body says so.
  - **Rotate, inbound line, the §6.4 flow (§8.4 call order):** click `peer-inbound-rotate` → `rotatePeerHost(hM,'air')` → `updatePeerHost(hA,'mini-lab',{token})` → refresh: `verifyPeerHost(hA,'mini-lab')` → post-dial `listPeerHosts(hM)` says `'current'` → **only** `peer-inbound-commit` rendered → click → `commitRotation(hM,'air')` (two args) → refresh shows no pending. Assert the full `invocationCallOrder` chain and that `commitRotation` was not called before the click.
  - **Push failure → Cancel offered, Commit absent (the §8.4 mutation fixture, stateful):** `updatePeerHost` rejects `HostApiError(409,…,'entry changed concurrently')`; `listPeerHosts(hM)` after the rotate returns `rotation_pending:true` with `last_inbound_auth:'current'` **until** `verifyPeerHost(hA,'mini-lab')` has been called after the rotate, then `'prev'` → `peer-inbound-cancel` present, `peer-inbound-commit` absent, `commitRotation` never called. (Mutation M-A / M-B target.)
  - Reload with `rotation_pending` from the first paint: before the post-dial re-read no button; then `'current'` → Commit only; `'prev'` → Cancel only; `''` → neither + `rotation_none`.
  - Both sides pending, Y's re-list fails → outbound line `rotation_stale` + no button, inbound line has its button (codex F5).
  - Commit answered 409 → `peer-inbound-gate-error` = `rotation unconfirmed`, `listPeerHosts` called again.
  - **Cancel answered 409** → same, on the `'prev'` fixture (codex F6).
  - Non-App counterpart with pending rotation → button by the rule + `rotation_as_of_last_dial`; no Rotate; `peer-inbound-rotate-unavailable` has the tooltip.
  - Outbound line Rotate → `rotatePeerHost(hA,'mini-lab')` → `updatePeerHost(hM,'air',{token})` → refresh → `verifyPeerHost(hM,'air')` → post-dial `listPeerHosts(hA)` → Commit → `commitRotation(hA,'mini-lab')`.
  - "Create return entry": X has `air`, A has no entry → inbound line `create_return` → `rotatePeerHost(hM,'air')` → `addPeerHost(hA,{alias:'mini-lab',url:X url,token})` → refresh → `verifyPeerHost(hA,'mini-lab')` → `listPeerHosts(hM)` → Commit.
  - After the pair and rotate flows: `assertNoTokenInDom()`.
- [ ] **Step 2:** red. **Step 3:** implement. **Step 4:** green; full `npx vitest run`; lint; build. **Step 5:** commit `feat(peers): pair, unpair and rotate from the Peers page (spec §7)`.

### Task 7: mutation record

**Files:** new `docs/plans/2026-09-18-peer-pairing-d4-mutations.md` (D2 record format: sha, per-mutation exact edit, command, result, printed assertion; `git status --short` empty before and after).

Required mutations (each must go RED; a green one means the test is missing and must be added first):
- **M-A (spec §8.4, the named one — read the row BEFORE the dial):** in `loadPairings` step 5, move the re-read before `Promise.all(dials)` (or reuse the pre-dial `listOf` memo) → the stateful fixture reads `'current'`, the loader test asserts `'prev'` and fails; the component push-failure test renders Commit and fails.
- **M-B:** in `RotationControls`, `offer = row.rotation_pending ? 'commit' : null` (ignore `last_inbound_auth`) → the push-failure and the three reload tests fail.
- **M-C:** remove the `!stale` guard → the F5 and `rotation_stale` tests fail.
- **M-D:** `commitRotation` sends `body: JSON.stringify({force:true})` → the wrapper test and `assertNoForce` fail.
- **M-E:** stash `inbound_token` into `useHostStore.setState({ lastToken })` (and separately `localStorage.setItem`) inside the pair handler → `assertNoTokenLeak` fails.
- **M-F:** `rotateDirection` swallows the push error and returns `pushed` → the push-failed actions test fails.
- **M-G:** `pairHosts` step 2 failure skips the undo delete → the step-2 tests fail.
- **M-H:** `unpairHosts` treats 404 as an error → the 404 test fails.
- **M-I:** `pairHosts` uses `x.url` for step 2's `url` → the happy-path body assertion fails.
- **M-J:** repair path uses `add` instead of `rotate` → the repair test fails.
- **M-K:** `rotationOffer` returns `'commit'` for `''` → table fails.
- **M-L:** `listPeerHosts` normalisation dropped → the pre-391 test fails.
- **M-M:** Pair button ignores `listError` → the F4 test fails.

- [ ] Run each, record, revert; commit the record `docs(peers): D4 mutation record`.

### Task 8: real-machine acceptance (spec §9 D4 — must be run, not assumed) and the PRs

Run by the main session (not a subagent) against the two live daemons (mlab `mini-lab:278cbm`, air-2026 `wakes-air-2026:oa6drb`, both alpha.391+), from the worktree's own dev server (`cd spa && npx vite --port 5175 --host 100.64.0.2`) with `playwright cli -s=peer-pairing-d4`, the App's `purdex-hosts` localStorage seeded with both hosts (admin tokens from each machine's `~/.config/pdx/config.toml` line 4, read into shell variables, only their lengths printed; air's via `ssh air26`). Every complex Bash step is a scratchpad zsh script.

Record in the D4b PR body, in this order, each with the CLI evidence:
1. Before: `pdx peers host list` on both machines, `pdx peers --all` green both ways.
2. **Unpair** mlab↔air26 from the page → both `host list`s empty.
3. **Pair** from the page → both `host list`s show the entry with both tokens (`has_token`/`has_inbound_token`), `pdx peers --all` green both ways.
4. **Rotate** (inbound line on mlab's page) → after the refresh Commit is offered (`pending, confirmed` in `host list`), click Commit → no pending; `pdx peers --all` green both ways. Then the outbound line once.
5. After: `pdx peers --all` green both ways; `pdx msg send air26/_64wca8 "D4 acceptance"` delivered.
⚠️ Step 2 really breaks the pair; step 3 must follow in the same sitting. Close the playwright session afterwards.

- [ ] Open PR D4b based on D4a's branch: `feat(peers): D4b — pair/unpair/rotate controls on the Peers page`, body linking spec §7/§8.4/§9, the mutation record, the acceptance evidence, and the residual below.

---

## Known residual (stated, not hidden)

Repair path, step 2 fails after a successful `rotate` on Y: Y's entry keeps a pending rotation the page cannot undo without `force` (never sent). The candidate line offers Commit/Cancel by the ordinary rule "as of the peer's last dial" (codex F3): a 409 on X's POST means X did dial Y with the new token → `'current'` → Commit; a transport failure means no dial → `''` → neither, until X can dial Y — which needs a successful Pair, which is blocked while the rotation is pending. The operator's way out is `pdx peers host rotate <alias> --cancel --force` on Y. A follow-up issue proposes a daemon-side "cancel without force when neither token has ever been presented".

## Plan review

Codex `task-mu6xk6sb-hatswl` (gpt-5.6-sol, with the spec): 7 findings, all accepted — F1 no auto-commit (Task 4 redesign), F2 M-A rewritten as "read before the dial" with a stateful fixture, F3 candidate-line Commit/Cancel, F4 `listError` blocks Pair, F5 per-side `gateStale`, F6 Cancel-409 test, F7 Task 8.
