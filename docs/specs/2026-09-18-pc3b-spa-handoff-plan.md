# Plan — P-C.3b: SPA "Hand to nex" / "Take back to terminal"

- Spec: `2026-09-18-pc-launch-ui-spec.md` v1.4 §4.4 (SPA + UI parts), §6
  items 3–6. Daemon endpoints shipped in alpha.386/387 (P-C.3a), verified
  live in §6.3.1.
- Worktree `.claude/worktrees/pc-launch-ui`, branch `worktree-pc-launch-ui`
  (at alpha.387). One PR, SPA only, ≤ 800 lines target.
- Every task: subagent, TDD, `git commit --only`; Bash prefixed with
  `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pc-launch-ui/spa && `;
  per task targeted vitest + eslint + `npx tsc --noEmit -p tsconfig.app.json`;
  before the PR the full gates. **Tasks 1→2→3→4→5 sequential.**
- Q1 (spec §8) resolved: the "Hand to nex" control is a **pane context
  menu** item (`PaneContextMenu.tsx`, dispatched by
  `PaneLayoutRenderer.tsx:70-120`) — pane-local, so every terminal pane in a
  split has it; the StatusBar only knows the primary pane and is not used.
  Spec §4.4 UI and §8 Q1 are amended to say this (task 5).

## Measured baseline (2026-09-18, alpha.387)

- Daemon (P-C.3a), from `internal/module/nex/{handoff,takeback}.go`:
  - `POST /api/sessions/{code}/nex-handoff` body `{expected_tmux_instance,
    profile?, rollback_command?}` → 200 `{execution_id, state,
    effective_profile, session_id, cwd}`. Errors `{error, code, …}`:
    503 `nex_unavailable`; 400 `malformed_body`, `invalid_instance`;
    404 `session_missing`; 409 `handoff_unsupported`, `handoff_in_progress`,
    `tmux_instance_mismatch` (+`after_exit?, rolled_back?, session_id?`),
    `no_identity`, `no_cc`, `delegate_rejected` (+`reject_reason,
    rolled_back, session_id, infra_error?`); 500 `principal_unresolved`,
    `session_lookup_failed`; 504 `cc_exit_timeout` (+`step`).
  - `POST /api/sessions/{code}/nex-takeback` body `{expected_tmux_instance,
    execution_id, resume_command, lease_id?}` → 200 `{session_id,
    archived}`. Errors: 503 `nex_unavailable`; 400 `malformed_body`,
    `invalid_instance`, `missing_execution_id`, `missing_resume_command`;
    404 `session_missing`, `execution_not_found`; 409 `handoff_in_progress`,
    `tmux_instance_mismatch`, `cc_already_running` (+`session_id`),
    `execution_not_bound` (+`execution_id, session_code`), `held_by`
    (+`principal`), `execution_not_settled` (+`state`), `no_session_id`;
    500 `principal_unresolved`, `session_lookup_failed`, `store_error`,
    `lease_error`, `interrupt_failed`, `send_failed` (+`session_id`);
    504 `interrupt_unconfirmed`, `cc_start_timeout` (+`session_id`).
    `session_id` is present only on `cc_already_running`, `send_failed`,
    `cc_start_timeout` and the handoff codes listed above — **not** on the
    others.
- `spa/src/lib/host-api.ts:379-397` `handoff()` — the `hostFetch` POST idiom.
- `spa/src/lib/resume-templates.ts`: `ResumeTemplatePair{exact, fallback}`,
  `resumeLookupFor(hostId)`; cc default `exact: 'claude --resume {id}'`. The
  daemon substitutes `{id}`; the SPA sends `pair.exact` unsubstituted.
- `spa/src/types/tab.ts:86` tmux-session content; `:100` execution content
  `{kind: 'execution'; executionId; host?}`; `lib/pane-utils.ts:57-60
  contentMatches`; `route-utils.ts:142-144` builder.
- `spa/src/stores/useTabStore.ts:440 setPaneContent(tabId, paneId, content)`
  — **silent no-op** when the tab/pane is gone; `:504-522 openSingletonTab`
  scans **primary panes only** and focuses the first match; `findPane`,
  `getPrimaryPane`, `countLeaves` in `lib/pane-utils.ts`.
