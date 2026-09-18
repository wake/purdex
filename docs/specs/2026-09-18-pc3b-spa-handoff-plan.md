# Plan — P-C.3b: SPA "Hand to nex" / "Take back to terminal"

- Spec: `2026-09-18-pc-launch-ui-spec.md` v1.4 §4.4 (SPA + UI parts), §6
  items 3–6. Daemon endpoints shipped in alpha.386/387 (P-C.3a).
- Worktree `.claude/worktrees/pc-launch-ui`, branch `worktree-pc-launch-ui`
  (at alpha.387). One PR, SPA only, ≤ 800 lines target.
- Every task: subagent, TDD, `git commit --only`; Bash prefixed with
  `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pc-launch-ui/spa && `;
  per task targeted vitest + eslint + `npx tsc --noEmit -p tsconfig.app.json`;
  before the PR the full gates. **Tasks 1→2→3→4 sequential.**
- Q1 (spec §8) resolved: the "Hand to nex" control lives in the **StatusBar**
  next to the view-mode dropdown (`StatusBar.tsx:546-571`), which already
  knows the active tab's primary pane and is the existing home of
  terminal/stream mode switching. No new pane-header component.

## Measured baseline (2026-09-18, alpha.387)

- Daemon (P-C.3a): `POST /api/sessions/{code}/nex-handoff` body
  `{expected_tmux_instance, profile?, rollback_command?}` → 200
  `{execution_id, state, effective_profile, session_id, cwd}`; errors
  `{error, code, …}` with codes `nex_unavailable` 503,
  `handoff_unsupported`/`handoff_in_progress`/`tmux_instance_mismatch`
  (+`after_exit, rolled_back, session_id`)/`no_identity`/`no_cc`/
  `delegate_rejected` (+`reject_reason, rolled_back, session_id,
  infra_error?`) 409, `session_missing` 404, `cc_exit_timeout` (+`step`) 504,
  `malformed_body`/`invalid_instance` 400. `POST …/nex-takeback` body
  `{expected_tmux_instance, execution_id, resume_command, lease_id?}` → 200
  `{session_id, archived}`; codes `session_missing`/`execution_not_found`
  404, `tmux_instance_mismatch`/`cc_already_running`/`execution_not_bound`/
  `held_by` (+`principal`)/`execution_not_settled` (+`state`)/
  `no_session_id` 409, `interrupt_unconfirmed`/`cc_start_timeout` 504,
  `store_error`/`lease_error`/`interrupt_failed`/`send_failed` 500. All
  post-preflight take-back failures carry `session_id`. Verified live in
  spec §6.3.1.
- `spa/src/lib/host-api.ts:379-397` `handoff()` — the `hostFetch` POST
  pattern (JSON body, `!res.ok` → throw with text).
- `spa/src/lib/resume-templates.ts`: `ResumeTemplatePair{exact, fallback}`,
  `resumeLookupFor(hostId)` / `useResumeTemplateLookup(hostId)`; cc default
  `exact: 'claude --resume {id}'` — the daemon substitutes `{id}`, so the
  SPA sends `pair.exact` **unsubstituted**. `composer.ts:27
  resolveResumeCommand(record, templates)` is not needed here.
- `spa/src/types/tab.ts:86` tmux-session content
  `{kind, hostId, sessionCode, mode, cachedName, tmuxInstance, terminated?,
  rebuild?}`; `:100` execution content `{kind: 'execution'; executionId;
  host?}`; `lib/pane-utils.ts:57-60 contentMatches`; `route-utils.ts:142-144`
  builder.
- `spa/src/stores/useTabStore.ts:440 setPaneContent(tabId, paneId,
  content)`; `:439 setViewMode`.
- `spa/src/components/SessionPaneContent.tsx:19-60`: derives
  `hostId/sessionCode/mode/tmuxInstance`, `session` from `useSessionStore`
  (`cc_session_id`, `name`); legacy `handleHandoff` uses
  `useStreamStore.setHandoffProgress` (leave it).
- `spa/src/components/StatusBar.tsx:546-571`: view-mode dropdown for the
  primary pane (`activeTab`, `primary`, `viewMode`, `onViewModeChange`).
