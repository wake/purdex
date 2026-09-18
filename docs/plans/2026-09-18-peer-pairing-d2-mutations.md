# D2 Peer Pairing — Mutation-Test Record

- Branch: `worktree-peer-pairing-d2`
- HEAD sha: `b858ea904780d90fdb9c3faa7f43fe42b26a0330`
- Date: 2026-09-18

Task 6 proves the tests written for Peer Pairing D2 guard what they claim
(spec §8.2: mutation tests are a deliverable). Each row below was applied as
a temporary one-edit mutation to `spa/src/lib/host-api.ts`,
`spa/src/lib/peer-pairing.ts`, `spa/src/lib/peer-pairing-load.ts`, or
`spa/src/components/hosts/PeersSection.tsx`, run against the named test(s)
with `npx vitest run <file> -t '<title>'` (no cache — vitest has none by
default, and `--changed` was never used), confirmed the result, then reverted
with `git checkout -- <file>` before the next row. `git status --short` was
empty both before this record was started and after all 24 rows.

Two facts noted in the task brief and confirmed unchanged at this HEAD:
`PeersSection`'s `api` table is built inside `run()`, not at module load;
`DirectionLine` shows the self alias whenever known, with the drift
marker/button only on an actual drift. Neither affected M15/M16/M17, which
still apply exactly as written in the task-6 brief.

