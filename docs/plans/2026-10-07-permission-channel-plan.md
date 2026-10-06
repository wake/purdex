# Permission channel — Purdex half Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. Project rules (CLAUDE.md): TDD, one commit per task, each PR ≤ 800 lines **or** ≤ 20 files, then codex R1 + R2 (attack → critic; **a critic is mandatory for every daemon-behaviour or spec-touching PR**) → merge + bump.

**Goal:** A handoff can run in a second mode, 「需要核准」: the worker stops on a risky action, Purdex shows the request in the worker pane, and the user answers 同意 / 拒絕 (PC1–PC4).

**Architecture:** Nexen v0.19.0 does the channel (`handoff_ask` profile, `permission.requested` / `permission.resolved` events, `POST /v1/executions/{id}/permissions/{request_id}`, summary `pending_permission`, delegate `permission_timeout_s`). Purdex bumps the pin, lets the handoff / rebuild endpoints choose that profile (and the timeout), and the SPA adds the choice, the request card, the 「等待核准」 state and one setting. The SPA's calls to Nexen already pass through the daemon's `/api/nex/` reverse proxy with no allowlist, so the answer needs no daemon route.

**Tech Stack:** Go (internal/module/nex, embedded Nexen), React 19 / Zustand / Tailwind 4 / Vitest, Phosphor icons.

**Spec:** `docs/specs/2026-10-07-permission-channel-spec.md` (§2 decisions, §4 *Final names*, §5 Purdex, §6 tests, §7 phases). Nexen contract: `nexen/docs/contract/consumer-guide.md` §9.8, `capability-matrix.md` §1.14 and §2 #76–#89.

## Global Constraints

- Nexen pin: **v0.19.0** (from v0.18.1; schema stays v6; the new `permission_requests` table is created by `Open`, no DB step).
- PC2 (amended): answers happen **only in the Mac App**. No phone-browser layout, no push notifications.
- PC4: no "always allow". `allow` always sends the request's own input; `updatedInput` is never shown or edited.
- The mode is offered only when **both** hold: `capabilities.permissions` exists **and** `capabilities.sandbox_profiles` contains `handoff_ask`. `permission_timeout_s` is sent **only** when `capabilities.permissions.timeout` exists (an old daemon ignores it silently). A `rejected` handoff shows its `reject_reason` and is **never** retried as 完全放行.
- `pending_permission.since` is **Unix milliseconds**. `pending_permission: null` is an answer; an absent field means an old daemon.
- Correlate request ⇄ resolution by **`request_id`**, never by `seq` (`permission.resolved` can follow `tool_result`).
- Copy (zh-TW): 完全放行（預設）／需要核准／同意／拒絕／等待核准／「等待核准逾時」（不逾時、5、15、30、60 分鐘）／「已逾時自動拒絕（N 分鐘）」. The timeout setting is per-client (device-local), applies to new handoffs only, and says so.
- Warning colour token `status-warning` plus a **distinct icon** so 「等待核准」 is not read as "queued" (queued already uses `bg-status-warning`).
- Both locales (`en`, `zh-TW`) get every new key (`locale-completeness.test.ts`).

## Review Focus

