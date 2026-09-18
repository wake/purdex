# D4 Peer Pairing — Mutation-Test Record

- Branch: `worktree-peer-pairing-d4b`
- HEAD sha: `42527309d5946fb47ea9b1bef281421690d53a84`
- Date: 2026-09-18

Task 7 of `2026-09-18-peer-pairing-d4-spa-plan.md` proves the tests written
for Peer Pairing D4 guard what they claim (spec §8.4: mutation tests are a
deliverable). Each row below was applied as a temporary one-edit mutation to
one source file, run against the named test(s) with
`cd spa && npx vitest run <file> -t '<title substring>'` (vitest has no cache
by default; `--changed` was never used), confirmed, then **undone by reversing
the exact edit** (never `git checkout --`, `stash` or `reset`). `git status
--short` printed nothing but the pre-existing untracked `.playwright-cli/`
before the first row and after every undo. No two mutations were ever applied
at once, and none was committed.

Baseline at this HEAD before the first row and again after the last undo:
the five files (`peer-pairing.test.ts`, `peer-pairing-load.test.ts`,
`peer-pairing-actions.test.ts`, `host-api.peers.test.ts`,
`PeersSection.test.tsx`) — 193 tests, all green.

Rows M-A … M-M are the plan's Task 7 list; M-N and M-O came from the codex
D4b round. M-A and M-E have sub-rows because the brief named alternative
edits; each sub-row was applied and undone on its own.