- `spa/src/components/PaneContextMenu.tsx`: props `{position, canDetach,
  onClose, onAction}`, `PaneMenuAction = 'split-h'|'split-v'|'close'|'detach'`,
  items built inline (`:46-57`); `PaneLayoutRenderer.tsx:70-120` opens it on
  right-click (not for editor panes / shift-click), dispatches actions with a
  live-layout guard.
- `spa/src/stores/useAgentStore.ts:143 agentTypes: Record<compositeKey,
  string>` — the live agent type per session (replay + normalized events);
  `types/tab.ts` `rebuild.agent.type` (pane-level, set at SessionStart,
  may be `unverified`); `session.cc_session_id` (legacy relay only).
- `spa/src/components/execution/ExecutionHeader.tsx:11-28` props;
  `ExecutionView.tsx` composes it with `useExecutionLease` (`{ensureLease,
  release, forget, touch}`; held lease id at
  `useExecutionStore.executions[key].lease?.leaseId`; the hook's cleanup
  calls `void release()` which swallows errors); pane wrapper
  `register-modules/index.tsx:100-108` — measure what props it receives
  (`pane`, `tabId`?).
- `stores/useNexHostStore.ts` `selectHandoffReady(hostId)`, `ensure(hostId)`.
- Toast: `useUndoToast.getState().show(...)` (as `SessionLauncher`); measure
  whether it supports an action button; if not, add an optional `action:
  {label, onClick}` to it in task 2 (small, with test).
- Locales `en.json`/`zh-TW.json`; `locale-completeness.test.ts`.

## Task 1 — API wrappers + error type + template helper

Files: new `lib/nex/handoff-api.ts` + test; `lib/resume-templates.ts` (+
test) `resumeTemplateFor(lookup, agentType): string | undefined` (=
`pair.exact`).

- `export class HandoffApiError extends Error { status: number; code:
  string; body: Record<string, unknown> }`; `nexHandoff(hostId, code, body)`,
  `nexTakeback(hostId, code, body)` via `hostFetch` + JSON + `X-Pdx-Client`
  (`getNexClientId()`); non-2xx → `HandoffApiError` from the JSON body
  (unparseable → `http_<status>`); network → `(0, 'network')`.
- Tests: bodies/headers; 200 parsed; 409 `delegate_rejected` fields; 504
  `cc_exit_timeout.step`; unparseable 500; network; `resumeTemplateFor`.
- Commit: `feat(nex): handoff/take-back API wrappers (P-C.3b task 1)`.

## Task 2 — checked pane swap, `from`, single-flight orchestration, error map

Files: `types/tab.ts`, `stores/useTabStore.ts` (+ test), `lib/pane-utils.ts`
(+ test), `lib/route-utils.ts` (+ test), new `lib/nex/handoff.ts` + test,
toast store (+ test) if an action button is missing, locales.

- **Checked swap**: `useTabStore.trySetPaneContent(tabId, paneId, content):
  boolean` — `false` when the tab or pane is not in the live layout, `true`
  after a real write (implement on top of `setPaneContent` with a
  `findPane` check; test both outcomes and that a `false` leaves state
  untouched).
- **`from`**: execution content gains `from?: {sessionCode; tmuxInstance;
  cachedName}`; `contentMatches` and `tabToUrl` ignore it (tests).
- **Singleton precedence** (codex §review 3): `openSingletonTab` for
  execution content scans **all leaves** of every tab (not only the primary)
  and, among matches, prefers a pane whose content has `from` (activate that
  tab; the pane itself is focused if the store has a focus-pane action —
  measure; otherwise activating the tab is enough). Tests: existing
  from-less primary + new from-pane in a split → route opens the from-pane's
  tab; from-pane in a secondary leaf found (no duplicate tab); no match →
  create as today; non-execution content keeps the primary-only scan
  (existing tests untouched).
