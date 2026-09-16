# Plan — Peer information in the tab panel and the status bar

Spec: `2026-09-16-peer-info-panel-spec.md` v2
Date: 2026-09-16
Branch: `worktree-peer-info-panel`
Baseline: `origin/main` @ `1.0.0-alpha.364`; `cd spa && npx vitest run` green
(449 files / 5826 tests) before task 1.

SPA-only. No Go, no Electron. Five tasks, each TDD, each its own commit.

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
`spa/src/lib/host-api.ts` (one new call)

`fetchPeers(hostId): Promise<PeersEnvelope>` in `host-api.ts`, following the
file's existing shape (`hostFetch`, throw on `!res.ok`).

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
3. each non-connected status → zero calls; `tmuxState: 'unavailable'` → one.
4. `refresh()` from the hook always issues one, regardless of cache age.
5. staleness: `fetchedAt` 30 s ago → fresh; 90 s ago → stale; loading keeps
   the previous row visible.

**Commit:** `feat(spa): one hook owns when peer data is fetched`

---

## Task 4 — the status bar

**Files:** `spa/src/components/StatusBar.tsx`, `StatusBar.test.tsx`, locales

Segments, separators (`border-l` spacer, never a `|` glyph), copy buttons, the
single refresh control, the fixed feedback slot, and §4.3's truncation order.

Do not touch the model badge, upload status, split buttons or the view-mode
control beyond adding their truncation/drop classes.

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
7. 400 px: no horizontal overflow with an active upload, a pane title and all
   segments present; `cwd`, peer name and pane title absent; `status` and the
   view control present. (jsdom cannot measure layout — assert the applied
   classes/`hidden` decisions from a width prop or a `useElementWidth`-style
   seam, and say so in the test name so nobody mistakes it for a real layout
   assertion.)
8. the existing tests for host/session-name/model-badge/upload still pass
   unchanged; any that must change are listed in the commit body with why.

**Commit:** `feat(spa): status bar shows and copies peer information`

---

## Task 5 — the tab panel

**Files:** `spa/src/components/RenamePopover.tsx`, `RenamePopover.test.tsx`,
locales

A peer section inside `PaneDetailBlock`, per spec §5, with per-block loading
and error. Refresh on open.

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
   `label: ''`, session not yet in the store.

**Commit:** `feat(spa): tab panel shows peer address, agent and deliverability`

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

**Not verifiable here:** the 2 s cost is real, so the first render of a host's
peer id genuinely takes that long — worth watching once by eye to confirm it
does not block the rest of the status bar.

## Risks

| Risk | Mitigation |
|---|---|
| The status bar starts a 2 s fetch on every tab switch | Task 3 test 2 is the tripwire, and the policy lives in the hook precisely so that test can exist |
| A stale address gets copied and sent to the wrong agent | Dimming past 60 s + the panel refreshing on open; spec §3.4 records why this matters |
| The row overflows at 400 px | Task 4 test 7, with an upload present — the segment most likely to be forgotten |
| The two new stores drift into one | They have different costs (2 s vs one tmux call) and different triggers; Task 2 test 4 pins why the cheap one exists |
