# Plan — Peer information in the tab panel and the status bar

Spec: `2026-09-16-peer-info-panel-spec.md` v2
Date: 2026-09-16
Branch: `worktree-peer-info-panel`
Baseline: `origin/main` @ `1.0.0-alpha.364`; `cd spa && npx vitest run` green
(449 files / 5826 tests) before task 1.

SPA-only. No Go, no Electron. Six tasks, each TDD, each its own commit.

## Conventions for every task

- Work in `/Users/wake/Workspace/wake/purdex/.claude/worktrees/worktree-peer-info-panel`.
  Every `Bash` call is prefixed `cd <that path> && `.
- `git commit --only <files>`; never `git add -A`.
- End of each task: `cd spa && pnpm run lint && npx vitest run`. End of the
  last one also `pnpm run build` — it catches type errors that neither lint
  nor vitest sees (it caught a `TS18047` in the previous PR).
- New strings go in **both** `spa/src/locales/en.json` and `zh-TW.json`.
- Follow the existing store shape (`useSessionStore`, `useHostStore`): zustand,
  plain async actions, no middleware beyond what those already use.

---

## Task 1 — `usePeerStore`

**Files:** `spa/src/stores/usePeerStore.ts`, `usePeerStore.test.ts`,
`spa/src/lib/host-api.ts` (+ its test), `spa/src/lib/host-lifecycle.ts`
(+ its test)

`fetchPeers(hostId): Promise<PeersEnvelope>` in `host-api.ts`, following the
file's existing shape (`hostFetch`, throw on `!res.ok`). Declare the wire
types (`PeersEnvelope`, `PeerRecordWire`) next to the other wire types in that
file, and add a contract test that a realistic envelope — copy one from
`pdx peers --json` — parses into the fields the store reads.

**`forgetHost` must be wired, not merely offered.** `spa/src/lib/host-lifecycle.ts`
already cascades host removal through the session, agent, stream, execution
and host-settings stores; a `forgetHost` nobody calls satisfies nothing. Two
call sites:

- host removal → the existing cascade, beside the other stores;
- `useHostStore.updateHost` when **ip, port or token** changes — a cached
  address belongs to a daemon identity. A *name* change must not clear it.

The cwd store (Task 2) gets the same treatment in the same cascade; decide it
once, here, rather than discovering the asymmetry later.

Store per spec §3.1. Three details that are the whole point of the task:

- **index only `row_kind === 'session' && session_code !== ''`** — positively,
  not by assuming entry rows are empty (spec §3.1);
- **dedupe**: a second `refresh(h)` while one is in flight returns the same
  promise and issues no second request;
- **`forgetHost(h)`** clears one host without touching others.

**Tests first:**

1. indexes session rows by code; an `inbox_dead` row and an `ambiguous` row
   are indexed (they are session rows and keep their codes); an entry row with
   `session_code: ''` is dropped; a hypothetical entry row *with* a code is
   also dropped, because the rule is `row_kind`, not emptiness.
2. ten concurrent `refresh('h1')` → one fetch; a later `refresh('h1')` after
   it settles → a second fetch.
3. `refresh('h1')` and `refresh('h2')` are independent.
4. `fetchedAt` set on success; `error` set and previous rows **kept** on
   failure (a failed refresh must not blank a working display).
5. envelope flags stored: `partial`, `labels_unavailable`,
   `unknown_registry_files`.
6. `forgetHost('h1')` leaves `h2` intact.
7. **wiring**: removing a host clears its peer rows (drive the real cascade,
   not the store directly); `updateHost` changing **ip / port / token** clears
   them; changing only the **name** does not.
8. the contract test above: a real `pdx peers --json` envelope parses.

Do not key the store by `compositeKey(hostId, sessionCode)`. `composite-key.ts`
joins with a colon and a `hostId` can itself contain one (`mini-lab:278cbm`),
so anything that later splits the key is a bug waiting to happen. The store is
nested: `byHost[hostId].rows[sessionCode]`.

