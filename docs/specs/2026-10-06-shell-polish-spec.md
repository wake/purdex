# Shell polish — worker list close button, chrome buttons keep focus — spec

Follow-up to the shell cleanup (`docs/specs/2026-10-05-shell-cleanup-spec.md`, shipped alpha.478–483). Two items the user reported on 2026-10-06 after using it. One phase, one PR.

## 1. User decisions (2026-10-06, do not reopen)

| # | Decision |
|---|---|
| W | The wide bar's docked worker list gets a **header row: "Workers" on the left, × on the right**, matching the narrow bar's floating panel (which already has a title and ×). The header stays put while the list scrolls. × closes the list, the same as the Workers button. |
| F | Shell chrome buttons **do not take focus on a mouse press**. Scope: the activity bar's bottom buttons, the status bar's buttons, and the title bar's layout buttons. |

The third item of the same report (how terminated workers are listed) is under discussion and is not in this spec.

## 2. Facts (measured 2026-10-06, origin/main alpha.485, Chromium via `playwright cli`)

- No stylesheet in `spa/src` sets a focus style; the ring the user saw is the UA's `:focus-visible` outline. Electron draws it in the macOS accent colour (orange on the user's machine).
- Click the Workers button with the mouse (wide bar, compact row): `document.activeElement` is the button afterwards, `:focus-visible` false, no outline.
- Then press any key, even Shift alone: the same button now matches `:focus-visible` and the outline appears. A screenshot hotkey is enough, which is how the report was captured.
- Then press Space: **the worker list toggles again** (`aria-pressed` true → false). Keystrokes meant for the pane go to the button.
- The narrow bar's `FloatingPanel` moves focus into the panel on open and, on close, restores it to whatever had focus before it opened (`FloatingPanel.tsx` focus management). It already has a title and a × (`floating-panel-close`).
- The wide bar's docked list (`ActivityBarWide.tsx`, `worker-list-section`) has no header: the first thing in it is the first host's section header, and the whole section is the scroll container.
- The section's height comes from `useWorkerListResize` (stored `workerListHeight`, min 96, max 800, capped so the workspace zone keeps 96).

## 3. Rule W — the docked worker list's header

- The docked section becomes a column of exactly the stored/capped height (as today): a **header row** (not scrolling) and below it the **scroll area** that holds `WorkerList` and takes the rest (`min-h-0`, `flex-1`, `overflow-y-auto`, `overscroll-contain` — the classes the section has today move to it).
- Header: the title `t('nav.workers')` and a × button, laid out and styled like `FloatingPanel`'s header (title `text-xs font-medium text-text-primary truncate`; × is Phosphor `X` 14, `rounded p-0.5 text-text-muted hover:text-text-primary hover:bg-surface-hover`, `aria-label={t('common.close')}`). Test ids: `worker-list-header`, `worker-list-close`. No new locale keys.
- × sets the list closed (`setWorkerListOpen(false)` or the toggle — the list is open whenever the header exists). Closing unmounts the list exactly as the Workers button does today; the stored height is not touched.
- The header is inside the section's height: the scroll area gets `height − header`. The header is at most 32 px tall, so at the minimum of 96 the scroll area is at least 64 px (measured in acceptance; jsdom has no layout).
- The divider, its drag, the cap and the stored height keep their current behaviour (`useWorkerListResize` unchanged).
- The narrow bar's floating panel is unchanged.

## 4. Rule F — chrome buttons keep focus where it was

