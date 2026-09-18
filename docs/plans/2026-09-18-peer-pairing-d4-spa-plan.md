# Peer Pairing D4 — SPA pair / unpair / rotate from the page: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the operator pair two App hosts, unpair a row, and rotate either direction's inbound token from the Peers sub-page, with the page never deciding Commit/Cancel from its own memory (spec D-7, §7.3) and never holding a token value outside one flow's component state (D-8).

**Architecture:** D4 adds to D2's three layers and does not rewrite them. `host-api.ts` gains five wrappers over the D0/D3 routes (no logic). `lib/peer-pairing.ts` gains one pure rule, `rotationOffer`. `lib/peer-pairing-load.ts` gains two things the page needs before it can act: the list of pair candidates (available App hosts with no entry on X, each with whether *they* already hold an entry for X — the repair case), and a **post-dial re-read** of any row whose entry has `rotation_pending` (spec §7.3: "re-verifies the return path, then re-reads the row" — the initial list is pre-dial). A new `lib/peer-pairing-actions.ts` holds the three flows (`pairHosts`, `rotateDirection`, `unpairHosts`) as async functions over an injected API that report each step through a callback, testable without React. `PeersSection.tsx` wires the buttons, the confirm dialog, the inline alias prompt and the step display.

**Split into two stacked PRs** (spec D-9 allows it; D2 was split the same way): **D4a** = Tasks 1–4 (lib layer, ~700 lines), **D4b** = Tasks 5–7 (page, i18n, mutation record). D4b's branch is cut at D4a's last commit and its PR is based on D4a's branch.

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
}
export interface PairingRow {
  … (unchanged fields) …
  /**
   * §7.3: Commit/Cancel may only be offered from a row read AFTER the evidence dial.
   * true when a rotation is pending on `entry` or `returnEntry` and this run could
   * NOT re-read that row after its dial (the re-list failed) — the component then
   * offers neither and says so. false when no rotation is pending or the re-read landed.
   */
  gateStale: boolean
}
export interface PairingSnapshot { self; error; rows; candidates: PairCandidate[] }
```

Behaviour:
- **Candidates** (after step 3): every `Meta` with `available: true` whose `hostId` is not the `counterpart.hostId` of any row → `listOf(hostId)` (memoised with step 3's map, so a host that is both a counterpart and… cannot happen; a host that is a candidate is listed once) → `returnEntry = matchReturnEntry({host_id: self.host_id, url: x.url}, theirs)`; a failed list makes `returnEntry: null` **and** excludes nothing (the candidate is still offered; the actions layer will find out). Unavailable hosts are not candidates (§7.1: "a host whose info could not be fetched is not listed"). Candidates are in the pre-dial emit and every later emit.
- **Step 5, post-dial re-read:** after `await Promise.all(dials)`, collect `{hostId → aliases}` for: X when any `row.entry.rotation_pending`; each `row.counterpart.hostId` when `row.returnEntry?.rotation_pending`. For each such host call `api.list(hostId)` **once** (a fresh call, not `listOf`'s memo — the memo is the pre-dial read). On success replace `row.entry` / `row.returnEntry` with the row of the same alias from the fresh list (if the alias is gone, keep the old row and set `gateStale: true`). On failure set `gateStale: true` on every row that needed that host. Emit once at the end. Rows with nothing pending have `gateStale: false` and cause no extra call. **The re-read happens after `Promise.all(dials)`, never before or concurrently** — the test pins the order.
- The pre-dial emit and every settle emit carry `gateStale: <a rotation is pending on entry or returnEntry>` — a pending row is stale by definition until step 5 has re-read it, so the component never offers a button from a pre-dial row; step 5 clears it (or leaves it set on failure). Name this in a comment.

- [ ] **Step 1: failing tests** (extend the file's fake-API harness; fixtures gain the two fields):
  - "candidates: an available host with no entry on X is listed with `returnEntry: null` and cost exactly one extra `list` call for it" (X has zero entries; others = [A connected] → `candidates` = `[{hostId:A, …, returnEntry:null}]`, `list` called for X and A once each).
  - "candidates: the repair case — Y already holds an entry for X → `returnEntry` is that row" (A's list returns `[MLAB_ROW]`).
  - "candidates: a host that is some row's counterpart is not a candidate" (the D2 fixture → `candidates` empty).
  - "candidates: an unavailable host is not a candidate" (A disconnected → empty; `list` never called for A).
  - "candidates: a failing `list(Y)` still lists Y with `returnEntry: null`".
  - "post-dial re-read: a pending rotation on X's entry re-lists X after BOTH dials settle, and the row shows the fresh `last_inbound_auth`" — fake `list(X)` returns `{…AIR_ROW, rotation_pending:true, last_inbound_auth:''}` first and `{…, 'current'}` second; record the call sequence in an array (`['list:X','verify:X:air','verify:A:mini-lab','list:X']`) and assert `list:X` #2 comes after both verifies; final row `entry.last_inbound_auth === 'current'`, `gateStale === false`; the pre-dial emit's row has `gateStale === true`.
  - "post-dial re-read: pending on Y's entry re-lists Y, not X" (call sequence has exactly one `list:X` and two `list:A`).
  - "post-dial re-read: the second `list` failing sets `gateStale: true` and keeps the pre-dial row".
  - "no pending rotation anywhere → no extra list call, `gateStale` false everywhere" (D2 fixture: `list` call count unchanged from D2's test).
  - "the alias vanished between the two lists (deleted concurrently) → old row kept, `gateStale: true`".
- [ ] **Step 2:** red. **Step 3:** implement per the behaviour above. Keep `loadOnce`'s memo for step 3 + candidates; step 5 uses `api.list` directly. **Step 4:** green; also `npx vitest run src/components/hosts/PeersSection.test.tsx` still green (D2 tests must not notice: `candidates` is empty in their fixture). Lint.
- [ ] **Step 5:** commit `feat(peers): loadPairings lists pair candidates and re-reads pending rows after the dials (§7.3)`.

### Task 4: `peer-pairing-actions.ts` — the three flows

**Files:** new `spa/src/lib/peer-pairing-actions.ts`, `spa/src/lib/peer-pairing-actions.test.ts`.

**Interfaces produced:**

```ts
export interface ActionApi {
  add: (hostId: string, body: { alias?: string; url: string; token?: string }) => Promise<PeerHostAdded>
  update: (hostId: string, alias: string, patch: { alias?: string; token?: string }) => Promise<PeerHostRow>
  delete: (hostId: string, alias: string) => Promise<void>
  rotate: (hostId: string, alias: string) => Promise<{ alias: string; inbound_token: string }>
  commit: (hostId: string, alias: string) => Promise<PeerHostRow>
  cancel: (hostId: string, alias: string) => Promise<PeerHostRow>
  verify: (hostId: string, alias: string) => Promise<PeerHostVerify>
  list: (hostId: string) => Promise<PeerHostRow[]>
}
/** Every step a flow reports. Never carries a token. */
export type FlowStep =
  | 'mint' | 'push' | 'verify' | 'read' | 'commit'                       // rotateDirection
  | 'create-on-y' | 'rotate-on-y' | 'create-on-x' | 'push-to-y' | 'undo-on-y' // pairHosts
  | 'delete-x' | 'delete-y'                                              // unpairHosts