**Commit:** `feat(spa): peer store over GET /api/peers`

---

## Task 2 — `useSessionCwdStore`

**Files:** `spa/src/stores/useSessionCwdStore.ts`, `useSessionCwdStore.test.ts`

Per `(hostId, sessionCode)` via the existing `fetchSessionCwd`. Same dedupe
and error-keeps-previous rules as Task 1.

Read spec §2.1 before writing this: the reason for a whole second store is
that `Session.cwd` is `#{session_path}` and does not follow a `cd`. If that
distinction is lost, the store is pointless.

**Tests first:**

1. fetches once per `(host, code)`; concurrent calls dedupe.
2. two sessions on one host are independent.
3. failure keeps the previous value and records the error.
4. **the distinction**: given a session whose `Session.cwd` is `/start/dir`
   and whose `/api/sessions/{code}/cwd` answers `/somewhere/else`, the store
   yields `/somewhere/else`. This is the test that stops someone "simplifying"
   the store away later.

`fetchSessionCwd` resolves to `SessionCwd` — `{ cwd, tmuxInstance }`, not a
bare string (`host-api.ts`). The store keeps `cwd` and **drops**
`tmuxInstance`: it exists so the rebuild probe can refuse to write a stranger's
directory into an old pane's record, and this feature only displays. Mocks in
the tests must return the object shape, or they will pass against code that
could not work.

**Commit:** `feat(spa): session cwd store over the pane-current-path endpoint`

---

## Task 3 — the fetch policy

**Files:** `spa/src/hooks/usePeerInfo.ts`, `usePeerInfo.test.ts`

One hook both landing sites use: given `(hostId, sessionCode)` it returns the
row, the cwd, loading/error/staleness, and a `refresh()`.

The trigger is spec §3.3's predicate, and it belongs **here**, not in a
component — that is what makes §8.2's UI test possible to write:

- fetch peers when this hook needs host H, `runtime[H].status === 'connected'`,
  and `byHost[H]` is absent;
- never on tab switch when the host is already fetched;
- never for `disconnected` / `reconnecting` / `auth-error`;
- **do** fetch for `connected` + `tmuxState === 'unavailable'`.

**Tests first:**