| # | Mutation (exact edit) | File | Command | Result | Failing assertion (as printed) |
|---|---|---|---|---|---|
| M1 | `if (inbound === 'not-app-host') return 'outbound-only'` → `return 'bidirectional'` in `pairStatus` | `spa/src/lib/peer-pairing.ts` | `npx vitest run src/lib/peer-pairing.test.ts -t 'ok / not-app-host'` and `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'a peer that is not an App host renders outbound-only'` | FAIL (both) | `peer-pairing.test.ts:91: AssertionError: expected 'bidirectional' to be 'outbound-only'`; `PeersSection.test.tsx:163: expect(element).toHaveAttribute("data-status", "outbound-only") // element.getAttribute("data-status") === "outbound-only"` (timed out waiting, status never reached `outbound-only`) |
| M2 | `if (inbound === 'counterpart-unavailable') return 'return-unknown'` → `return 'outbound-only'` in `pairStatus` | `spa/src/lib/peer-pairing.ts` | `npx vitest run src/lib/peer-pairing.test.ts -t 'ok / counterpart-unavailable'` and `-t 'outbound-only and return-unknown are never the same word'` | FAIL (both) | `peer-pairing.test.ts:91: AssertionError: expected 'outbound-only' to be 'return-unknown'`; `peer-pairing.test.ts:94: AssertionError: expected 'outbound-only' not to be 'outbound-only'` |
| M3 | Deleted the leading `if (outbound === 'pending' \|\| inbound === 'pending') return 'checking'`, cast `outbound` with `as { ok?: boolean }` so the rest still runs | `spa/src/lib/peer-pairing.ts` | `npx vitest run src/lib/peer-pairing.test.ts -t 'pending'` | FAIL (4/4 pending rows) | `peer-pairing.test.ts:91`, four failures: `pending / ok → pending` got `'one-way'`; `ok / pending → …` got `'one-way'`; `pending / not-app-host → pending` got `'unpaired'`; `pending / pending → pending` got `'unpaired'` |
| M4 | In `matchCounterpart`, replaced the guarded by-id block (`if (entry.host_id !== '') { const byId = hosts.find((h) => h.host_id !== '' && h.host_id === entry.host_id); if (byId) return byId }`) with the unconditional `const byId = hosts.find((h) => h.host_id === entry.host_id); if (byId) return byId` | `spa/src/lib/peer-pairing.ts` | `npx vitest run src/lib/peer-pairing.test.ts -t 'both host_id-less match only by URL'` | FAIL | `peer-pairing.test.ts:50: AssertionError: expected { hostId: 'hU', host_id: '', … } to be null` |
| M5 | URL fallback condition `(entry.host_id === '' \|\| h.host_id === '')` → `true` in `matchCounterpart` | `spa/src/lib/peer-pairing.ts` | `npx vitest run src/lib/peer-pairing.test.ts -t 'same URL but a different KNOWN host_id is not a match'` | FAIL | `peer-pairing.test.ts:43: AssertionError: expected { hostId: 'hA', … } to be null` |
| M6 | URL fallback condition `(entry.host_id === '' \|\| h.host_id === '')` → `entry.host_id === ''` in `matchCounterpart` | `spa/src/lib/peer-pairing.ts` | `npx vitest run src/lib/peer-pairing.test.ts -t 'URL also joins an entry to a host whose host_id is unknown'` and `npx vitest run src/lib/peer-pairing-load.test.ts -t 'a disconnected App host still joins by URL'` | FAIL (both) | `peer-pairing.test.ts:39: AssertionError: expected null to be { hostId: 'hU', host_id: '', … }`; `peer-pairing-load.test.ts:146: AssertionError: expected null to deeply equal { hostId: 'hA', name: 'Air 2026' }` |
| M7 | Removed `.replace(/\/+$/, '')` from `normalizePeerUrl`'s `path` | `spa/src/lib/peer-pairing.ts` | `npx vitest run src/lib/peer-pairing.test.ts -t 'trailing slash trimmed'` and `-t 'URL is the fallback when the entry has no host_id'` | FAIL (first); **PASS (NOT RED)** (second) | `peer-pairing.test.ts:13: AssertionError: expected 'http://100.64.0.4:7860/' to be 'http://100.64.0.4:7860'`. The second test stayed green: its fixture URLs (`http://100.64.0.4:7860`, no explicit path) both parse to `pathname === '/'` regardless of a trailing slash in the raw input, so removing the trim does not change either side of that comparison — the mutation is real but this particular fixture happens not to exercise it. |
| M8 | Dropped `.toLowerCase()` on both sides of the equality check in `aliasDrift` | `spa/src/lib/peer-pairing.ts` | `npx vitest run src/lib/peer-pairing.test.ts -t 'case-insensitive equal is not drift'` | FAIL | `peer-pairing.test.ts:101: AssertionError: expected 'aIR' to be ''` |
| M9 | `if (h.status !== 'connected') return {…}` → `if (false) return {…}` in `loadMeta` | `spa/src/lib/peer-pairing-load.ts` | `npx vitest run src/lib/peer-pairing-load.test.ts -t 'a disconnected App host still joins by URL'` | FAIL | `peer-pairing-load.test.ts:147: AssertionError: expected 'list: unexpected hA' to be 'disconnected'` |
| M10 | `const theirs = await listOf(y.hostId)` → `const theirs = await api.list(y.hostId).catch((e) => (e instanceof Error ? e : new Error(String(e))))` (no memo) | `spa/src/lib/peer-pairing-load.ts` | `npx vitest run src/lib/peer-pairing-load.test.ts -t 'two entries pointing at the same daemon share ONE list'` | FAIL | `peer-pairing-load.test.ts:206: AssertionError: expected "vi.fn()" to be called 2 times, but got 3 times` |
| M11 | In `settle`, the reject handler `(e) => ({ ok: false as const, error: msg(e) })` → `(_e) => 'pending' as const` | `spa/src/lib/peer-pairing-load.ts` | `npx vitest run src/lib/peer-pairing-load.test.ts -t 'a rejected verify'` | FAIL | `peer-pairing-load.test.ts:216: AssertionError: expected 'pending' to deeply equal { ok: false, error: '404 unknown alias' }` |
| M12 | Removed the early `return snap` inside the `for (const [call, r] of failed)` loop (kept the `emit(snap)`) | `spa/src/lib/peer-pairing-load.ts` | `npx vitest run src/lib/peer-pairing-load.test.ts -t 'a failing settings'` | FAIL | `peer-pairing-load.test.ts:94: TypeError: Cannot read properties of undefined (reading 'alias')` at `peer-pairing-load.ts:80` (execution fell through to build `self` from the rejected `settings` result) |
| M13 | Deleted `emit({ ...snap })` right before the "Step 4" comment (the pre-dial emit) | `spa/src/lib/peer-pairing-load.ts` | `npx vitest run src/lib/peer-pairing-load.test.ts -t 'joins by host_id, finds the return entry, verifies both directions, ends bidirectional'` and `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'paints checking while the dials are out'` | FAIL (first); **PASS (NOT RED)** (second) | `peer-pairing-load.test.ts:67: AssertionError: expected { ok: true, self_alias: 'air26', … } to be 'pending'` (the first-emit / emit-count assertions). The component test stayed green: its held-promise fixture means the inbound settle (mocked to resolve immediately) still fires its own `emit({ ...snap })` while `outbound` is still `'pending'` in `snap.rows[0]`, so the visible "checking" paint is produced by that later emit regardless of whether the earlier, now-deleted emit ran. |
| M14 | `const emit = (s) => { if (gen.current === my) setSnap(s) }` → `const emit = (s) => { setSnap(s) }` in `PeersSection.run` | `spa/src/components/hosts/PeersSection.tsx` | `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'a result that lands after hostId changed is dropped'` | FAIL | `PeersSection.test.tsx:236: AssertionError: expected element to be null` (`peer-row-air` was still in the DOM — the abandoned run's late dial was allowed to paint) |
| M15 | `{ alias: drift }` → `{ alias: renameTarget.alias }` in `DirectionLine.rename`'s `updatePeerHost` call | `spa/src/components/hosts/PeersSection.tsx` | `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'Rename calls updatePeerHost'` | FAIL | `PeersSection.test.tsx:112`: `waitFor` timed out — `api.updatePeerHost` was never called with `{ alias: 'air26' }` (it was called with `{ alias: 'air' }`, the unchanged `renameTarget.alias`) |
| M16 | `setError(e instanceof HostApiError ? e.detail : …)` → `setError(null)` in `DirectionLine.rename`'s catch | `spa/src/components/hosts/PeersSection.tsx` | `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'a 409 on Rename shows the daemon message inline'` | FAIL | `PeersSection.test.tsx:124`: `findByTestId('peer-outbound-rename-error')` timed out — the error span never rendered because `error` state stayed `null` |
| M17 | On the return-line `DirectionLine`, `renameTarget={{ hostId: counterpart.hostId, alias: returnEntry.alias }}` → `renameTarget={{ hostId, alias: returnEntry.alias }}` (the outer prop, i.e. X's own hostId instead of the counterpart's) | `spa/src/components/hosts/PeersSection.tsx` | `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'Rename on the return line acts on the counterpart host'` | FAIL | `PeersSection.test.tsx:153`: `waitFor` timed out — `api.updatePeerHost` was never called with host `A` (it would have been called with `M` instead) |
| M18 | `peerHostError` gutted to never read the response body: `const detail: string \| undefined = undefined; return new HostApiError(res.status, res.statusText, detail)` | `spa/src/lib/host-api.ts` | `npx vitest run src/lib/host-api.peers.test.ts -t 'updatePeerHost surfaces a 409 with the daemon text in detail'` | FAIL | `host-api.peers.test.ts:78: AssertionError: expected 'ERR' to be 'alias "air26" is already used by another host'` (fell back to the mocked `statusText`, "ERR") |
| M19 | `this.detail = detail ?? statusText` → `this.detail = detail ?? ''` in `HostApiError`'s constructor | `spa/src/lib/host-api.ts` | `npx vitest run src/lib/host-api.peers.test.ts -t 'a non-JSON error body falls back to statusText in detail'` | FAIL | `host-api.peers.test.ts:83: AssertionError: expected HostApiError: 502 Bad Gateway {…} to match object { status: 502, detail: 'Bad Gateway' }` — actual `detail` was `''` |
| M20 | `fetchHostInfo`: `fetchInfo(hostId).then(peerHostJson<HostInfo>)` → `fetchInfo(hostId).then((r) => r.json())` (skips the non-2xx check) | `spa/src/lib/host-api.ts` | `npx vitest run src/lib/host-api.peers.test.ts -t 'fetchHostInfo rejects with HostApiError on a non-2xx'` | FAIL | `host-api.peers.test.ts:97: AssertionError: promise resolved "{ error: 'boom' }" instead of rejecting` |
| M21 | Added `<span>{host.token}</span>` inside the `peers-self` paragraph (deliberate D-8 leak) | `spa/src/components/hosts/PeersSection.tsx` | `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'never renders a token value'` | FAIL | `PeersSection.test.tsx:245: AssertionError: expected '<div>…<span>pdx_admin_secret_M_9f3k2q8w</span>…' not to contain 'pdx_admin_secret_M_9f3k2q8w'` |
| M22 | Deleted the entire `{snap?.self && (<p data-testid="peers-self">…</p>)}` block | `spa/src/components/hosts/PeersSection.tsx` | `npx vitest run src/components/hosts/PeersSection.test.tsx -t 'renders the §5.3 row'` | FAIL | `PeersSection.test.tsx:72: TestingLibraryElementError: Unable to find an element by: [data-testid="peers-self"]` |
| M23 | `matchReturnEntry` body → `return rows[0] ?? null` (skips the join entirely) | `spa/src/lib/peer-pairing.ts` | `npx vitest run src/lib/peer-pairing.test.ts -t 'returns the row whose host_id is ours'` and `-t 'falls back to URL only for a row with no host_id'` | FAIL (both) | `peer-pairing.test.ts:63: AssertionError: expected { alias: 'x', host_id: 'other:1', … } (the stranger row) to equal the "mlab" row`; `peer-pairing.test.ts:68: AssertionError: expected { alias: 'mini-lab', host_id: 'stranger:1', … } to be null` |
| M24 | `emit({ ...snap })` → `emit(snap)` for the pre-dial emit (aliases the object the settles later mutate `rows` on) | `spa/src/lib/peer-pairing-load.ts` | `npx vitest run src/lib/peer-pairing-load.test.ts -t 'joins by host_id, finds the return entry, verifies both directions, ends bidirectional'` | FAIL | `peer-pairing-load.test.ts:67: AssertionError: expected { ok: true, self_alias: 'air26', … } to be 'pending'` (`snaps[0]` and `snaps[1]` are now the same aliased object, so `snaps[0].rows[0].outbound` reads the post-settle value instead of `'pending'`) |

Every mutation was applied with a single Edit, run against only the named
test(s), and reverted with `git checkout -- <file>` immediately after; no two
mutations were ever applied at once, and none was committed. `git status
--short` was empty before this record was written and is empty again now
that only this record file has been added.

**Deviations from the task-6 brief, both already noted inline above and
repeated here for visibility:**

- **M7**: the second named assertion ("URL is the fallback when the entry
  has no host_id … trailing slash ignored") stays green under this mutation.
  Its fixture URLs have no explicit path component, so `new URL(...).pathname`
  is `'/'` for both the trailing-slash and non-trailing-slash forms — the
  comparison this test performs cannot distinguish "path trimmed" from "path
  defaulted", so it does not regress even though the trim was removed. The
  first named assertion (which compares the normalized values directly) does
  go red, confirming the trim is still exercised elsewhere.
- **M13**: the `PeersSection.test.tsx` "paints checking while the dials are
  out" test stays green under this mutation. Its fixture holds the outbound
  verify promise open while letting the inbound verify resolve immediately;
  the inbound settle's own `emit({ ...snap })` (added after the deleted
  pre-dial emit) is sufficient to paint the row with `outbound: 'pending'`,
  so the visible "checking" state does not depend on the specific emit this
  mutation removes. The `peer-pairing-load.test.ts` assertions for the same
  mutation (first-emit shape, emit ordering) do go red.

No other rows deviated; the remaining 22 all reproduced exactly the failure
the task-6 brief predicted.