| # | Mutation (exact edit) | File | Command | Result | Failing assertion (as printed) |
|---|---|---|---|---|---|
| M-A1 | Moved `await Promise.all(dials)` from directly after the `rows.forEach(...)` that starts the dials to directly before the final `return { ...snap }` (the step-5 re-read now runs while the dials are still in flight) | `spa/src/lib/peer-pairing-load.ts` | `npx vitest run src/lib/peer-pairing-load.test.ts -t 're-lists X after BOTH dials settle'` and `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'push failed after its verify dial'` | **PASS (NOT RED)** (both) | Neither test failed. Both stateful fixtures flip `last_inbound_auth` when `verify` is **called**, not when it settles (`statefulApi` pushes `verify:…` onto `calls` synchronously inside the mock; `seedRotation` sets `dialled = true` synchronously inside the `verifyPeerHost` mock). With this edit the verifies are still started before the re-read, so the fixture already reads `'prev'`. The concurrent-read bug is real but this fixture shape cannot see it — see the deviations section. |
| M-A2 | `fresh.set(h, await api.list(h).catch(toError))` → `fresh.set(h, await listOf(h))` in step 5 (the pre-dial memo instead of a fresh list) | `spa/src/lib/peer-pairing-load.ts` | same two commands as M-A1, plus `npx vitest run src/lib/peer-pairing-load.test.ts -t "pending on Y's entry re-lists Y, not X"` | **PASS (NOT RED)** (the two named); FAIL (the Y-pending test) | The two named tests stayed green: X (`hM`) is listed in step 1 by `api.list` directly and never enters the `listOnce` memo, so `listOf(hM)` still issues a fresh call. The memo only aliases hosts listed in step 3, which is Y — and the Y-pending test catches it: `peer-pairing-load.test.ts:408: AssertionError: expected 1 to be 2 // Object.is equality` (`nth(calls, 'list:hA')`). |
| M-A3 | Moved the whole "Step 5" block (from the `// Step 5 (spec §7.3)` comment through its closing `emit({ ...snap }) }`) to directly above `const dials: Promise<void>[] = []`, i.e. the re-read runs before any `verify` is called; `await Promise.all(dials)` left where it was | `spa/src/lib/peer-pairing-load.ts` | `npx vitest run src/lib/peer-pairing-load.test.ts -t 're-lists X after BOTH dials settle'` and `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'push failed after its verify dial'` | FAIL (both) | `peer-pairing-load.test.ts:379: AssertionError: expected [ 'list:hM', 'list:hA', …(3) ] to deeply equal [ … ]` — received order `list:hM, list:hA, list:hM, verify:hM:air, verify:hA:mini-lab` (the second `list:hM` before both verifies; the final row would read `'current'`); `PeersSection.test.tsx:778: TestingLibraryElementError: Unable to find an element by: [data-testid="peer-inbound-cancel"]` — the DOM dump shows `data-offer="commit"`, "the peer is on the new token" and a `peer-inbound-commit` button rendered instead. |
| M-B | `const offer = rotationOffer(row)` → `const offer = row.rotation_pending ? 'commit' : null` | `spa/src/components/hosts/peers/RotationControls.tsx` | `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'push failed after its verify dial'` and `-t 'reload with rotation_pending'` | FAIL (push-failure); FAIL (2 of the 3 reload rows: `prev` and `''`; the `current` row passes by construction — its expected answer IS commit) | `PeersSection.test.tsx:778: TestingLibraryElementError: Unable to find an element by: [data-testid="peer-inbound-cancel"]`; `PeersSection.test.tsx:814: AssertionError: expected <button type="button" …(2)></button> to be null` (the `peer-inbound-commit` button rendered for `prev` and for `''`) |
| M-C | `{stale ? (` → `{false ? (` (the stale guard around the Commit/Cancel buttons removed) | `spa/src/components/hosts/peers/RotationControls.tsx` | `npx vitest run src/components/hosts/PeersSection.test.tsx -t "both sides pending, Y's re-list fails"` and `-t 'reload with rotation_pending'` | FAIL (F5); FAIL (2 of 3 reload rows: `current` and `prev`; the `''` row has no button either way) | `PeersSection.test.tsx:831: Error: expect(element).toHaveTextContent() — Expected element to have text content: could not re-read after the dial — Received:` (the stale note is gone; the outbound line rendered a button instead); `PeersSection.test.tsx:805` / `:806: AssertionError: expected <button type="button" …(3)></button> to be null` (a Commit / Cancel button was present on the pre-dial paint, where `gateStale.entry` is still true) |
| M-D | `{ method: 'POST' }` → `{ method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ force: true }) }` in `commitRotation` | `spa/src/lib/host-api.ts` | `npx vitest run src/lib/host-api.peers.test.ts -t 'commitRotation POSTs'`, `-t 'no wrapper ever sends'`, and `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'inbound line: mint'` (exercises `assertNoForce` in `afterEach`) | FAIL (wrapper, both); **PASS (NOT RED)** (component `assertNoForce`) | `host-api.peers.test.ts:237: AssertionError: expected '{"force":true}' to be undefined`; `host-api.peers.test.ts:281: AssertionError: expected '{"force":true}' not to contain 'force'`. The component's `assertNoForce` stayed green: `PeersSection.test.tsx` mocks `commitRotation` as `vi.fn()` (line 19, `vi.mock('../../lib/host-api', …)`), so the mutated wrapper body never executes there; `assertNoForce` inspects the mock's **arguments** `(hostId, alias)`, which guard the page-level contract (no third arg / no `force` from the component), not the wrapper's request body. The wrapper tests are the ones that own that body, and they went red. |
| M-E1 | In `actionApi()`: `add: addPeerHost,` → `add: (h, b) => addPeerHost(h, b).then((r) => { useHostStore.setState({ lastToken: r.inbound_token } as never); return r }),` plus `import { useHostStore } from '../../../stores/useHostStore'` | `spa/src/components/hosts/peers/flow.ts` | `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'happy path: the three calls in order'` | FAIL (in `afterEach` → `assertNoTokenLeak`) | `PeersSection.test.tsx:113: AssertionError: expected '{"hosts":{"hM":{"id":"hM","name":"mla…' not to match /pdxp_/` — the received store JSON ends with `"lastToken":"pdxp_b2b2…b2"` |
| M-E2 | Same wrapper, `localStorage.setItem('pdx-last-token', r.inbound_token)` in place of the store write (no import) | `spa/src/components/hosts/peers/flow.ts` | same command | FAIL (in `afterEach` → `assertNoTokenLeak`) | `PeersSection.test.tsx:113: AssertionError: expected 'pdxp_b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2' not to match /pdxp_/` (the `localStorage.getItem(k)` branch) |
| M-F | `if (!pushed.ok) return { kind: 'push-failed', error: msg(pushed.error) }` → `if (!pushed.ok) return { kind: 'pushed' }` in `rotateDirection` | `spa/src/lib/peer-pairing-actions.ts` | `npx vitest run src/lib/peer-pairing-actions.test.ts -t 'push throws'` | FAIL | `peer-pairing-actions.test.ts:136: AssertionError: expected { kind: 'pushed' } to deeply equal { kind: 'push-failed', …(1) }` (`- "error": "entry changed concurrently"`, `- "kind": "push-failed"`, `+ "kind": "pushed"`) |
| M-G | `if (!repair) undoError = await undoOnY(y.hostId, minted.value as PeerHostAdded, api, report)` → `if (!repair) undoError = ''` in `pairHosts` step-2 failure | `spa/src/lib/peer-pairing-actions.ts` | `npx vitest run src/lib/peer-pairing-actions.test.ts -t 'step 2 502'`, `-t 'step 2 409 → undo delete on Y first'`, and `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'step 2 fails → the step-1 entry on Y is deleted'` | FAIL (all three) | `peer-pairing-actions.test.ts:249: AssertionError: expected [ …(2) ] to deeply equal [ …(4) ]` (missing `"list:A"`, `"delete:A:mini-lab"`); `peer-pairing-actions.test.ts:268: AssertionError: expected [] to deeply equal [ 'list:A', 'delete:A:mini-lab' ]`; `PeersSection.test.tsx:429: AssertionError: expected "vi.fn()" to be called with arguments: [ 'hA', 'mini-lab' ]` (`deletePeerHost` never called) |
| M-H | `return s.ok \|\| status(s.error) === 404 ? '' : msg(s.error)` → `return s.ok ? '' : msg(s.error)` in `unpairHosts.del` | `spa/src/lib/peer-pairing-actions.ts` | `npx vitest run src/lib/peer-pairing-actions.test.ts -t 'X 404 \+ Y 204'` (the `+` must be escaped — `-t` is a regex) and `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'a 404 on either side is already done'` | FAIL (both) | `peer-pairing-actions.test.ts:529: AssertionError: expected { xError: 'unknown alias', yError: '' } to deeply equal { xError: '', yError: '' }`; `PeersSection.test.tsx:669: AssertionError: expected <span …(2)></span> to be null` (a flow error span rendered for the 404) |
| M-I | `api.add(x.hostId, { url: y.url, token: tY, …` → `api.add(x.hostId, { url: x.url, token: tY, …` in `pairHosts` step 2 | `spa/src/lib/peer-pairing-actions.ts` | `npx vitest run src/lib/peer-pairing-actions.test.ts -t 'happy: add on Y'` and `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'happy path: the three calls in order'` | FAIL (both) | `peer-pairing-actions.test.ts:181: AssertionError: expected [ …(3) ] to deeply equal [ …(3) ]` (`- "add:X:alias=none:url=http://100.64.0.4:7860:token=same:A"`, `+ "add:X:alias=none:url=http://100.64.0.2:7860:token=same:A"`); `PeersSection.test.tsx:402: AssertionError: expected 2nd "vi.fn()" call to have been called with [ 'hM', { …(2) } ]` |
| M-J | `? api.rotate(y.hostId, y.returnEntry.alias)` → `? api.add(y.hostId, { alias: y.returnEntry.alias, url: x.url })` in `pairHosts` step 1 (repair branch) | `spa/src/lib/peer-pairing-actions.ts` | `npx vitest run src/lib/peer-pairing-actions.test.ts -t "rotate on Y's entry instead of add"` and `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'the repair case is noted'` | FAIL (both) | `peer-pairing-actions.test.ts:398: AssertionError: expected { kind: 'step-failed', …(3) } to deeply equal { kind: 'paired', …(2) }` (received `error: "unexpected A"`, `step: "rotate-on-y"` — the fixture's `add` mock has no answer for Y); `PeersSection.test.tsx:553: TestingLibraryElementError: Unable to find an element by: [data-testid="peer-row-air26"]` |
| M-K | Inserted `if (row.last_inbound_auth === '') return 'commit'` after the `'prev'` line in `rotationOffer` | `spa/src/lib/peer-pairing.ts` | `npx vitest run src/lib/peer-pairing.test.ts -t 'rotationOffer'` | FAIL (1 of 8: the `''` table row) | `peer-pairing.test.ts:164: AssertionError: expected 'commit' to be 'none' // Object.is equality` (row "pending, peer has not dialled since → neither (Refresh only)") |
| M-L | The `.map((r) => ({ ...r, rotation_pending: …, last_inbound_auth: … }))` normalisation replaced by `return body.hosts ?? []` in `listPeerHosts` | `spa/src/lib/host-api.ts` | `npx vitest run src/lib/host-api.peers.test.ts -t 'normalises a pre-391 row'` and `-t 'coerces an unknown last_inbound_auth'` | FAIL (both) | `host-api.peers.test.ts:122: AssertionError: expected undefined to be false // Object.is equality` (`rows[0].rotation_pending`); `host-api.peers.test.ts:139: AssertionError: expected 'yes' to be false // Object.is equality` |
| M-M | `const blocked = c.listError !== '' \|\| pendingReturn` → `const blocked = pendingReturn` in `CandidateLine` | `spa/src/components/hosts/peers/PairWithSection.tsx` | `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'could not be read is listed with Pair disabled'` | FAIL | `PeersSection.test.tsx:529: Error: expect(element).toBeDisabled() — Received element is not disabled: <button … data-testid="peers-pair-hA" …>` |
| M-N (codex) | Deleted `if (offer !== 'commit') return { kind: 'repair-pending', aliasOnX, aliasOnY, offer, commitError: '' }` in the `pairHosts` repair finish (commit sent whatever the fresh row said) | `spa/src/lib/peer-pairing-actions.ts` | `npx vitest run src/lib/peer-pairing-actions.test.ts -t 'repair finish: the fresh row says'` | FAIL (2/2: the `""` and `"prev"` rows) | `peer-pairing-actions.test.ts:416` and `:425: AssertionError: expected { kind: 'paired', …(2) } to deeply equal { kind: 'repair-pending', …(4) }` (`- "offer": "none"` / `- "offer": "cancel"`, `+ "kind": "paired"` — the commit was sent on `''` and on `prev`) |
| M-O (codex) | `const report: Report = (step) => { if (mine()) setFlow({ … }) }` → `const report: Report = (step) => { setFlow({ … }) }` in `PeersSection.runFlow` | `spa/src/components/hosts/PeersSection.tsx` | `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'neither locks nor repaints the new page'` | FAIL | `PeersSection.test.tsx:911: Error: expect(element).toBeEnabled() — Received element is not enabled: <button … data-testid="peers-refresh" disabled="" …>` (M's late `report('push')` locked A's page) |

17 sub-rows over 15 mutations. 14 mutations went red on at least one named
test; M-A went red only in its M-A3 form.

**Deviations from the Task 7 brief, already noted inline and repeated here
for visibility:**

- **M-A1 / M-A2 — not red on the two named tests.** The brief offered two
  edits for M-A: "move the block" and "replace the fresh `api.list` with the
  pre-dial `listOf` memo". Both stateful fixtures (`statefulApi` in the loader
  test, `seedRotation` in the component test) record a dial at the moment the
  `verify` mock is *invoked*, synchronously, not when its promise settles. So
  any edit that still starts the verifies before the re-read — including
  M-A1, which only removes the `await` so the re-read runs concurrently with
  the in-flight dials — is read by the fixture as "after the dial". M-A2 is
  invisible for a second reason: X's own list never goes through the
  `listOnce` memo (step 1 calls `api.list(x.hostId)` directly), so `listOf(hM)`
  is still a fresh call; the memo edit is caught only where the memo has an
  entry, i.e. by the Y-pending test (`:408`, red). **M-A3** — the re-read
  moved to before the verifies are *called* — is the "read the row BEFORE the
  dial" the spec §8.4 names, and it goes red on both named tests exactly as
  the plan predicts (`'current'` in the final row; Commit rendered instead of
  Cancel). What the fixtures do not cover is the *concurrent* read (M-A1): a
  fixture that flips on settle rather than on call would need to hold the
  verify promise and flip inside `.then` (the `reload with rotation_pending`
  tests already park the promise but flip nothing). No test was added, per
  the task rules; this is a candidate for a follow-up.
