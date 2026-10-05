# Plan — shell polish (worker list close button, chrome buttons keep focus)

Spec: `docs/specs/2026-10-06-shell-polish-spec.md`. Its §1 decisions are the user's and are not reopened here.

Codex plan review (job `task-muvml2ew-m9919m`, gpt-5.6-sol, spec read with it) returned 3 findings, all applied: #1 existing scroller assertions (T2.3), #2 focus is only provable in a real browser (spec §5, T4 steps 2–4, 7, WebKit), #3 the 96 px claim is now measured (spec §3, T4 step 6).

Anchors measured on `8d428d4f` (origin/main alpha.485) in worktree `shell-polish`. SPA paths are relative to `spa/src/`.

## Working rules

- TDD: write the failing test first, run it and see it fail on the assertion it is meant to check, then write the code. One commit per task, with `git commit --only <files>`.
- Every subagent Bash command starts with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/shell-polish/spa && `.
- Gates before the PR:
  - `npx vitest run`
  - `pnpm run lint`
  - `npx tsc --noEmit -p tsconfig.app.json`
  - `pnpm run build`
- No new locale keys (the header reuses `nav.workers` and `common.close`).
- Icons come from Phosphor only.
- Mutation testing is a deliverable: each task reports which mutation turned which test red.

## T1 — the helper and the bottom buttons (rule F, part 1)

Files: `lib/keep-focus.ts` (new), `lib/keep-focus.test.ts` (new), `features/workspace/components/BottomNav.tsx`, `features/workspace/components/BottomNav.test.tsx`.

1. `lib/keep-focus.ts`: export one mousedown handler, e.g. `keepFocus(e: React.MouseEvent)`, which calls `e.preventDefault()`. Its comment says why: a chrome button should not take focus from the pane on a mouse press, because the button then keeps focus, shows the focus ring on the next key, and Space re-fires it (spec §2). Keyboard focus is unaffected.
2. Test it: a button with `onMouseDown={keepFocus}`, where `fireEvent.mouseDown` returns `false`.
3. `BottomNav.tsx`: add `onMouseDown={keepFocus}` to every `<button>`. That means `iconButton` (used by narrow and compact), `row` (wide rows), and both `bottom-nav-compact-toggle` buttons.
4. Tests in `BottomNav.test.tsx`, for each variant (narrow, wide rows, wide compact):
   - `fireEvent.mouseDown` returns `false` for every entry button and for the compact toggle;
   - no button has a negative `tabIndex`;
   - a click still calls its handler.
5. Mutations to report:
   - make `keepFocus` a no-op → every T1 test turns red;
   - drop the handler from `row` only → only the wide-rows cases turn red.

## T2 — the docked worker list's header (rule W)

Files: `features/workspace/components/ActivityBarWide.tsx`, `features/workspace/components/ActivityBarWide.test.tsx`.

1. Today `worker-list-section` is both the sized box and the scroll container (around `ActivityBarWide.tsx:332-346`). Split it into two elements:
   - **`worker-list-section`** keeps `style={{ height: workerListHeight }}` and becomes `flex flex-col min-h-0 shrink-0`.
   - **Header** `worker-list-header`: a row with `px-3 py-1.5` (or what matches `FloatingPanel`'s header padding), `flex items-center justify-between shrink-0`. It holds the title `t('nav.workers')` (`text-xs font-medium text-text-primary truncate`) and the × button `worker-list-close`:
     - `type="button"`, `aria-label={t('common.close')}`, `title={t('common.close')}`;
     - `onMouseDown={keepFocus}`, `onClick={() => setWorkerListOpen(false)}` (or the existing toggle);
     - Phosphor `X` 14 with `FloatingPanel`'s close-button classes.
   - **Scroll area** `worker-list-scroll`: `min-h-0 flex-1 overflow-y-auto overscroll-contain`, containing `<WorkerList />`.
2. `useWorkerListResize` and the `PaneSplitter` stay unchanged.
3. Tests in `ActivityBarWide.test.tsx`:
   - **existing assertions that the section itself scrolls must move to `worker-list-scroll`** (codex plan review #1): `ActivityBarWide.test.tsx:158-164` (the section as the second scroller) and `:187-200` (the section's own `overflow-y-auto overscroll-contain`). Update them to assert the classes on `worker-list-scroll`, and that `worker-list-section` no longer scrolls;
   - header and × absent while the list is closed, present while open;
   - × click → `useLayoutStore.getState().workerListOpen === false`, `worker-list` gone, `workerListHeight` unchanged;
   - `worker-list-scroll` contains `worker-list` and does not contain `worker-list-header`; `worker-list-section` contains both;
   - `worker-list-section`'s inline height equals the rendered height (the existing height tests keep passing — adjust their selector only if they read the scroll container);
   - `fireEvent.mouseDown(×)` returns `false`.
4. Mutations to report:
   - put the header inside the scroll area → the structure test turns red;
   - have × toggle twice, or do nothing → the close test turns red.

## T3 — status bar and title bar (rule F, part 2)

Files:
- `components/TitleBar.tsx`, `components/TitleBar.test.tsx`
- `components/status/PaneModeButtons.tsx`, `components/status/PaneModeButtons.test.tsx`
- `components/status/StatusSegments.tsx` (`CopySegment`) and its test (in `StatusBar.test.tsx` if there is no separate file)
- `components/StatusBar.tsx` (`status-peer-refresh`), `components/StatusBar.test.tsx`

1. First list every `<button>` rendered directly by `TitleBar.tsx`, `StatusBar.tsx` and `components/status/*`. Each one is either covered, or written in the report as not covered with the reason (spec §4: it opens something that takes focus itself).
2. Add `onMouseDown={keepFocus}` to every covered button:
   - the layout buttons;
   - the three mode buttons, including when disabled (harmless) — keep them consistent;
   - `CopySegment`;
   - `status-peer-refresh`;
   - anything else found in step 1.
3. Tests, for each covered button:
   - `fireEvent.mouseDown` returns `false`;
   - no negative `tabIndex`;
   - the click still runs:
     - layout: `handlePattern` path, e.g. the existing apply test;
     - mode: `run`;
     - copy: `onCopy`;
     - refresh: `peer.refresh`.
   - Negative control: `floating-panel-close` is not prevented (it lives in `FloatingPanel` and is out of scope).
4. Mutation to report: drop the handler from `CopySegment` → only its test turns red.

## T4 — acceptance (main session, after T1–T3)

Run `playwright cli` (session `shell-polish`) against the worktree's own Vite dev server on a free port. Do not use :5174 (main checkout). Run the whole list once with Chromium and once with `--browser webkit` (codex plan review #2). Steps:

1. Set `purdex-layout` in localStorage: `activityBarWidth: 'wide'`, `bottomNavCompact: true`.
2. Append a probe `<textarea>`, focus it, and keep a handle to it (it stands in for the pane).
3. Click Workers:
   - `activeElement` **is the probe** (not merely "not the button");
   - press Shift → `:focus-visible` matches nothing;
   - press Space → `aria-pressed` does not change.
4. Blur everything (`activeElement` is body), then click Workers: `activeElement` is still body.
5. Click × in the header: the list closes.
6. Set `workerListHeight: 96`, reload, and open the list:
   - `worker-list-header` `offsetHeight` ≤ 32;
   - `worker-list-scroll` `clientHeight` ≥ 64 (codex plan review #3).
7. Narrow bar (`activityBarWidth: 'narrow'`): focus the probe, open the floating panel, then close it with Escape and with the Workers button. Each time focus returns to the probe.
8. Close the session and stop the dev server.

The title bar is Electron-only, and the status bar's mode buttons need a live agent pane. Those sites rest on their unit tests plus this browser proof of the shared helper; the user's Electron run is the final check.

## PR

A single PR. Expected diff: about 150 lines of code plus 200 lines of tests, well under 800 lines and 20 files.