- **Single-flight**: `lib/nex/handoff.ts` keeps a module-level
  `Set<string>` of in-flight `${hostId}:${sessionCode}` (handoff) and
  `${hostId}:${executionId}` (take-back); a second call while one is in
  flight rejects immediately with `HandoffApiError(0,
  'handoff_in_progress')` without a request (test: two concurrent
  `handToNex` → one request; after settle the key is cleared).
- `handToNex({hostId, sessionCode, tmuxInstance, cachedName, tabId,
  paneId})`: `ensure` → `selectHandoffReady` else `(0,
  'handoff_unsupported')` → `rollback_command = resumeTemplateFor(…, 'cc')`
  → `nexHandoff` → `trySetPaneContent(..., {kind: 'execution', executionId,
  host: hostId, from: {…}})`; returns `{result, swapped: boolean}`. On
  `swapped: false` the caller shows a toast with an **"open execution"
  action** that calls `openSingletonTab({kind: 'execution', executionId,
  host: hostId, from})` (codex §review 2 — the handoff already happened;
  the user must be able to reach it).
- `takeBack({hostId, executionId, from, leaseId?, tabId, paneId,
  forgetLease})`: `resume_command` from the template → `nexTakeback` → on
  success call `forgetLease()` **before** the swap (so the unmounting lease
  hook does not release a lease the daemon already consumed — codex §review
  6) → `trySetPaneContent(tabId, paneId, tmux-session content)`; returns
  `{result, swapped}`; on failure the lease is left to the hook.
- `handoffErrorMessage(t, err)`: **every** code in the baseline table maps
  to `handoff.error.<code>` with params; unknown → `handoff.error.generic`
  with the code; `manualResumeHint(err)` returns the `session_id` only for
  the codes that carry it (table above). Locale keys for every code (en +
  zh-TW) + `handoff.menu` ("Hand to nex"), `handoff.confirm_title`,
  `handoff.confirm_body`, `handoff.success`, `handoff.open_execution`,
  `takeback.button`, `takeback.confirm_running`, `takeback.success`,
  `takeback.manual_resume` ("Resume by hand: claude --resume {{id}}").
- Tests: swap outcomes; single-flight; `handToNex` body + swap content +
  unsupported → no request + `swapped:false` path; `takeBack` body incl.
  `lease_id`, `forgetLease` called before swap on success and **not** on
  failure; error map table-driven over every code (key + params);
  `manualResumeHint` presence table; toast action if added.
- Commit: `feat(exec): checked pane swap, execution content from, single-flight handoff orchestration (P-C.3b task 2)`.

## Task 3 — pane context menu "Hand to nex" + confirm dialog

Files: `components/PaneContextMenu.tsx` (+ test), `components/PaneLayoutRenderer.tsx`
(+ test), new `components/HandoffConfirmDialog.tsx` (+ test), new
`lib/nex/handoff-gate.ts` (+ test).

- `handoff-gate.ts`: `isHandoffCandidate(content, {agentTypes, session,
  handoffReady}): boolean` — pure: `content.kind === 'tmux-session'`, `mode
  === 'terminal'`, not `terminated`, `handoffReady(hostId)`, and the agent
  is CC by **this precedence**: `useAgentStore.agentTypes[compositeKey(hostId,
  sessionCode)] === 'cc'` → else `content.rebuild?.agent?.type === 'cc'`
  (even if `unverified` — the daemon re-checks identity) → else
  `!!session?.cc_session_id`. Tests: each source alone; agent switched away
  (`agentTypes` says `codex`) hides even with a stale rebuild record; no
  info → hidden; stream mode / terminated → hidden.
- `PaneContextMenu` gains prop `extraItems?: MenuItem[]` (rendered after a
  separator) so the generic menu stays generic; `PaneMenuAction` gains
  `'hand-to-nex'`. `PaneLayoutRenderer` computes the candidate flag with
  the gate (subscribing to `useAgentStore`/`useSessionStore`/
  `useNexHostStore` selectors for the leaf's host+session; calls
  `ensure(hostId)` for tmux-session leaves) and, on `'hand-to-nex'`, opens
  `HandoffConfirmDialog` (state on the renderer: `{tabId, paneId, content}
  | null`).