- `spa/src/components/execution/ExecutionHeader.tsx:11-28` props
  `{summary, costUsd, sse, isMine, onInterrupt, onTerminate, busy}`;
  `ExecutionView.tsx` composes it with `useExecutionLease` (returns
  `{ensureLease, release, forget, touch}`; the held lease id is in
  `useExecutionStore.executions[key].lease?.leaseId`).
- `stores/useNexHostStore.ts` `selectHandoffReady(hostId)` (ready ∧
  `delegate.resume_session_id` ∧ `handoff` ∈ profiles); `ensure(hostId)`.
- Toasts: find the existing toast store used by `SessionLauncher`
  (`useUndoToast.getState().show`) and reuse.
- Locales `en.json`/`zh-TW.json`; `locale-completeness.test.ts`.

## Task 1 — API wrappers + error type

Files: new `lib/nex/handoff-api.ts` + test; `lib/resume-templates.ts` (+
test) gains `resumeTemplateFor(lookup, agentType): string | undefined`
(returns `pair.exact`).

- `export class HandoffApiError extends Error { status; code; body }`.
- `nexHandoff(hostId, code, body: {expected_tmux_instance; profile?;
  rollback_command?}) → Promise<NexHandoffResult>`;
  `nexTakeback(hostId, code, body: {expected_tmux_instance; execution_id;
  resume_command; lease_id?}) → Promise<{session_id; archived}>`. Both via
  `hostFetch` with JSON; non-2xx → parse `{error, code, …}` into
  `HandoffApiError` (unparseable → code `http_<status>`); network →
  `HandoffApiError(0, 'network')`; add `X-Pdx-Client` header
  (`getNexClientId()`), same as `nexFetch`.
- Tests: bodies and headers sent; 200 parsed; 409 `delegate_rejected`
  carries `rolled_back`/`session_id`; 504 `cc_exit_timeout` carries `step`;
  unparseable 500; network; `resumeTemplateFor` returns the exact template
  unsubstituted / undefined for unknown agent.
- Commit: `feat(nex): handoff/take-back API wrappers (P-C.3b task 1)`.

## Task 2 — pane content `from` + orchestration

Files: `types/tab.ts`, `lib/pane-utils.ts` (+ test), `lib/route-utils.ts`
(+ test), new `lib/nex/handoff.ts` + test, locales.

- Execution content gains `from?: {sessionCode: string; tmuxInstance:
  string; cachedName: string}`; `contentMatches` and `tabToUrl` ignore it
  (tests: two execution contents differing only in `from` match; URL
  unchanged).
- `handToNex({hostId, sessionCode, tmuxInstance, cachedName, tabId,
  paneId})`: `await useNexHostStore.getState().ensure(hostId)`; require
  `selectHandoffReady` else throw `HandoffApiError(0, 'handoff_unsupported')`;
  `rollback_command = resumeTemplateFor(resumeLookupFor(hostId), 'cc')`;
  `nexHandoff`; on success `setPaneContent(tabId, paneId, {kind:
  'execution', executionId, host: hostId, from: {sessionCode, tmuxInstance,
  cachedName}})`; returns the result. Errors propagate as
  `HandoffApiError`; the caller (task 3) maps to toasts. No automatic
  `openSingletonTab` fallback — if `setPaneContent` throws (pane gone) the
  caller offers "open execution".
- `takeBack({hostId, executionId, from, leaseId?, tabId, paneId})`:
  `resume_command = resumeTemplateFor(…, 'cc')`; `nexTakeback`; on success
  `setPaneContent(tabId, paneId, {kind: 'tmux-session', hostId,
  sessionCode: from.sessionCode, mode: 'terminal', cachedName:
  from.cachedName, tmuxInstance: from.tmuxInstance})`; returns
  `{session_id, archived}`.