- A mouse press on a covered button does not move focus: its `mousedown` default is prevented. The click still fires and does what it does today. Focus stays where it was — usually the pane the user is typing in — so the next keystroke reaches that pane, no outline appears on the button, and Space no longer re-fires it.
- Keyboard use is unchanged: the buttons stay in the tab order (no `tabIndex={-1}`), and Enter/Space activate a focused button as before.
- One shared handler (a small helper under `spa/src/lib/`) so every call site reads the same and the reason is written once.
- **Covered buttons** (every `<button>` in these components):
  - `features/workspace/components/BottomNav.tsx` — all variants (wide rows, wide compact, narrow) and both compact toggles;
  - the new `worker-list-close` (§3);
  - `components/TitleBar.tsx` — the layout buttons (`layout-buttons`) and the sidebar toggle it renders (`CollapseButton`, `features/workspace/components/CollapseButton.tsx`, every variant). The user's scope is the whole title bar; v1 of this spec named only the layout buttons, which the T1–T3 report caught;
  - `components/status/PaneModeButtons.tsx` — terminal / worker / chat;
  - `components/status/StatusSegments.tsx` — `CopySegment`;
  - `components/StatusBar.tsx` — `status-peer-refresh`.
  The implementer lists any other `<button>` rendered directly by `StatusBar.tsx`, `components/status/*` or `TitleBar.tsx` and covers it too, unless it opens something that takes focus itself (then it is listed as not covered, with the reason).
- **Not covered**: buttons inside dialogs, menus, floating panels (including `floating-panel-close`), the tab strip, pane headers, forms and inputs — they either own focus on purpose or are not shell chrome.
- When nothing had focus (body), focus stays on body.
- **A dialog opened from a covered button takes focus itself** (codex R2 attack, review `review-muvnvof3-nm7z5i`): the title bar's layout buttons open `ConfirmDialog` / `LayoutKeepPicker`, and the mode buttons open the handoff confirm — all on the shared `components/ConfirmDialog.tsx`, which had no focus handling at all. With the button no longer taking focus, keystrokes would reach the pane behind the dialog (Enter sends a WorkerInput draft, keys go to xterm). So `ConfirmDialog`:
  - on mount, moves focus to the **dialog panel itself** (`tabIndex={-1}`), not to a button — typing or Enter must not confirm or cancel by accident; Escape still cancels; Tab reaches the buttons;
  - keeps Tab / Shift+Tab inside the dialog while it is up;
  - on unmount, if focus is still inside the dialog, returns it to whatever had it when the dialog opened (the pane, when opened from a covered button) — the same rule as `FloatingPanel`; if something else has taken focus meanwhile (e.g. a tab switch that cancelled the dialog and focused the new tab's pane), it leaves focus alone.
  This also fixes the same gap for every other `ConfirmDialog` caller.
- The narrow bar: pressing Workers no longer focuses the button, so when the floating panel closes it restores focus to what had it before (the pane), not to the Workers button.

## 5. Tests

- W: header and × render only while the list is open; × closes the list (store false, `worker-list` unmounted, stored height unchanged); the header is not inside the scroll container (the scroll area is a separate element that contains `worker-list` and not the header); the section's total height is still the rendered height from `useWorkerListResize`.
- F: for every covered button, `fireEvent.mouseDown` returns `false` (default prevented) and the button is still focusable (no negative `tabIndex`); its click still runs its action. A negative control: a not-covered button (e.g. `floating-panel-close`) is not prevented.
- Mutation (deliverable): removing the handler from one call site turns that site's test red; making the helper a no-op turns all of them red.
- What the unit tests can and cannot show: jsdom does not run the browser's native "focus on mousedown" default, so `fireEvent.mouseDown(...) === false` proves only that a site is wired to the helper. That the helper keeps focus is proven once, in a real browser, below; every site uses the same helper.
- Real browser (acceptance, `playwright cli`, Chromium and WebKit, wide compact row and narrow bar):
  - with a focusable element holding focus (a textarea standing in for the pane), click Workers → `activeElement` is **that same element**; press Shift → nothing matches `:focus-visible`; press Space → the list does not toggle;
  - with nothing focused (body), click Workers → `activeElement` is still body;
  - narrow bar: open and close the floating panel → focus returns to the element that had it before;
  - click × in the header → the list closes;
  - with `workerListHeight` 96: header `offsetHeight` ≤ 32 and the scroll area's `clientHeight` ≥ 64.
  The title bar exists only under Electron, and the status bar's mode buttons need a live agent pane, so those sites rest on their unit tests plus the shared-helper argument; the user's Electron run is the final check.

## 6. Not in scope

- How terminated workers are listed or archived (under discussion).
- Focus styling (no `focus-visible` ring design changes).
- Pane-level buttons (execution header, worker input) and the tab strip.