1. A host whose `max_profile` is `handoff_ask` (below `handoff`) must still allow the asking mode — today the daemon refuses any handoff unless `handoff` itself is usable (`handoff.go:158`). → Task 2.
2. `permission_timeout_s` with a profile that has no channel, or out of 0..86400, must be a clear 400, not an infra error that rolls the handoff back. → Task 3.
3. An answer that loses a race (resolved, cancelled, expired, worker exited) must close the card quietly (409 `permission_not_pending`), never show an error or re-send. → Task 9.
4. The lease must not lapse while a request is pending (the pane's lease lapses after 2×ttl idle). → Task 9.
5. `permission.*` events must neither pollute the chat messages nor leave the pane's summary stale. → Task 8.
6. A pending request on a row in a list that refreshed later must show 「等待核准」 without opening the pane. → Task 10.
7. The timeout setting must never leave the device through Profile Sync. → Task 6.

## File map

| Area | Files |
|---|---|
| Daemon | `go.mod`/`go.sum`, `internal/module/nex/handoff.go`, `worker_rebuild.go`, `handoff_test.go`, `worker_rebuild_test.go`, new `permission_race_test.go`, `testdata/fake-claude-permission.sh` (+ frames inlined) |
| SPA types / API | `spa/src/lib/nex/types.ts`, `nex-api.ts`, `stores/useNexHostStore.ts`, `components/hosts/nex/nex-config-diff.ts` |
| SPA setting | `stores/useWorkerSettingsStore.ts` (field kept off Profile Sync), `components/settings/WorkerSettingsSection.tsx`, locales |
| SPA handoff | `components/HandoffConfirmDialog.tsx`, `lib/nex/handoff.ts`, `handoff-api.ts`, `worker-rebuild.ts`, `components/execution/WorkerEndedPane.tsx` |
| SPA pane | `lib/nex/event-reducer.ts`, `stores/useExecutionStore.ts`, new `components/execution/PermissionRequestCard.tsx`, new `hooks/usePermissionAnswer.ts`, `components/execution/ExecutionView.tsx` |
| SPA states | `lib/nex/state-dot.ts`, `worker-agent-status.ts`, `lib/nex/worker-tab-title.ts`, `components/executions/ExecutionRowCompact.tsx`, `components/execution/ExecutionHeader.tsx`, `hooks/useTabDisplay.ts` |

## Phases

| PR | Content |
|---|---|
| P-1 | Tasks 1–5 daemon: pin, profile check, timeout in handoff + rebuild, **every ending path preempts the lease (D22)**, answer-vs-end tests; spec corrections. **Mandatory critic. Daemon deploy by the coordinator.** If it exceeds 800 lines and 20 files, split as P-1a (Tasks 1–3) / P-1b (Tasks 4a–5). |
| P-2a | Tasks 6–7 SPA: capability / summary types, answer API, the setting, the dialog choice and body |
| P-2b | Tasks 8–10 SPA: events, request card, 「等待核准」 states |
| Acceptance | spec §6 on mlab in the Mac App, throwaway tmux, haiku, cleaned up |

---

## Phase P-1 — daemon

### Task 1: Pin Nexen v0.19.0

**Files:** `go.mod`, `go.sum`; modify `internal/module/nex/engine_iface.go` only if the build demands it; `spa/src/components/hosts/nex/nex-config-diff.ts:6` is **not** touched here (Task 6).

**Interfaces:** Produces `sandbox.Lookup(name).PermissionChannel`, `execution.Request.PermissionTimeoutS int`, rank order `readonly 0, standard 1, trusted 2, handoff_ask 3, handoff 4`.

- [ ] **Step 1:** `go get lab.protype.tw/wake/nexen@v0.19.0` (tag `3ddfab6`; it is not in the module cache yet — if the proxy cannot resolve it, report NEEDS_CONTEXT, do not `replace` to a local path). Accumulated changes since v0.18.1: v0.18.2 shutdown waits `ctx.Done()`, v0.18.3 pure move, v0.18.4 watchdog, v0.19.0 channel.
- [ ] **Step 2:** `go build ./... && go vet ./... && go test ./... -count=1`. Any failure caused by the bump is fixed here (cite the Nexen change that caused it in the commit body). The import-boundary test (`imports_test.go`) must still pass.
- [ ] **Step 3: Commit** `chore(deps): pin Nexen v0.19.0`.

### Task 2: Handoff profile check looks at the profile actually used

**Files:** `internal/module/nex/handoff.go` (≈ line 158 and 249–252), `handoff_test.go`.

**Behaviour:** `profile := body.Profile, default "handoff"`. The endpoint answers 409 `handoff_unsupported` unless `profile` is in `sandbox.UsableProfiles(policy)`; additionally, when `profile` is `handoff_ask` (or any profile where `sandbox.Lookup(profile).PermissionChannel` is true) it must be usable — nothing else is checked for it. A host with `max_profile = handoff_ask` therefore allows `handoff_ask` and refuses plain `handoff` (as before it refused everything).

- [ ] **Step 1: Failing tests** in `handoff_test.go`: (a) `max_profile: handoff_ask`, body `profile: handoff_ask` → 200 and the delegate request carries `SandboxProfile: handoff_ask`; (b) same host, no profile → 409 `handoff_unsupported`; (c) `max_profile: trusted`, `profile: handoff_ask` → 409 `handoff_unsupported` (Nexen would reject it, the daemon says so first and does not delegate or touch tmux); (d) `max_profile: handoff` both profiles work; (e) existing `TestHandoffRequestHonoursProfileAndClientHeader` (`readonly` passes through) keeps passing — if it only passed because the profile was never checked, change its host to one where `readonly` is usable and say so in the commit.
- [ ] **Step 2:** run → FAIL. **Step 3:** implement. **Step 4:** run `go test ./internal/module/nex -count=1`.
- [ ] **Step 5: Commit** `feat(nex): handoff checks the profile it will use, so handoff_ask works below handoff`.

### Task 3: `permission_timeout_s` in handoff and worker-rebuild

**Files:** `handoff.go` (`handoffRequest`, the `execution.Request` at ≈253–263), `worker_rebuild.go` (`rebuildRequest`, delegate at ≈165–175), the two `_test.go` files.

**Interfaces:** Produces JSON field `permission_timeout_s` (optional integer, seconds) on `POST /api/sessions/{code}/nex-handoff` and `POST /api/nex/worker-rebuild`; error `400 invalid_permission_timeout` with `{field:"permission_timeout_s"}`.

**Behaviour:** absent or `0` → not sent. `< 0` or `> 86400` → 400 `invalid_permission_timeout` **before** any side effect (no delegate, no tmux kill, no row). `> 0` with a profile whose `PermissionChannel` is false → the same 400. Otherwise `PermissionTimeoutS` is set on the delegate request. A `rejected` delegate keeps today's 409 `delegate_rejected` with `reject_reason`.

- [ ] **Step 1: Failing tests** for each endpoint: valid value reaches the delegate request; `-1`, `86401` → 400 and **zero** delegate calls; `600` with `profile: handoff` → 400; `600` with `handoff_ask` → ok; `0`/absent → field not set; Nexen answering `rejected` with `permission channel unavailable` → 409 `delegate_rejected` and the handoff rollback path runs as for any rejected row (existing test pattern).
- [ ] **Step 2:** FAIL. **Step 3:** implement (one shared validator `validatePermissionTimeout(profile string, s *int) *handoffError` in a small new file or in `handoff.go`). **Step 4:** PASS.
- [ ] **Step 5: Commit** `feat(nex): handoff and rebuild accept permission_timeout_s`.

### Task 4a: Every ending path preempts the pane's lease (coordinator ruling R-PC-1, 2026-10-07)

**Why:** the pane's lease now also gates **answers**. Exit, Q1 manual resume and worker-rebuild-with-replace *borrow* a pdx holder's lease (`takeControl` = `borrowPdx`, `control.go:25,81–83`), so the original pane can keep writing (before: sends; now: answers) until the terminate lands. take-to-terminal / take-back already *preempt* (D22: release the holder's lease, acquire an exclusive one). Same reason, same fix.

**Files:** `internal/module/nex/control.go`, `exit.go` (the `takeControl` calls at ≈32 and ≈54, and the `#1665` no-terminate path), `manual_resume.go` (≈280), `worker_rebuild.go` (≈139), tests `control_test.go`, `exit_test.go`, `exit_engine_test.go`, `manual_resume_test.go`, `worker_rebuild_test.go`.

**Behaviour:** `exitWorker` takes control with `preemptPdx` whenever it has to take control itself (no caller lease, no transfer `ctl`). A non-pdx holder still answers `held_by` and nothing changes (D4). A **caller-supplied** lease (the pane's own exit) is still used as is — the caller is the holder. A transfer's `ctl` is still the transfer's. The lease an `exitWorker` took itself is released after the archive; D23's renew / fence rules apply to the preempted lease exactly as to an acquired one (a preempted lease is an acquired one). `exit` must still never fail merely because another pdx tab holds control: the preempted pane lands on the 已退出 screen.

- [ ] **Step 1: Failing tests:** (a) pdx holder + exit with no caller lease → call order `[acquire-attempt held, release(holder), acquire, … terminate, archive, release]`, and the holder's next `CheckLease`/send/answer is refused (`ErrLeaseMismatch`) — in the fake engine and once through the real engine over HTTP; (b) same for Q1 (`manual_resume`) and worker-rebuild with `replace_execution_id`; (c) non-pdx holder → `held_by`, nothing released; (d) caller-supplied lease → unchanged (no preempt); (e) the D16 retry / failed / rejected no-terminate path from #1665 preempts too; (f) preempt loses a race (holder re-attaches between read and release) → the existing single retry then `lease_contended`. Replace `TestExit_BorrowsPdxHolderLeaseNeverReleasesIt` and every assertion that pinned borrow with its preempt counterpart — list each replaced assertion in the report.
- [ ] **Step 2:** FAIL. **Step 3:** implement (one switch of the mode in the three call sites; no new lease logic). **Step 4:** `go test ./internal/module/nex -race -count=1`, then `go test ./...`.
- [ ] **Step 5: Commit** `fix(nex): every ending path preempts the pane's lease (D22), so a pane can no longer write after it`.

### Task 4b: What an answer meets when the worker ends

**Files:** new `internal/module/nex/permission_race_test.go`; fixture `testdata/fake-claude-permission.sh` copied from `/Users/wake/Workspace/wake/nexen/adapter/testdata/fake-claude-permission.sh` with the sibling `stream_permission_request.jsonl` frames **inlined** (the script resolves siblings through `dirname "$0"`); uses `newMountFixtureWith` (`mount_test.go`) with `cfg.Nex.ClaudeBin` set to the script and `MaxProfile: handoff`.

**The three guarantees pinned (spec §5.4, ruling R-PC-1) — no universal "never allow" assertion:**
1. After a preempt, an answer carrying the **old** `lease_id` gets 409 `lease_mismatch` (exit, Q1, rebuild-with-replace, take-to-terminal, take-back).
2. After `interrupt` or `terminate` took effect, any answer gets 409 `permission_not_pending`, and the fixture's log shows the tool never ran.
3. An ending path never turns a `deny`, `cancelled` or `expired` request into `allowed` (assert the request's recorded outcome stays what it was).

**Known residuals — documented, not tested away:** (i) Nexen re-checks the lease before it writes but does not fence the write, so an answer whose check passed just before the preempt can still write; (ii) Q1 detection latency — an allow pressed after the user's terminal resume and before Purdex detects and preempts still reaches the worker (transcript-fork risk is the consumer's, Nexen matrix #84). For (i) assert only that exactly one terminal outcome is recorded per `request_id` (never two, never none).

Also pinned: `interrupt` while pending → `permission.resolved` `cancelled`/`interrupt`; reconcile after a daemon restart → `cancelled`/`daemon_restart`, later answer `permission_not_pending`.

- [ ] **Step 1:** write fixture + tests; record per test RED vs pin; mutation-check the pins (remove the preempt in one path; remove the lease check in the fake engine) and report.
- [ ] **Step 2:** `go test ./internal/module/nex -race -count=3`. **Step 3: Commit** `test(nex): pin what an answer meets when the worker ends`.

### Task 5: Spec corrections

**Files:** `docs/specs/2026-10-07-permission-channel-spec.md` (the *Final names* block: `since` is Unix ms; §5.4 rewritten to the guarantees and residuals of R-PC-1, and D4 / D22 / D23 in the conversation-entity spec updated: exit, Q1 and rebuild now preempt; the preempted lease follows D23's renew / release rules), plan self-record.

- [ ] **Step 1:** edit; **Step 2: Commit** `docs(spec): permission channel — since is Unix ms; §5.4 states what the ending paths guarantee`.

---

## Phase P-2a — SPA: types, setting, dialog

### Task 6: Capability, summary and answer API; the setting

**Files:** `spa/src/lib/nex/types.ts` (`NexCapabilities.permissions?: { profiles: string[]; answer: { method: string; path: string }; timeout?: { max_s: number } }`; `ExecutionSummary.pending_permission?: { request_id: string; tool_name: string; since: number } | null`), `spa/src/lib/nex/nex-api.ts` (`answerPermission`), `spa/src/stores/useNexHostStore.ts` (selectors), `spa/src/components/hosts/nex/nex-config-diff.ts` (add `handoff_ask` to `SANDBOX_PROFILES`, between `trusted` and `handoff`), `spa/src/stores/useWorkerSettingsStore.ts`, `spa/src/components/settings/WorkerSettingsSection.tsx`, locales; tests beside each (+ `lib/profile/projections.test.ts`).

**Interfaces:**
- Produces `selectPermissionAskReady(hostId): boolean` = host `phase==='ready'` && `capabilities.permissions` present && `capabilities.sandbox_profiles` includes `'handoff_ask'`; `selectPermissionTimeoutMax(hostId): number | null` = `capabilities.permissions.timeout?.max_s ?? null`.
- Produces `answerPermission(hostId: string, executionId: string, requestId: string, a: { decision: 'allow' | 'deny'; message?: string; leaseId: string }): Promise<{ request_id: string; outcome: 'allowed' | 'denied' }>` → `POST /v1/executions/{id}/permissions/{request_id}` with body `{decision, message?, lease_id}`; errors keep `code` (`permission_not_pending`, `permission_not_found`, `invalid_permission_answer`, `lease_*`).
- Produces `useWorkerSettingsStore.permissionTimeoutMin: 0 | 5 | 15 | 30 | 60` (default 0), `setPermissionTimeoutMin`.

**Rules:** the new field is in `partialize` (survives reload) but **not** added to any `PROJECTIONS` entry in `lib/profile/projections.ts` (precedent: `useEditorSettingsStore`); a test in `projections.test.ts` asserts `purdex-worker-settings` projects only `theme`, `iconStyle`, `customIcon`. The select 「等待核准逾時」 renders only when some ready host reports `permissions.timeout` (`selectPermissionTimeoutMax !== null`), carries a description line「只影響之後的交接」, and an invalid persisted value sanitizes to 0.

- [ ] **Step 1: Failing tests:** selectors (all four combinations of the two capability conditions, host not ready); `answerPermission` URL/body/errors (mock `fetch` as `nex-api.test.ts` does); store default, setter, sanitize, persistence, **not** projected; section shows/hides the select by capability and writes the store; both locales have the keys.
- [ ] **Step 2:** FAIL. **Step 3:** implement. **Step 4:** focused tests, then `npx tsc --noEmit -p tsconfig.app.json && pnpm run lint`.
- [ ] **Step 5: Commit** `feat(spa): permission capability, answer API and the approval-timeout setting`.

### Task 7: The dialog choice and the body

**Files:** `spa/src/components/HandoffConfirmDialog.tsx`, `spa/src/lib/nex/handoff.ts` (`handToNex`, the `nexHandoff` body at ≈156–161), `handoff-api.ts` (`NexHandoffRequest.permission_timeout_s?`), `spa/src/lib/nex/worker-rebuild.ts`, `spa/src/components/execution/WorkerEndedPane.tsx` (≈78, passes `profile: summary.effective_profile`), `TerminatedPane.tsx` (unchanged: a terminal→worker rebuild keeps the default profile, v1), locales; tests: `HandoffConfirmDialog.test.tsx`, `handoff.test.ts`, `handoff-api.test.ts`, `worker-rebuild.test.ts`.

**Behaviour:**
- Dialog: two radios, **完全放行（預設）** and **需要核准**, shown only when `selectPermissionAskReady(hostId)`; default 完全放行; the body copy explains 需要核准 in one line.
- `需要核准` → body `profile: 'handoff_ask'`, plus `permission_timeout_s = permissionTimeoutMin*60` **only** when `permissionTimeoutMin > 0` **and** `selectPermissionTimeoutMax(hostId)` is non-null. 完全放行 sends no `profile` and no timeout (today's body, byte for byte).
- A 409 `delegate_rejected` shows the `reject_reason`; the dialog never resubmits as 完全放行.
- Rebuild: `worker-rebuild` resends `profile` as today; when `profile === 'handoff_ask'` it also sends the current `permission_timeout_s` under the same two conditions.

- [ ] **Step 1: Failing tests:** radios hidden without capability / without the profile; default is 完全放行 and the body is unchanged (assert exact object); 需要核准 body has `profile` and (with setting 15 and `timeout` capability) `permission_timeout_s: 900`; setting 15 but no `timeout` capability → no timeout key; rejected → reject reason visible and exactly one request made; rebuild of a `handoff_ask` row resends profile + timeout, of a `handoff` row sends no timeout.
- [ ] **Step 2–4:** as Task 6. **Step 5: Commit** `feat(spa): choose 完全放行 or 需要核准 when handing off`.

---

## Phase P-2b — SPA: events, card, states

### Task 8: Permission events in the execution store

**Files:** `spa/src/lib/nex/event-reducer.ts` (`isLifecycleKind` ≈184, the default append ≈528–531), `spa/src/stores/useExecutionStore.ts`, `spa/src/lib/nex/types.ts` (event payload types), tests `event-reducer.test.ts`, `useExecutionStore.test.ts`.

**Interfaces:** Produces per-execution state `permissions: Record<requestId, PermissionRequestState>` where `PermissionRequestState = { requestId: string; toolUseId?: string; toolName: string; displayName?: string; description?: string; input?: unknown; decisionReason?: string; blockedPath?: string; agentId?: string; requestedAt: number; status: 'pending' | 'allowed' | 'denied' | 'cancelled' | 'expired'; timeoutS?: number; reason?: string }` and selector `selectPendingPermission(state): PermissionRequestState | undefined` = the pending request with the smallest `requestedAt` (ties: request_id order) — never derived from `seq`.

**Behaviour:** `permission.requested` creates `pending`; `permission.resolved` sets `status` from `outcome` (and `timeoutS` for `expired`, `reason` for `cancelled`); a `resolved` that arrives **before** its `requested` (replay out of order) creates a resolved entry that a later `requested` must not turn back to pending. Neither kind is appended to `messages`; both set `summaryStale` so the pane refetches `pending_permission`. Duplicate delivery of the same event is idempotent.

- [ ] **Step 1: Failing tests:** requested → pending, not in messages, `summaryStale`; resolved allowed/denied/cancelled/expired; resolved-before-requested; duplicate event; resolved after `tool_result` for the same `tool_use_id` (correlation by `request_id`); two pending → earliest; replay of a recorded event sequence yields the same state as live.
- [ ] **Step 2–4. Step 5: Commit** `feat(spa): reduce permission requests by request_id`.

### Task 9: The request card and answering

**Files:** new `spa/src/components/execution/PermissionRequestCard.tsx`, new `spa/src/hooks/usePermissionAnswer.ts`, `spa/src/components/execution/ExecutionView.tsx` (slot between `WorkerDock` ≈660 and `QuickReplyDock` ≈674), `spa/src/hooks/useExecutionLease.ts` (a `hold` flag), locales; tests for each.

**Interfaces:** Consumes Task 6 `answerPermission`, Task 8 `selectPendingPermission`, `useExecutionLease` (`ensureLease`, `forget`). Produces `usePermissionAnswer(hostId, executionId)` returning `{ answer(req, decision, message?): Promise<void>; busy: boolean; error?: string }`.

**Card:** shows tool name (`displayName ?? toolName`) and description; the input as a bounded preview (first 400 characters of the pretty-printed JSON or the `command` field) with an expand control; `decision_reason` / `blocked_path`; when `agentId` is set a line 「subagent: …」; time waited from `requestedAt` ticking every second. Buttons **同意** and **拒絕**; 拒絕 reveals a one-line note input (`maxLength` 200) whose value becomes `message` (empty note → no `message`, Nexen's default applies). `data-testid`: `permission-card`, `permission-allow`, `permission-deny`, `permission-deny-note`.

**Behaviour:**
- Answer: `ensureLease()` then `answerPermission`. Success or a `resolved` event → the card disappears.
- **409 `permission_not_pending` → close quietly** (no error text, no retry); `permission_not_found` the same.
- `lease_expired` / `lease_mismatch` / `lease_required` → `forget()`, `ensureLease()` once more and answer the **same** `request_id` once; a second failure shows the error line.
- While a request is pending the lease is **held**: `useExecutionLease`'s idle lapse (2×ttl) is suspended and renewal continues; released again when no request is pending.
- Buttons are disabled while the pane runs exit / take-to-terminal / take-back (the existing busy flag in `ExecutionView`), and while an answer is in flight.
- `expired` resolution → the card is replaced by one muted line「已逾時自動拒絕（N 分鐘）」(N = `timeoutS/60`, rounded) that stays until the next turn event; `cancelled` → card disappears, no line.
- A request that arrives after the turn's `result` (background subagent) renders the same.

- [ ] **Step 1: Failing tests:** card content incl. long input truncation and expand; allow → one `answerPermission` call with `decision:'allow'` and the pane's lease; deny with and without note; 409 not_pending → card gone and **no** error element; lease_expired → exactly one re-attach and one retry; two failures → error line; buttons disabled while busy / exiting; lease hold (fake timers: a pending request outlives 2×ttl without the lease being dropped, and it releases after resolution); expired line text for 5/15 minutes; cancelled removes; a card shown for a post-`result` request; the answer is not sent twice on double click.
- [ ] **Step 2–4. Step 5: Commit** `feat(spa): answer a worker's permission request in the pane`.

### Task 10: 「等待核准」 everywhere

**Files:** `spa/src/lib/nex/state-dot.ts`, `worker-agent-status.ts`, `worker-tab-title.ts`, `spa/src/components/executions/ExecutionRowCompact.tsx`, `spa/src/components/execution/ExecutionHeader.tsx`, `spa/src/hooks/useTabDisplay.ts` and `useWorkerAgentProjection.ts`, locales; tests beside each.

**Interfaces:** Produces `isAwaitingApproval(summary): boolean` = `summary.pending_permission != null` (in `lib/nex/worker-summary.ts`, next to `workerTitleOf`).

**Behaviour:** (the summary row from the list, or the live summary, decides — no event stream needed)
- Row (activity bar, New Tab Workers, Settings → Workers): dot `bg-status-warning` plus a small **`HandPalm`** icon (Phosphor) and the text 「等待核准」 in the activity tooltip; **not** the queued look (queued stays a plain warning dot with no icon).
- Pane header: the state text shows 「等待核准」 with the icon.
- Tab label: the worker tab title gets the suffix 「（等待核准）」 (same suffix mechanism as the closed-terminal suffix), removed when `pending_permission` is null.
- Tab light: `projectWorkerStatus` returns `waiting` while awaiting approval (it already exists in `AgentStatus`); verify and test that this does **not** raise a notification (PC2 forbids push) — if the `waiting` path notifies, add the guard in the projection instead of the notification module and say so in the report.
- A summary without the field (old daemon) behaves exactly as before.

- [ ] **Step 1: Failing tests:** each surface with `pending_permission` set / null / absent; queued row still has no icon; tab label suffix on/off; `projectWorkerStatus` table adds the awaiting case; no notification call for the awaiting transition; i18n keys in both locales.
- [ ] **Step 2–4. Step 5: Commit** `feat(spa): show 等待核准 on rows, header and tab`.

---

## Acceptance (after P-2b, on mlab, in the Mac App)

Per spec §6: hand off a throwaway session in 需要核准, ask it to run a Bash command → card → 同意 and the command runs; a second one → 拒絕 with the note「不要動 prod」and the model quotes it; a third with timeout 5 minutes → expires, the pane shows 「已逾時自動拒絕（5 分鐘）」; tab / row / header show 「等待核准」 while pending; exit the worker while a request is pending → the card disappears and nothing runs. haiku, throwaway tmux, cleaned up. (A deploy of P-1 by the coordinator comes first.)

## Self-review record

- **Spec coverage:** §5.1 → Tasks 1, 2, 6; §5.2 → 3, 7; §5.3 → 8, 9; §5.4 → 4, 5, 10; §5.5 → 6, 9; §5.6 non-goals → no tasks (checked: nothing offers the mode outside handoff / rebuild-of-asking-row; New Tab starts untouched).
- **Departures from the spec text, sent to the coordinator 2026-10-07:** (1) §5.4's "D4/D22 already take the lease first" was true only for take-to-terminal / take-back; **ruled R-PC-1: every ending path preempts** (Tasks 4a, 4b, 5); (2) the daemon's handoff profile check (Task 2) and the timeout validation (Task 3) were not in §5.1's text; (3) the timeout setting is device-local (Task 6); (4) rebuild of an asking row resends the timeout (Task 7); (5) tab label is a suffix (Task 10).
- **Type consistency:** `PermissionRequestState` (8) feeds the card (9); `selectPermissionAskReady` / `selectPermissionTimeoutMax` (6) feed 7; `answerPermission` (6) is called only by `usePermissionAnswer` (9); `isAwaitingApproval` (10) reads the field typed in 6.