1. first render for a connected, unfetched host → exactly one `refresh`.
2. re-rendering with a different `sessionCode` on the same host → **zero**
   further `refresh` calls. (Spec §8.2's UI half.)
3. each non-connected status → zero calls; `tmuxState: 'unavailable'` → one;
   **`tmuxState: undefined` → one**. That is not a corner case but the normal
   opening moment: `useMultiHostEventWs` sets `status: 'connected'` on WS open
   and only sets `tmuxState` when a tmux event arrives, so every host passes
   through `connected` + `undefined`. Treating undefined as "not ok" would
   mean the first render after connecting never fetches.
4. `refresh()` from the hook always issues one, regardless of cache age.
5. staleness: `fetchedAt` 30 s ago → fresh; 90 s ago → stale; loading keeps
   the previous row visible.

**Commit:** `feat(spa): one hook owns when peer data is fetched`

---

## Task 4 — the status bar

**Files:** `spa/src/components/StatusBar.tsx`, `StatusBar.test.tsx`, locales

Segments, separators (`border-l` spacer, never a `|` glyph), copy buttons, the
single refresh control, the fixed feedback slot, and §4.3's truncation order.

**The row needs restructuring, not just classes.** It is one flat
`flex gap-3`, and `ml-auto` currently hangs off whichever of pane title / view
controls happens to be present. Dropping three more segments plus a feedback
slot into that will not behave. Restructure into three explicit containers:

| Container | Rules |
|---|---|
| left: the segment group | `min-w-0`, `flex`, holds host · cwd · agent · peer id · status and the separators |
| middle: the flexible gap | absorbs the slack; the feedback slot lives at its start with a fixed width so nothing reflows on copy |
| right: the controls | `shrink-0`, `ml-auto` lives **here** and nowhere else — pane title, upload status, split buttons, view mode |

Each segment is `min-w-0` with its own `truncate`; `status` and the view
control are `shrink-0`. Behaviour of the model badge, upload status, split
buttons and view mode is otherwise unchanged — they only move into the right
container and gain drop classes.

**Tests first:**

1. renders host, cwd, peer name, peer id, status, each in its own element.
2. each copy button copies §4.2's value — **peer id copies the full address,
   not the label** — and shows the confirmation in the fixed slot.
3. `copyText` rejecting shows the distinct failure message.
4. a stale peer id is dimmed **and still copies** on click (it does not
   refresh — spec §3.4).
5. the refresh control calls `refresh()` once; disabled while loading and when
   the host is not connected.
6. keyboard: each button is focusable and fires on Enter and on Space.
7. narrow layout: with an active upload, a pane title and every segment
   present, the responsive decisions match §4.3 — `cwd`, peer name and pane
   title dropped, `status` and the view control kept. **Name the test for what
   it proves**: `renders the narrow-width decisions (jsdom does no layout — see
   the manual 400px check)`. jsdom has no layout engine, so this cannot and
   must not be called an overflow test; the real one is in Verification.
8. the early-return branches never fetch: `activeTab === null`, an editor
   pane, and a non-tmux pane each render without calling `refresh`.
9. the existing tests for host/session-name/model-badge/upload still pass
   unchanged; any that must change are listed in the commit body with why.

**Commit:** `feat(spa): status bar shows and copies peer information`

---

## Task 5 — the tab panel

**Files:** `spa/src/components/RenamePopover.tsx`, `RenamePopover.test.tsx`,
locales

A peer section inside `PaneDetailBlock`, per spec §5, with per-block loading
and error. Refresh on open — **both** stores, peers and cwd, matching the
refresh control in Task 4; the panel is where the cwd is read in full.

**Stream panes are out of scope here, deliberately.** `collectRenameTargets`
(`spa/src/features/workspace/hooks.ts`) returns `mode === 'terminal'` panes
only, so the panel has no block for a stream pane to put a peer section in.
Spec §6 was narrowed rather than the collector widened: changing what the
panel collects would hand stream panes name/cwd/resume rows as a side effect
of a read-only feature. The status bar covers stream panes instead.

**Refresh must fire on open, not on render.** `RenamePopover` recomputes its
targets every render, and `useClickOutside` binds `mousedown`. Put the refresh
in a `useEffect` keyed on the distinct host set —
`Array.from(new Set(targets.map((t) => t.hostId))).sort().join('\0')` — so it
runs once per open and once more only if the set genuinely changes. The
refresh control stays inside the popover container, or `mousedown` will close
the popover before its `click` lands.

**Tests first:**

1. address / agent / deliverable-reason render for a live pane.
2. `labelSource: 'default'` shows the marker; `'user'` does not.
3. `deliverable: false` shows the **reason**, not a bare "no".
4. a terminated pane has no peer section.
5. two blocks on two hosts: one host failing shows an error in that block
   only, and the other block still renders its address.
6. opening the panel triggers one refresh per distinct host among its panes.
7. §6's table rows that concern the panel: partial-with-row, partial-without-row
   ("could not be determined", **not** "no peer"), `labels_unavailable`,
   `label: ''`, session not yet in `useSessionStore` (spinner, **no** error).
8. a re-render with unchanged targets does **not** re-refresh; a re-render
   after the host set changes does.
9. clicking the refresh control inside the popover does not close it.
10. a tab whose primary pane is not a tmux session still renders peer sections
    in the blocks whose panes are.

**Commit:** `feat(spa): tab panel shows peer address, agent and deliverability`

---

## Task 6 — i18n key parity

**Files:** `spa/src/locales/en.json`, `zh-TW.json`, a new locale test

Spec §8.9 has no task of its own in v1 of this plan. One test asserts the two
files have **identical key sets** for the namespaces this feature adds, so a
string added to one and forgotten in the other fails rather than silently
rendering a key to whichever half of the fleet runs the other locale.

If the repo already has such a test, extend it instead of adding a second.

**Commit:** `test(spa): locale key parity for the peer namespace`

---

## Verification before the PR

```
cd spa && pnpm run lint && npx vitest run && pnpm run build
```

Then a **live check against the running daemon**, which this feature makes
easy and which the previous two PRs showed is where the real bugs are: the
dev SPA is served from this host, so load it, open a tab on `mini-lab`, and
confirm the status bar shows the real address (`mini-lab/<tmux name>:…`) and
that copying it yields something `pdx msg send` accepts. Paste the evidence
into the PR.

**The 400 px check is manual and belongs in a real browser**, because jsdom
does no layout and Task 4's test says so in its own name. The repo has
`playwright cli` (see the global CLAUDE.md; `-s=<session>` isolates it):

```
playwright cli -s=peer-info open https://<site>   # or the dev server URL
playwright cli -s=peer-info resize 400 800
# then evaluate: document.querySelector('[data-testid=status-bar]')
#   → scrollWidth <= clientWidth
playwright cli -s=peer-info close
```

Paste the measured numbers into the PR. If the dev SPA cannot be reached from
here, say so in the PR rather than claiming the check passed.

**Also not verifiable here:** the 2 s cost is real, so the first render of a
host's peer id genuinely takes that long — worth watching once by eye to
confirm it does not block the rest of the status bar.

## Codex plan review disposition (`task-mu48owc6-d99ewm`)

| # | Severity | Finding | Disposition |
|---|---|---|---|
| B1 | Blocker | `forgetHost` was offered but never wired; the removal cascade and `updateHost` would not call it | **Accepted** — Task 1 names both call sites and tests them, and settles the cwd store the same way |
| B2 | Blocker | Task 5 omits stream panes, contradicting spec §6 | **Accepted, by narrowing the spec** — the panel renders no block for stream panes at all, and widening `collectRenameTargets` would give them rename rows as a side effect of a read-only feature. Spec §6 amended; the status bar covers the case |
| M1 | Major | `connected` + `tmuxState: undefined` is the normal opening state and was untested | **Accepted** — Task 3 test 3 |
| M2 | Major | "just add classes" understates the flex restructure | **Accepted** — Task 4 specifies three containers and where `ml-auto` lives |
| M3 | Major | A jsdom test cannot prove no overflow | **Accepted** — the test is renamed for what it proves, and a real 400 px measurement moves to Verification |
| M4 | Major | Refresh-on-open needs stable effect deps and must not fight `useClickOutside` | **Accepted** — Task 5, with the host-set key spelled out |
| M5 | Major | `fetchSessionCwd` returns `{cwd, tmuxInstance}`, not a string | **Accepted** — Task 2 |
| m1 | Minor | Do not key by `compositeKey`; `hostId` can contain a colon | **Accepted** — Task 1 |
| — | Omission | No task for spec §8.9's i18n key parity | **Accepted** — Task 6 |
| — | Omission | Early-return branches untested for "does not fetch" | **Accepted** — Task 4 test 8 |
| — | Omission | Panel spinner state and non-tmux primary pane untested | **Accepted** — Task 5 tests 7 and 10 |
| — | Omission | Panel refresh should cover the cwd store too | **Accepted** — Task 5 opening |
| — | Omission | Wire types and a contract test for `/api/peers` | **Accepted** — Task 1 |

## Risks

| Risk | Mitigation |
|---|---|
| The status bar starts a 2 s fetch on every tab switch | Task 3 test 2 is the tripwire, and the policy lives in the hook precisely so that test can exist |
| A stale address gets copied and sent to the wrong agent | Dimming past 60 s + the panel refreshing on open; spec §3.4 records why this matters |
| The row overflows at 400 px | Task 4 test 7, with an upload present — the segment most likely to be forgotten |
| The two new stores drift into one | They have different costs (2 s vs one tmux call) and different triggers; Task 2 test 4 pins why the cheap one exists |