export type Report = (step: FlowStep) => void

export interface Ref { hostId: string; alias: string }

/**
 * Spec §6.4 with the roles named: `holder` is the entry whose inbound token is
 * rotated; `presenter` is the host that must present it (and whose dial to the
 * holder is the evidence). `push(token)` stores the new token on the presenter
 * (a PUT on its existing entry, or — for "create return entry" — a POST that
 * creates it) and resolves the presenter-side alias to verify.
 */
export type Push = (token: string) => Promise<{ verifyAlias: string | null; error: string }>
export function rotateDirection(holder: Ref, presenter: { hostId: string }, push: Push, api: ActionApi, report: Report): Promise<RotateOutcome>
export type RotateOutcome =
  | { kind: 'committed' }
  | { kind: 'pending'; offer: RotationOffer; pushError: string; commitError: string }   // pushError '' when the push landed; commitError '' unless the commit 409'd
  | { kind: 'rotate-failed'; error: string }     // nothing changed
  | { kind: 'read-failed'; error: string }       // the post-dial list failed; both tokens stay valid
export function pairHosts(x: { hostId: string; url: string; selfAlias: string }, y: { hostId: string; url: string; returnEntry: PeerHostRow | null }, aliases: { onY?: string; onX?: string }, api: ActionApi, report: Report): Promise<PairOutcome>
export type PairOutcome =
  | { kind: 'paired'; aliasOnX: string; aliasOnY: string }
  | { kind: 'alias-conflict'; side: 'x' | 'y'; error: string }   // nothing left behind on the non-repair path
  | { kind: 'step-failed'; step: 'create-on-y' | 'rotate-on-y' | 'create-on-x'; error: string; undoError: string }
  | { kind: 'return-failed'; aliasOnX: string; aliasOnY: string; error: string }   // both entries exist → one-way
  | { kind: 'repair-pending'; aliasOnX: string; aliasOnY: string; offer: RotationOffer; commitError: string }