- `HandoffConfirmDialog`: title/body from locales (body says CC exits
  here and continues headless with no permission prompts), Cancel /
  Confirm; Confirm → `handToNex(...)` with busy state (buttons disabled;
  second click ignored — plus the store-level single-flight); success →
  close + toast `handoff.success`; `swapped: false` → toast with the
  "open execution" action; `HandoffApiError` → toast
  `handoffErrorMessage` + `takeback.manual_resume` when
  `manualResumeHint` gives an id.
- Tests: menu shows the item only when the gate says so (secondary pane
  of a split included); action opens the dialog with the right pane;
  **rapid double-click on Confirm → exactly one `handToNex` call**; success
  closes and toasts; error toast text for `no_cc`, `delegate_rejected`
  (rolled back / not), `cc_exit_timeout`; `swapped:false` toast has the
  action and clicking it calls `openSingletonTab` with `from`.
- Commit: `feat(exec): "Hand to nex" in the pane context menu with confirm (P-C.3b task 3)`.

## Task 4 — ExecutionHeader "Take back"

Files: `components/execution/ExecutionHeader.tsx` (+ test),
`components/execution/ExecutionView.tsx` (+ test),
`lib/register-modules/index.tsx` (pane wrapper passes `tabId`/`paneId`/
`content` if it does not already).

- `ExecutionHeader` gains `onTakeBack?: () => void`, `takeBackBusy?:
  boolean`; renders `takeback.button` (Phosphor `ArrowUUpLeft`) only when
  `onTakeBack` is provided.
- `ExecutionView` receives `content` (with `from`), `tabId`, `paneId`;
  when `content.from` exists: handler → if `st.summary?.state ===
  'running'` confirm `takeback.confirm_running` (reuse the confirm dialog
  component with different copy) → `takeBack({…, leaseId:
  st.lease?.leaseId, forgetLease: lease.forget})` → success toast /
  `swapped:false` toast (the execution is archived — say so) / error toast
  with `manualResumeHint`.
- Tests: no `from` → no button; with → button; running → confirm then
  `takeBack` with lease id and `forget` passed; idle → no confirm; error
  toast; success swaps the pane back (store assertion) and the lease hook
  does not call `releaseLease` afterwards (spy).
- Commit: `feat(exec): "Take back to terminal" on execution panes that came from a session (P-C.3b task 4)`.

## Task 5 — spec amendment + PR

- Spec §4.4 UI: "Hand to nex" lives in the pane context menu (pane-local);
  §8 Q1 resolved; §9 log "v1.5 — P-C.3b: pane-context-menu entry; checked
  pane swap with open-execution recovery; execution singleton prefers the
  `from` pane; client single-flight". Commit `docs(spec): P-C v1.5`.
- Full gates; PR "feat(exec): Hand to nex / Take back to terminal in the
  SPA (P-C.3b)"; codex R1 → attack → critic (`gpt-5.6-sol`); acceptance
  spec §6 items 3–6 on mlab (worktree dev server + playwright; daemon
  alpha.387): tmux session with interactive `claude` → right-click → Hand
  to nex → confirm → pane becomes the execution with the earlier exchange →
  follow-up from the pane (hooks fire) → Take back → terminal again,
  conversation continues, execution archived; right-click on a non-CC
  session → no item; double-click Confirm → one execution; kill the tmux
  session then Take back → 404 toast, execution pane untouched; split a
  terminal pane and hand off the **secondary** leaf.

## Review log

- codex `task-mu6rbltg-z25ykx` (gpt-5.6-sol), 8 findings, all applied: (1)
  StatusBar only sees the primary pane → pane context menu; (2)
  `setPaneContent` is a silent no-op → `trySetPaneContent` + open-execution
  recovery toast; (3) `from` vs singleton precedence defined (all leaves,
  prefer `from`); (4) error-code table completed from the handlers,
  `session_id` presence corrected; (5) CC gate precedence fixed
  (`agentTypes` → `rebuild.agent.type` → `cc_session_id`); (6) `forget()`
  before the take-back swap; (7) client single-flight + rapid double-click
  test; (8) task 2 builds the checked-swap primitive first.