- **M-D — the component `assertNoForce` is not red**, only the two
  `host-api.peers.test.ts` wrapper tests are. `PeersSection.test.tsx` replaces
  `commitRotation` with `vi.fn()` (its `vi.mock` at line 9–21), so a mutation
  inside the wrapper's body never runs under the component test; the
  `afterEach` guard inspects mock **call arguments** and guards the page → 
  wrapper contract (two args, no `force` from the component), which this
  mutation does not touch. The wrapper tests own the request body and both
  fail as the plan expects. Not a missing test — a different boundary.
- **M-B / M-C partial red on the `reload with rotation_pending` table** is by
  construction, not a gap: M-B (always `'commit'`) cannot fail the `current`
  row whose expected answer is Commit; M-C (buttons even when stale) cannot
  fail the `''` row where `rotationOffer` gives `'none'` and there is no
  button to leak. The other rows of each table fail.

Every mutation was applied as one edit (Edit tool or a literal `sed -i ''`),
run against only the named test(s), and undone by the reverse edit; the tree
was verified clean (`git status --short` → only `.playwright-cli/`) after each
undo, and the five test files were run once more at the end: 193/193 green.

## Addendum (HEAD after `72ce347d`): M-A1 closed

The `statefulApi` fixture in `peer-pairing-load.test.ts` now logs two markers per dial: `verify:<h>:<a>` when the dial is **sent** (kept for the order assertions) and `dialled:<h>:<a>` only once it has **settled**, one macrotask later; the stateful `list` answers flip on `dialled:`, not `verify:`. Re-run of M-A1 (`await Promise.all(dials)` → `const MUT_dials = Promise.all(dials)` at line 191, `await MUT_dials` inserted before the final `return { ...snap }`):

| # | Mutation | File | Command | Result | Failing assertion |
|---|---|---|---|---|---|
| M-A1′ | as M-A1 (re-read concurrent with the dials) | `spa/src/lib/peer-pairing-load.ts` | `npx vitest run src/lib/peer-pairing-load.test.ts -t 'after BOTH dials settle'` | **FAIL** | `AssertionError: expected 4 to be greater than 6` — `calls.lastIndexOf('list:hM')` is no longer after `calls.indexOf('dialled:hA:mini-lab')` |

Reverted; 37/37 green; `git status --short` shows only the test file (this addendum's change) before commit.