export function unpairHosts(x: Ref, y: Ref | null, api: ActionApi, report: Report): Promise<UnpairOutcome>
export type UnpairOutcome = { xError: string; yError: string }   // '' = deleted or was already gone (404)
```

Behaviour, exactly:

**`rotateDirection`:**
1. `report('mint')`; `rotate(holder)` → `{alias, inbound_token: tok}`. Throws → `{kind:'rotate-failed', error}`.
2. `report('push')`; `{verifyAlias, error: pushError} = await push(tok)`. `push` is built by the component and **never throws**: `error` non-empty means the push failed; `verifyAlias` is the presenter-side alias to dial with — the existing entry's alias for a PUT (whether or not the PUT failed: the entry still exists and still holds the old token), the created alias for a successful POST, `null` when nothing exists on the presenter to dial with (a failed POST).
3. `report('verify')`; if `verifyAlias !== null`: `await verify(presenter.hostId, verifyAlias)` — its result is **ignored** (ok or not; the dial itself is the evidence, and the row is what is read next); a throw is ignored too.
4. `report('read')`; `rows = await list(holder.hostId)` → throws → `{kind:'read-failed', error}`. `row = rows.find(alias === holder.alias)`; missing → `{kind:'read-failed', error:'entry <alias> not found after rotation'}`. `offer = rotationOffer(row)`; `null` (someone else committed/cancelled) → treat as `'none'`.
5. If `offer === 'commit'`: `report('commit')`; `await commit(holder)` → `{kind:'committed'}`; a 409 → `{kind:'pending', offer, pushError, commitError: msg}` (the row is re-read by the caller's refresh, §7.3); other throw → same shape.
6. Else `{kind:'pending', offer, pushError, commitError:''}`.
**The outcome never depends on `pushError` being empty** — that is the §8.4 mutation. The only path to `commit()` is step 4's fresh row saying `'current'`.

**`pairHosts`** (spec §7.1, steps numbered as there):
1. If `y.returnEntry` (repair): `report('rotate-on-y')`; `rotate(y.hostId, y.returnEntry.alias)` → `tY`; `aliasOnY = y.returnEntry.alias`. Throw → `{kind:'step-failed', step:'rotate-on-y', error, undoError:''}`.
   Else: `report('create-on-y')`; `add(y.hostId, {alias: aliases.onY ?? x.selfAlias, url: x.url})` → `tY`, `aliasOnY = res.alias`. 409 → `{kind:'alias-conflict', side:'y', error}`; other throw → `{kind:'step-failed', step:'create-on-y', …}`.
2. `report('create-on-x')`; `add(x.hostId, {url: y.url, token: tY, ...(aliases.onX ? {alias: aliases.onX} : {})})` → `tX`, `aliasOnX = res.alias`. On any throw: non-repair → `report('undo-on-y')`; `delete(y.hostId, aliasOnY)` (a 404 is fine; any other error → `undoError`); then 409 → `{kind:'alias-conflict', side:'x', error}` (the Y entry was removed, so the retry re-runs step 1 cleanly — this is why the undo is unconditional on the non-repair path); other → `{kind:'step-failed', step:'create-on-x', error, undoError}`. Repair → no undo possible without `force` (the rotation on Y stays pending; the row's Commit/Cancel follow the rule after refresh, and Pair is not re-offered while `returnEntry.rotation_pending` — component rule) → `{kind:'step-failed', step:'create-on-x', error, undoError:''}`.
3. `report('push-to-y')`; `update(y.hostId, aliasOnY, {token: tX})`. Throw → `{kind:'return-failed', aliasOnX, aliasOnY, error}` (both entries exist; the page shows `one-way` with "Retry return path" = `rotateDirection(holder X/E, presenter Y)` with a PUT push).
4. Repair only: the rotation on Y's entry is still pending. Per §7.3 finish it the same way every rotation is finished: `report('verify')`; `verify(x.hostId, aliasOnX)` (X dials Y with `tY`, ignored result); `report('read')`; `list(y.hostId)` → row `aliasOnY` → `offer`; `'commit'` → `report('commit')`; `commit(y.hostId, aliasOnY)` → `{kind:'paired', …}`; 409 → `{kind:'repair-pending', offer, commitError}`; other offers → `{kind:'repair-pending', offer, commitError:''}`; a failed list → `{kind:'repair-pending', offer:'none', commitError: msg}`.
   Non-repair: `{kind:'paired', aliasOnX, aliasOnY}`.
Local variables `tY`, `tX` are the only places tokens exist; the outcome objects carry aliases only.

**`unpairHosts`:** `report('delete-x')`; `delete(x)`; a `HostApiError` with `status === 404` → `''`, other throw → `xError`. If `y`: `report('delete-y')`; same → `yError`. Both attempted regardless of the first's result (§7.2: "either 404 is treated as already done"; a failure on X must not leave Y's half dangling for no reason).

- [ ] **Step 1: failing tests** — a `fake()` harness building an `ActionApi` from `vi.fn`s and a `calls: string[]` log (`'rotate:X:air'`, `'update:A:mini-lab:{token}'` — log **`hasToken: true/false`, never the value**), tokens `pdxp_` + 32 hex; a `steps: FlowStep[]` collector. After **every** test: `expect(JSON.stringify({outcome, steps})).not.toMatch(/pdxp_/)`.
  - rotateDirection happy path: calls in order `rotate:X:air`, push (component fake: `update:A:mini-lab` with the token), `verify:A:mini-lab`, `list:X`, `commit:X:air`; outcome `committed`; steps `['mint','push','verify','read','commit']`; **`commit` called with exactly two args (no body/force)**.
  - **the §8.4 fixture:** push's PUT rejects with `HostApiError(409,'Conflict','entry changed concurrently')` *after* the fake records that Y dialled (the fake `update` pushes `'dial:A→X'` to `calls` before throwing); `list(X)` returns `last_inbound_auth:'prev'` (Y re-presented the old token at the verify) → outcome `{kind:'pending', offer:'cancel', pushError:'entry changed concurrently', commitError:''}`; `commit` **never called**. This test is the mutation target (Task 7 M-A).
  - push landed but `list(X)` says `''` (peer could not be made to dial) → `pending / none`, no commit.
  - push landed, row says `'current'`, but commit 409s (`rotation unconfirmed`, raced) → `pending / commit / commitError`.
  - `rotate` 409 `rotation already pending` → `rotate-failed`, nothing else called.
  - `list(X)` throws → `read-failed`, no commit.
  - `push` returns `{verifyAlias:null, error}` (create failed) → no verify call, then read → row `''` → `pending / none / pushError`.
  - verify rejecting (e.g. 404) is ignored: flow proceeds to read.
  - pairHosts happy: `add:A {alias:'mini-lab', url:X url}` (no token key), `add:X {url:A url, token}` (no alias key), `update:A:mini-lab {token}`; outcome `paired`; steps in order; the token passed to X's add **equals** A's `inbound_token` and the one pushed to A equals X's `inbound_token` (the fake asserts equality internally and logs only booleans).
  - pair with `aliases.onX` → X's add carries `alias`.
  - step 1 409 → `alias-conflict / y`, nothing else called.
  - step 2 502 (transport) → `delete:A:mini-lab` called, outcome `step-failed / create-on-x`, `undoError:''`; **X has no `add` after the failure and no update on A**.
  - step 2 409 → undo delete on A, then `alias-conflict / x`.
  - undo delete 404 → `undoError:''`; undo delete 500 → `undoError` set.
  - step 3 502 → `return-failed` with both aliases; no delete anywhere.
  - repair: `y.returnEntry` set → `rotate:A:mini-lab` instead of add; then add on X with that token; update on A; then `verify:X:<aliasOnX>`, `list:A`, row `'current'` → `commit:A:mini-lab` → `paired`.
  - repair, row `''` → `repair-pending / none`, no commit.
  - repair, step 2 fails → `step-failed / create-on-x`, **no delete** on A (no undo on the repair path).
  - unpair: both deletes; X 404 + Y 204 → `{xError:'', yError:''}`; X 500 → `xError` set, **Y still deleted**; `y === null` → one call.
- [ ] **Step 2:** red. **Step 3:** implement. **Step 4:** green, lint, build. **Step 5:** commit `feat(peers): pair / rotate / unpair flows as pure orchestration over an injected API (spec §6.4, §7.1–7.3)`.

**D4a PR checkpoint:** full `npx vitest run`, lint, build; `git diff --stat fc39dcc5..HEAD` ≤ 800 lines / 20 files (if over, Task 4's tests are the thing to trim, not the flows). Open PR `feat(peers): D4a — lib layer for pair/unpair/rotate` with body: spec sections, the token-handling invariant, the pre-391 normalisation note, and that `HOST_SUB_PAGES` (`host-routes.ts`) is still deliberately untouched.

---

# PR D4b — the page

### Task 5: i18n keys

**Files:** `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`.

Keys (en; zh-TW in the same order):
`peers.pair_heading` "Pair with…"; `peers.pair` "Pair"; `peers.pairing` "Pairing…"; `peers.pair_repair_note` "{{name}} already has an entry for this host — Pair will rotate its token and complete the return path"; `peers.pair_blocked_pending` "{{name}}'s entry has a rotation pending — commit or cancel it first"; `peers.no_candidates` "Every other connected host is already paired."; `peers.alias_prompt_y` "{{name}} already uses that alias. Alias for this host on {{name}}:"; `peers.alias_prompt_x` "This host already uses that alias. Alias for {{name}} here:"; `peers.alias_retry` "Retry with this alias"; `peers.unpair` "Unpair"; `peers.unpair_title` "Unpair {{x}} and {{y}}?"; `peers.unpair_body_both` "Deletes {{x}}'s entry “{{ex}}” and {{y}}'s entry “{{ey}}”. Both directions stop working until paired again."; `peers.unpair_body_one` "Deletes {{x}}'s entry “{{ex}}”. {{y}} is not a host in this App, so its side is left as is."; `peers.unpairing` "Unpairing…"; `peers.rotate` "Rotate token"; `peers.rotate_unavailable` "Rotation needs the counterpart to be a host in this App"; `peers.retry_return` "Retry return path"; `peers.create_return` "Create return entry"; `peers.commit` "Commit"; `peers.cancel_rotation` "Cancel rotation"; `peers.rotation_pending` "rotation pending"; `peers.rotation_current` "the peer is on the new token"; `peers.rotation_prev` "the peer is still presenting the old token"; `peers.rotation_none` "the peer has not dialled since the rotation; both tokens stay valid"; `peers.rotation_as_of_last_dial` "(as of the peer's last dial)"; `peers.rotation_stale` "(could not re-read after the dial — refresh)"; `peers.step.mint` "minting…"; `peers.step.push` "pushing to the peer…"; `peers.step.verify` "verifying the return path…"; `peers.step.read` "reading the row…"; `peers.step.commit` "committing…"; `peers.step.create-on-y` "creating the entry on the peer…"; `peers.step.rotate-on-y` "rotating the peer's token…"; `peers.step.create-on-x` "creating the entry here…"; `peers.step.push-to-y` "pushing the return token…"; `peers.step.undo-on-y` "removing the half-made entry…"; `peers.step.delete-x` "deleting here…"; `peers.step.delete-y` "deleting on the peer…"; `peers.flow_error` "{{step}} failed: {{error}}"; `peers.flow_undo_error` "and the cleanup failed too: {{error}}"; `peers.return_failed_hint` "The return path was not stored — use Retry return path."

- [ ] Add both files; `npx vitest run src/lib/locale-completeness.test.ts` (or the file's actual path) green; commit `i18n(peers): D4 keys`.

### Task 6: `PeersSection.tsx` — the controls

**Files:** modify `spa/src/components/hosts/PeersSection.tsx`, `spa/src/components/hosts/PeersSection.test.tsx`. If the component passes ~450 lines, extract `PairWithSection` and `RotationControls` into `spa/src/components/hosts/peers/` (same PR).

Wiring rules (all state in `useState`, dies with the component):
- `actionApi(): ActionApi` built **inside handlers** from the host-api wrappers (never at module load).
- A single `flow: { rowKey: string; step: FlowStep | null; error: string } | null` state shows the current step under the affected row/candidate; every flow ends with `run()` (§7.4) and then clears `flow` except its `error`.
- **Pair section** (below the rows, `data-testid="peers-pair"`): one line per `snap.candidates`: name, url; note `pair_repair_note` when `returnEntry`; **Pair** button (`peers-pair-<hostId>`) disabled while `busy`/a flow runs, or when `returnEntry?.rotation_pending` (then `pair_blocked_pending` instead). Click → `pairHosts({hostId, url: getDaemonBase(hostId), selfAlias: snap.self.self_alias}, {hostId: c.hostId, url: c.url, returnEntry: c.returnEntry}, {}, api, report)`. Outcome `alias-conflict` → inline `<input data-testid="peers-alias-input-<hostId>">` + `peers-alias-retry-<hostId>` that re-runs with `{onY}` or `{onX}` (side from the outcome; the other side's typed alias, if any, is kept). `return-failed` → refresh (the row will be `one-way`) + `return_failed_hint`. `step-failed` → `flow_error` (+ `flow_undo_error`). `repair-pending` → refresh; the row shows the pending state by the ordinary rule. Empty list → `no_candidates`.
- **Unpair** per row (`peer-unpair-<alias>`) → `ConfirmDialog` (`testIdPrefix="peer-unpair"`, body `unpair_body_both` when `row.counterpart && row.returnEntry`, else `unpair_body_one` naming the counterpart name or the entry alias) → `unpairHosts({hostId, alias: entry.alias}, counterpart && returnEntry ? {hostId: counterpart.hostId, alias: returnEntry.alias} : null, …)` → errors shown, refresh.
- **Rotation per direction line.** Each `DirectionLine` gets a `rotation` prop:
  ```ts
  rotation: {
    holder: Ref                                   // whose entry's inbound token this direction presents
    row: PeerHostRow                              // the holder's row (entry for the inbound line, returnEntry for the outbound line)
    gateStale: boolean
    evidenceDialled: boolean                      // did THIS run dial holder from the presenter? (inbound line: row.inbound is a verify outcome; outbound line: always true)
    push: ((token: string) => Promise<{ verifyAlias: string | null; error: string }>) | null   // null = presenter is not an App host → Rotate absent + tooltip
    createReturn: boolean                         // inbound line with counterpart available but no returnEntry → button says "Create return entry" and push is a POST
    retryReturn: boolean                          // inbound line, returnEntry exists, inbound verify failed → button says "Retry return path"
  } | null
  ```
  - outbound line (X→Y): holder = `{Y, returnEntry.alias}`, row = `returnEntry`; only when `counterpart && returnEntry`. push = `updatePeerHost(X, entry.alias, {token})` → `{verifyAlias: entry.alias, error:''}` / catch → `{verifyAlias: entry.alias, error}`.
  - inbound line (Y→X): holder = `{X, entry.alias}`, row = `entry` (always exists). push when `counterpart` is available: `returnEntry` ? PUT on Y → `{verifyAlias: returnEntry.alias}` : POST `addPeerHost(Y, {alias: self.self_alias, url: X url, token})` → `{verifyAlias: res.alias}` / catch → `{verifyAlias: null, error}`. Not an App host / unavailable → `push: null`.
  - Rendering: if `rotationOffer(row) !== null` → `rotation_pending` badge + the explanatory sentence by `last_inbound_auth` (`rotation_current`/`_prev`/`_none`), then **exactly one** of `peer-<line>-commit` / `peer-<line>-cancel` / nothing, **and only when `!gateStale`**; when `gateStale` → neither + `rotation_stale`; when `!evidenceDialled` (non-App / unavailable counterpart) → the button by the rule + `rotation_as_of_last_dial`. Commit → `commitRotation(holder)`; Cancel → `cancelRotation(holder)`; a 409 → the daemon text inline; both end in refresh.
  - If `rotationOffer(row) === null` and `push` → **Rotate** button (`peer-<line>-rotate`), labelled `retry_return` / `create_return` in those cases; click → `rotateDirection(holder, {hostId: presenter}, push, api, report)`; outcome → error text if any; refresh.
  - `push === null` → no Rotate; `title={t('peers.rotate_unavailable')}` on a disabled placeholder (`peer-<line>-rotate-unavailable`).
- Buttons are disabled while `busy` or any flow is running.

- [ ] **Step 1: failing tests** (extend `PeersSection.test.tsx`; add `addPeerHost, deletePeerHost, rotatePeerHost, commitRotation, cancelRotation` to the mock; fixtures gain the two fields; tokens are `pdxp_` + 32 hex; helper `assertNoTokenLeak()` = `JSON.stringify(useHostStore.getState())` and every `localStorage` key/value contain no `pdxp_`; helper `assertNoForce()` = every mocked call's args JSON contains no `"force"`; both run in `afterEach`):
  - Pair happy path: X has no entries, A available with no entry → `peers-pair-hA` → `addPeerHost(hA, {alias:'mini-lab', url:'http://100.64.0.2:7860'})`, then `addPeerHost(hM, {url:'http://100.64.0.4:7860', token:<A's inbound_token>})`, then `updatePeerHost(hA, 'mini-lab', {token:<M's inbound_token>})` — assert order via `mock.invocationCallOrder`; then the list mocks flip to the paired fixture and the row appears `bidirectional`.
  - Step 2 fails (502) → `deletePeerHost(hA,'mini-lab')` called; error text shown; no row.
  - Step 3 fails → row `one-way`, `return_failed_hint`, inbound line shows **Retry return path**; clicking it → `rotatePeerHost(hM,'air')` → `updatePeerHost(hA,'mini-lab',{token})` → `verifyPeerHost(hA,'mini-lab')` → `listPeerHosts(hM)` → `commitRotation(hM,'air')` (when the re-read says current).
  - 409 on step 1 → `peers-alias-input-hA` appears; typing `mlab-2` + retry → `addPeerHost(hA, {alias:'mlab-2', …})`.
  - Unpair: click → dialog names both; confirm → `deletePeerHost(hM,'air')` and `deletePeerHost(hA,'mini-lab')`; 404 on one → no error; non-App counterpart → only X deleted and the body says so.
  - Rotate (inbound line, the §6.4 flow): call order `rotatePeerHost(hM,'air')` → `updatePeerHost(hA,'mini-lab',{token})` → `verifyPeerHost(hA,'mini-lab')` → `listPeerHosts(hM)` → `commitRotation(hM,'air')` with **two args**; **commit sent only after `listPeerHosts` returned `last_inbound_auth:'current'`** (assert `commitRotation.mock.invocationCallOrder[0] > listPeerHosts` last order).
  - **Push failure → Cancel offered, Commit absent:** `updatePeerHost` rejects 409 after the mock notes the dial; `listPeerHosts(hM)` after the rotate returns `{rotation_pending:true,last_inbound_auth:'prev'}` → `peer-inbound-cancel` present, `peer-inbound-commit` absent, `commitRotation` never called. (Mutation target M-B.)
  - Reload with `rotation_pending` (fixture lists X's row pending from the start): the initial paint offers nothing until the post-dial re-read; then `'current'` → Commit only; `'prev'` → Cancel only; `''` → neither + `rotation_none`. Three cases.
  - `gateStale` (second `listPeerHosts(hM)` rejects) → neither button + `rotation_stale`.
  - Commit answered 409 → text `rotation unconfirmed` inline, `listPeerHosts` called again (refresh).
  - Non-App counterpart with pending rotation → Commit/Cancel by the rule + `rotation_as_of_last_dial`; **no Rotate button**, `peer-inbound-rotate-unavailable` has the tooltip.
  - Outbound line Rotate → `rotatePeerHost(hA,'mini-lab')` → `updatePeerHost(hM,'air',{token})` → `verifyPeerHost(hM,'air')` → `listPeerHosts(hA)` → `commitRotation(hA,'mini-lab')`.
  - "Create return entry": X has `air`, A has no entry → inbound line button → `rotatePeerHost(hM,'air')` → `addPeerHost(hA,{alias:'mini-lab',url:X url,token})` → `verifyPeerHost(hA,'mini-lab')` → `listPeerHosts(hM)` → commit when current.
  - Every test: `assertNoTokenLeak()`, `assertNoForce()`; plus one explicit test that renders the DOM after the pair and rotate flows and asserts `document.body.innerHTML` contains no `pdxp_`.
- [ ] **Step 2:** red. **Step 3:** implement. **Step 4:** green; full `npx vitest run`; lint; build. **Step 5:** commit `feat(peers): pair, unpair and rotate from the Peers page (spec §7)`.

### Task 7: mutation record + PR

**Files:** new `docs/plans/2026-09-18-peer-pairing-d4-mutations.md` (same format as the D2 record: sha, per-mutation exact edit, command, result, printed assertion; `git status --short` empty before and after).

Required mutations (each must go RED; a green one means the test is missing and must be added first):
- **M-A (spec §8.4, the named one):** in `rotateDirection`, replace `const offer = rotationOffer(row)` with `const offer = pushError === '' ? 'commit' : rotationOffer(row)` (decide from memory) → the "push's verify passed but Y failed to persist" test must fail on `commit` being called.
- **M-B:** in `PeersSection`, offer Commit whenever `rotation_pending` regardless of `last_inbound_auth` → the push-failure and the three reload tests fail.
- **M-C:** remove the `!gateStale` guard → the `gateStale` test fails.
- **M-D:** `commitRotation` sends `body: JSON.stringify({force:true})` → the wrapper test and `assertNoForce` fail.
- **M-E:** stash `inbound_token` into `useHostStore.setState({ lastToken })` (or `localStorage.setItem`) inside the pair flow → `assertNoTokenLeak` fails.
- **M-F:** in `loadPairings` step 5, move the re-read before `Promise.all(dials)` → the order test fails.
- **M-G:** in `pairHosts` step 2 failure, skip the undo delete → the step-2 tests fail.
- **M-H:** `unpairHosts` treats 404 as an error → the 404 test fails.
- **M-I:** `pairHosts` step 1 uses `x.url` for step 2's `url` (wrong host) → the happy-path body assertion fails.
- **M-J:** repair path uses `add` instead of `rotate` → the repair test fails.
- **M-K:** `rotationOffer` returns `'commit'` for `''` → table fails.
- **M-L:** `listPeerHosts` normalisation dropped (`last_inbound_auth: undefined` passes through) → the pre-391 test fails.

- [ ] Run each, record, revert; commit the record `docs(peers): D4 mutation record`.
- [ ] Open PR D4b based on D4a's branch: `feat(peers): D4b — pair/unpair/rotate controls on the Peers page`. Body links spec §7/§8.4/§9, the mutation record, and holds the real-machine acceptance section (filled in after §9 D4 is run; see the kickoff memory file for the exact procedure and its warnings).

---

## Known residual (stated, not hidden)

Repair path, step 2 fails after a successful `rotate` on Y: Y's entry keeps a pending rotation and the page cannot undo it without `force` (which it never sends). If X's dial reached Y (a 409 on X's POST) the row reads `'current'` and Commit is offered; if it did not, it reads `''` and only Refresh is offered until X dials Y — which needs X to hold a token for Y, i.e. a successful Pair. The operator's way out is `pdx peers host rotate <alias> --cancel --force` on Y. Recorded in the PR; a follow-up issue proposes "cancel without force when the entry has never been presented either token" (daemon-side).