- `handoffErrorMessage(t, err): string` — maps every daemon code to an
  i18n key `handoff.error.<code>` with params (`step`, `reject_reason`,
  `rolled_back`, `session_id`, `principal`, `state`); unknown → generic
  with the code. i18n keys added for all codes listed in the baseline (en +
  zh-TW), plus `handoff.button`, `handoff.confirm_title`,
  `handoff.confirm_body` ("Claude Code will exit here and continue as a
  headless execution with no permission prompts (handoff profile)."),
  `handoff.busy`, `handoff.success`, `handoff.open_execution`,
  `takeback.button`, `takeback.confirm_running`, `takeback.success`,
  `takeback.manual_resume` ("Resume by hand: claude --resume {{id}}").
- Tests: `handToNex` request body (template unsubstituted, instance),
  pane swap content shape, unsupported → no request; `takeBack` body incl.
  `lease_id` passthrough, pane swap back; error mapping table (each code →
  its key + params); pane-gone → error surfaces (no throw swallowed).
- Commit: `feat(exec): handToNex / takeBack orchestration and pane content from (P-C.3b task 2)`.

## Task 3 — StatusBar "Hand to nex"

Files: `components/StatusBar.tsx` + test, new
`components/HandoffConfirmDialog.tsx` (or reuse an existing confirm
component — measure `TerminateConfirm`-style patterns in the codebase).

- Next to the view-mode dropdown: a `Lightning` button `handoff.button`
  shown when the primary pane is `tmux-session` with `mode === 'terminal'`,
  not `terminated`, `useNexHostStore.selectHandoffReady(hostId)` true
  (call `ensure` when the pane's host changes), and the session is a CC one
  (`session.cc_session_id` or `content.rebuild?.agent?.type === 'cc'` or
  `session.current_command`/agent hints — measure which is reliable; the
  daemon re-checks anyway, so this is a display gate only).
- Click → confirm dialog (body text above) → busy spinner on the button →
  `handToNex(...)`; success toast `handoff.success`; on `HandoffApiError`
  toast with `handoffErrorMessage` and, for `delegate_rejected` with
  `rolled_back: false` or `tmux_instance_mismatch {after_exit}` or
  `cc_exit_timeout`, append `takeback.manual_resume` with the session id when
  present.
- Tests: button hidden for stream mode / terminated / not handoff-ready /
  non-cc; shown otherwise; confirm → `handToNex` called with the pane's
  values; busy while pending; error toast text per code (three
  representative codes); `ensure` called on host change.
- Commit: `feat(exec): "Hand to nex" in the status bar for terminal CC panes (P-C.3b task 3)`.

## Task 4 — ExecutionHeader "Take back"

Files: `components/execution/ExecutionHeader.tsx` + test,
`components/execution/ExecutionView.tsx` + test.

- `ExecutionHeader` gains `onTakeBack?: () => void` and `takeBackBusy?`;
  renders `takeback.button` (Phosphor `ArrowUUpLeft`) only when
  `onTakeBack` is provided.
- `ExecutionView` passes `onTakeBack` when `content.from` exists (needs the
  pane's `content`, `tabId`, `paneId` — extend `ExecutionViewProps` and the
  pane wrapper in `register-modules/index.tsx:100-108` to pass them; measure
  what the wrapper receives). Handler: if `st.summary?.state === 'running'`
  confirm `takeback.confirm_running`; call `takeBack({…, leaseId:
  st.lease?.leaseId})`; success toast; error toast via `handoffErrorMessage`
  (+ `takeback.manual_resume` when `session_id` present and the code is
  `cc_start_timeout`/`send_failed`).
- Tests: button absent without `from`; present with; running → confirm
  then `takeBack` with the lease id; idle → no confirm; error toast; pane
  content swaps back (store assertion).
- Commit: `feat(exec): "Take back to terminal" on execution panes that came from a session (P-C.3b task 4)`.

## Task 5 — PR

Full gates; PR "feat(exec): Hand to nex / Take back to terminal in the SPA
(P-C.3b)"; codex R1 → attack → critic (`gpt-5.6-sol`); acceptance spec §6
items 3–6 on mlab with the worktree dev server + playwright (daemon already
alpha.387): tmux session with interactive `claude` → StatusBar "Hand to
nex" → pane becomes the execution with the earlier exchange → follow-up
from the pane (hooks fire) → "Take back" → terminal again, conversation
continues, execution archived; hand-off on a non-CC session → button
hidden; double-click → one execution (second gets 409 toast); kill the tmux
session then "Take back" → 404 toast, execution pane untouched.
