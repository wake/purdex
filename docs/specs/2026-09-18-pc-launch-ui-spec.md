# Spec — P-C: exec mode launch UI (Headless section, Executions view, handoff)

- Status: v1 draft (2026-09-18)
- Predecessors: P-A (`2026-09-15-pa-nex-module-spec.md`, nex module + `/api/nex`),
  P-B (`2026-09-15-pb-execution-pane-spec.md`, execution pane + Host → Nex
  page), P-B2 (`2026-09-18-pb2-exec-live-stream-spec.md`, typewriter + tool
  activity). Their transport rules (fetch + Bearer + `X-Pdx-Client`, no
  tickets on `/api/nex/…`) stay binding.
- Contract source of truth: Nexen v0.11.2 (pinned in `go.mod`) —
  `docs/contract/capability-matrix.md` §1.5 (labels), §1.8 (resume), and
  `GET /api/nex/v1/capabilities` (feature-detect, never hard-code).
- Successor: P-D (remove Stream mode, relay, bridge, M0, the legacy
  `/api/sessions/{code}/handoff`). **P-D must not start before §6 of this spec
  has passed on mlab** (kickoff rule: the handoff path replaces relay only
  once it is proven).

## 1. Problem

Executions can only be created from the CLI (`pdx nex delegate`) and only
found on the Host → Nex page. An interactive Claude Code session in a tmux
pane cannot be handed to the engine, and an execution cannot be taken back
into the terminal — the only round trip today is the legacy relay handoff,
which P-D deletes. The kickoff's three target scenarios (commander, phone
takeover, Aigora) all need: a place to **start** an execution, a place to
**see** them per host, and a **hand to nex / take back** pair on a session
pane.

## 2. Goals / non-goals

### Goals

- G1 **Headless section on NewTab**, one per host with nex ready: brief,
  working directory (under an advertised root), sandbox profile — every
  option read from `capabilities` at open time; delegate → the pane becomes
  the new execution.
- G2 **Executions sidebar view** per host: live list of non-archived
  executions (state dot, brief, age), grouped by source, click → execution
  tab. Live via the site-wide SSE the Host page already uses.
- G3 **Hand to nex** on a terminal session pane running Claude Code: the CC
  session id + cwd are read from the daemon's identity record (not from a
  `/status` screen-scrape), CC is interrupted and exited, an execution is
  delegated with `resume_session_id` + `handoff` profile, and the pane turns
  into that execution. The tmux session stays alive with an idle shell.
- G4 **Take back to terminal** on an execution pane that came from a
  session: interrupt the live turn (control lease), then `claude --resume
  <session_id>` (via the host's resume template) in the original tmux
  session, and the pane turns back into the terminal pane.
- G5 A shared per-host **nex readiness + capabilities** cache so NewTab,
  sidebar and session panes agree on "is nex usable here" without each
  probing `/api/info`.
- G6 Pure additions. Nothing in the legacy handoff / relay / Stream mode
  changes; nothing in Nexen changes.

### Non-goals

- Aigora integration itself (only the grouping hook: `labels.source`).
- Labels / origin editing in the form (P-C sets them; a later phase edits).
- A cwd *browser*; the form takes a root select + a relative sub-path text
  field. Server is the authority (F2).
- Auto-adding the Executions view to a sidebar region. Regions default to
  no views (`useLayoutStore.ts:66-71`); the user adds it via RegionManager
  like the file-tree views. NewTab is the discoverable entry.
- Hand-off of anything but Claude Code (`agent_type === 'cc'`); other agents
  see no button.
- Multi-pane sessions: the handoff targets window 0 like the legacy path
  (`sess.Name + ":0"`).
- Permission prompts during a handed-off turn (`-p` never asks; handoff
  profile = `bypassPermissions` + inherited settings, F5).

## 3. Measured facts

Nexen v0.11.2 (`~/tmp/go/pkg/mod/lab.protype.tw/wake/nexen@v0.11.2`):

- F1 `GET /v1/capabilities` (`api/capabilities.go:237-317`): `sandbox_profiles`
  (strictest-first, already clamped to the host's `max_profile`),
  `sandbox_default_profile`, `sandbox_max_profile` (both members of the
  list), `roots: [{path, kind: 'dev'|'service'}]` (canonical, symlink-resolved;
  `[]` when nothing is allowed), `brief.max_bytes` 65536 (**bytes**),
  `origin.max_bytes` 2048, `labels{max_count 32, max_key_bytes 64,
  max_value_bytes 256, max_total_bytes 4096, reserved_prefix 'nex.'}`,
  `delegate.resume_session_id: true` — fail-closed: send the field only when
  the value is exactly `true`.
- F2 `POST /v1/executions` body (`api/handlers.go:36-55`): `{provider, brief,
  sandbox_profile, mounts: [{path, role: 'cwd', writable}], origin?, labels?,
  resume_session_id?}`. Exactly one mount. Always **200** once the body
  parses: `{id, state, reject_reason?, effective_profile?}` — a cwd outside
  every root, a missing transcript, or a non-claude provider is `state:
  'rejected'` with a row; a profile above `max_profile` is **clamped**, not
  rejected (`sandbox/profile.go:100-140`); only an unknown profile name is
  rejected. 400 codes: `invalid_labels`, `reserved_label_namespace`,
  `invalid_origin`, `invalid_brief`, `invalid_resume_session_id`,
  `malformed_body`. pdx adds 503 `nex_unavailable`.
- F3 `resume_session_id`: 36-byte UUID, either case, normalised lowercase
  (`store/execution.go:276-303`). Gate at delegate: transcript must exist at
  `~/.claude/projects/<slug(cwd)>/<sid>.jsonl` — **same cwd, same HOME** as
  the interactive session (matrix §1.8). Turn ≥ 2 resumes the session id the
  CLI reported at turn 1 (`launch.go:288-306`), which can differ from the
  requested one → "take back" must read the summary's `session_id`.
- F4 `interrupt` needs a control lease (`service.go:950-955`), waits for
  process exit, settles to `idle` (`turn.go:546-549`); before the turn-1
  `system/init` handshake it settles to `failed`. `archive` needs no lease
  and is refused while running (409). Nexen never locks the transcript
  (`addressing.go:57-68`); exclusivity is the consumer's job.
- F5 `handoff` profile (`sandbox/profile.go:61-94, 228-240`): rank 3
  (`readonly < standard < trusted < handoff`), argv
  `--setting-sources user,project,local --permission-mode bypassPermissions
  --tools default`, no `--strict-mcp-config` → hooks/plugins/MCP load. Must
  be requested explicitly; reachable only when the host config has
  `max_profile = "handoff"`. mlab's `~/.config/pdx/config.toml` has it
  (measured 2026-09-18). Handoff turns run on ambient identity (no token
  injected, `account_id` empty). Nexen issue #73: a handoff turn may rewrite
  the host credential store — production nexen keeps `max_profile =
  "trusted"`; Purdex's embedded engine is the user's own machine, accepted.
- F6 `GET /v1/executions`: `limit` ≤ 500, `cursor`, `state`, `include_archived`,
  repeatable `label.<k>=<v>`; items carry `origin` and `labels`. Site-wide
  `GET /v1/events` emits every lifecycle kind with `brief`/`text` stripped.

Purdex today (this worktree, alpha.379):

- F7 NewTab: `lib/new-tab-registry.ts` — `NewTabProviderSource` gives one
  provider per host (`lib/session-new-tab-providers.tsx:17-63`: cached
  wrapper component per host id, subscribes to `useHostStore` +
  `persist.onFinishHydration`, `isReady = hasHydrated()`); layout auto-places
  unknown provider ids (`useNewTabLayoutStore.ts:223-240`);
  `onSelect(content)` **replaces the new-tab pane in place**
  (`register-modules/index.tsx:72-88`). Launcher pattern to mirror:
  `session-launcher/SessionLauncher.tsx` (liveness gate, `busy`/`error`,
  `launcher-*` i18n, `data-testid="launcher-error"`).
- F8 Sidebar: no per-host section registry; `SidebarRegion.tsx` renders a
  `ViewDefinition {id, label, icon, scope, component}` from
  `registerModule({views})` with `hostId = activeHostId ?? hostOrder[0]`
  (`:26, :171-177`). Only the two file-tree views exist. Per-host list
  patterns: `SessionPanel.tsx` (orphaned), `SessionSection.tsx:73-170`.
  Open/activate: `useTabStore.openSingletonTab(content)` (`:504-522`).
- F9 Nex data: `useNexHostData` (`hosts/nex/useNexHostData.ts`) exposes
  `{phase, info, config, …}` from `/api/info` + `/api/config`, mount-scoped,
  no SSE, no executions, no capabilities. `NexExecutionsTable.tsx` owns the
  list (`listExecutions(hostId, {includeArchived, limit: 100})`) + site-wide
  SSE as a 500 ms-debounced refetch signal (`:120-152`); capabilities are
  fetched in `NexEngineStatus.tsx:67-70`. **No store holds either.**
  `HostInfo.nex` (`lib/host-api.ts:59-68`) is not cached anywhere.
- F10 `nex-api.ts` has no `delegate`; `types.ts` has `NexCapabilities`
  (typed subset, `[key]: unknown` rest) and `ExecutionSummary.session_id?`
  but no `resume_session_id`.
- F11 Execution pane content: `{kind: 'execution'; executionId; host?}`
  (`types/tab.ts:100`); singleton match `pane-utils.ts:57-60`; route
  `/execution/<host>/<id>`; `resolveExecutionHostId` returns the hint or
  `hostOrder[0]`.
- F12 Session identity: every own-frame hook event updates the daemon's
  `session_identity` (`internal/module/agent/frame_ops.go:238,285,425,967`),
  exposed by `GET /api/sessions/{code}/provenance` →
  `{found, agent_type, session_id, cwd, tmux_pane_id, tmux_instance,
  last_seen_at}` (`provenance_handler.go:49`). `PdxSessionEnd` (fired by
  `/exit`) **deletes** the frame (`frame_ops.go:184`) → read identity
  **before** exiting. Fallbacks: the pane's persisted
  `rebuild.agent.sessionId` / `rebuild.cwd` (`types/tab.ts:44-80`, survives
  exit), then `session.cc_session_id`.
- F13 CC operator (`internal/agent/cc/operator.go`, `CCOperator` interface):
  `Interrupt(ctx, target)` = `C-u`, `C-c`, poll readiness to idle; `Exit(ctx,
  target)` = cancel/Escape/`C-c`/Escape spaced 500 ms, `/exit`, poll until
  `!IsAliveFor("cc")`. Readiness is a screen-scrape (`cc/readiness.go`).
  Liveness: `Prober.IsAliveFor("cc", target)` (`probe/liveness.go:148`).
  Legacy `runHandoff` locks per session (`stream/locks.go`, 409 on
  double-click).
- F14 Sending a command into a pane from the SPA:
  `POST /api/sessions/{code}/send-keys {keys, expected_tmux_instance}`
  (`session/handler.go:373-432`, generation-guarded, 409 on mismatch), SPA
  wrapper `lib/rebuild/transport.ts:130-145` (`sendKeys(code, command,
  tmuxInstance)`). Resume command per host from
  `lib/rebuild/composer.ts:27 resolveResumeCommand` + `lib/resume-templates.ts`
  (default cc `claude --resume {id}`).
- F15 `SessionPaneContent.tsx:19-130` knows `hostId`, `sessionCode`, `mode`,
  `tmuxInstance`, `session` (`name`, `cwd`, `cc_session_id`); terminal mode
  has **no per-pane action toolbar**; `StatusBar.tsx:546-571` hosts the
  view-mode dropdown. Lease handling for an execution pane lives in
  `hooks/useExecutionLease.ts` (attach control / renew / release).

## 4. Design

### 4.1 Shared: `useNexHostStore` (G5)

`spa/src/stores/useNexHostStore.ts` — per host:

```ts
interface NexHostEntry {
  info: NexInfo | null            // from GET /api/info .nex
  capabilities: NexCapabilities | null
  phase: 'unknown' | 'loading' | 'ready' | 'disabled' | 'unavailable'
  error: string | null
  fetchedAt: number
}
ensure(hostId): Promise<void>     // fetch once per host, dedup in flight, refetch when older than 60 s or after `invalidate`
invalidate(hostId)                // called on host reconnect (host-lifecycle cascade) and by the Nex page after config save
selectReady(hostId): boolean      // phase === 'ready'
```

- `phase` = `disabled` when `!info.configured || !info.mounted`,
  `unavailable` on `nex_unavailable`/network/`init_error`, `ready` when
  `isNexReady(info)` (existing `nex-ready.ts`) **and** capabilities loaded.
- `NexCapabilities` in `types.ts` gains typed `brief`, `origin`, `labels`,
  `delegate` per F1 (still `[key]: unknown` for the rest).
- `useNexHostData` and `NexEngineStatus` are **not** rewritten in P-C
  (avoid touching the Host page); they may read the store later (issue).
- Cleared by `clearHost` in the same cascade as `useExecutionStore.clearHost`.

### 4.2 P-C.1 — Headless section on NewTab (G1)

- `nex-api.ts` gains
  `delegateExecution(hostId, req: DelegateRequest): Promise<DelegateResult>`
  with `DelegateRequest = {brief, cwd, profile?, labels?, origin?,
  resume_session_id?}` mapped to F2's body (`provider: 'claude'`,
  `mounts: [{path: cwd, role: 'cwd', writable: true}]`, `sandbox_profile`);
  `resume_session_id` is only put on the wire when the caller passes it
  **and** `capabilities.delegate.resume_session_id === true` — otherwise
  `delegateExecution` rejects with `NexApiError(0, 'resume_unsupported')`
  before any request. `DelegateResult = {id, state, reject_reason?,
  effective_profile?}`; HTTP 400 codes surface as `NexApiError(400, code)`.
- Provider source `headless-new-tab-providers.tsx` mirroring F7: id
  `headless:<hostId>`, label `newtab.headless.title` with `{host}`, icon
  Phosphor `Lightning`, `order` after the sessions section, `isReady`
  = host store hydrated. The wrapper component calls
  `useNexHostStore.ensure(hostId)` on mount and renders:
  - `phase === 'disabled'` → one line `newtab.headless.disabled` (+ link text
    "Hosts → Nex"); `unavailable` → `newtab.headless.unavailable` with the
    error; `loading` → skeleton.
  - `ready` → `HeadlessLauncher` form:
    - **Brief**: textarea, required, counter `bytes/ max_bytes` (UTF-8 byte
      length, F1), error above limit.
    - **Directory**: `<select>` of `roots` (path + kind badge) + a text
      field "sub-path" (relative, no `..` segments, may be empty); the
      submitted `cwd` is `root + '/' + sub`. Client-side check is a hint only;
      the server's `rejected` is the authority (F2). `roots.length === 0` →
      form disabled with `newtab.headless.no_roots`.
    - **Profile**: `<select>` of `sandbox_profiles`, preselected
      `sandbox_default_profile`; a muted note shows `sandbox_max_profile`.
    - Submit: liveness gate (`isHostLive`), `busy` flag, `delegateExecution`
      with `labels: {source: 'purdex'}` and `origin:
      'purdex://host/<hostId>/newtab'`. `state === 'rejected'` → inline error
      `newtab.headless.rejected` with `reject_reason`; 400 → mapped message;
      otherwise `onSelect({kind: 'execution', executionId: id, host: hostId})`
      (the pane becomes the execution, F7).
    - Last-used root/profile per host remembered in the launcher's local
      persisted store (like the session launcher's project memory), keyed by
      host.
- Registered from `register-modules/index.tsx` next to the sessions source;
  `unregisterNewTabProvidersByModule('execution')` on disable.

### 4.3 P-C.2 — Executions sidebar view (G2)

- `useHostExecutions(hostId)` hook + `useExecutionListStore` (per host:
  `items: ExecutionSummary[]`, `phase`, `error`, `lastSeq`) extracted from
  `NexExecutionsTable.tsx:94-152`'s list + site-wide-SSE-as-refetch-signal
  logic. **One** site-wide SSE per host, refcounted by subscribers
  (the table and the sidebar view share it); 500 ms debounce; refetch on
  reconnect; gated by `useNexHostStore.selectReady`. `NexExecutionsTable`
  switches to the hook in the same PR (pure behaviour move, its 256-line test
  file is the guard).
- `ViewDefinition {id: 'executions', label: 'Executions', icon:
  Phosphor `Lightning`, scope: 'system', component: ExecutionsView}`
  registered on the `execution` module (`register-modules/index.tsx:227-231`).
  `ExecutionsView({hostId})`:
  - header: host name + nex phase dot; `disabled`/`unavailable` → one-line
    hint (same keys as 4.2).
  - list: non-archived, newest first, grouped by `labels.source ?? 'local'`
    (group header i18n: `executions.group.local`, `executions.group.purdex`,
    any other value shown raw — this is the Aigora hook, no Aigora code);
    row = state dot (`STATE_DOT_CLASSES` from `NexExecutionRow.tsx`),
    brief (1 line, ellipsis), relative age of `updated_at`, and a
    `↩ from <session>` marker when `origin` starts with
    `purdex://host/<hostId>/session/`.
  - click → `openSingletonTab({kind: 'execution', executionId, host: hostId})`.
  - empty → `executions.empty`.
- `HostInfo.nex` stays uncached elsewhere; only the store reads it.

### 4.4 P-C.3 — Hand to nex / take back (G3, G4)

#### Daemon: one small endpoint, outside the stream module

`POST /api/sessions/{code}/cc-exit` (session module or a new
`internal/module/nex/handoff.go` — the nex module already depends on the
session provider; **not** in `internal/module/stream`, which P-D deletes).
Body `{expected_tmux_instance}`. Behaviour:

1. Per-session `TryLock` (reuse `stream/locks.go`'s type by moving it to
   `internal/module/session/locks.go` — pure move — so both callers share
   it; 409 `handoff_in_progress`).
2. Generation check like `send-keys` (409 `tmux_instance_mismatch`).
3. `Prober.IsAliveFor("cc", name+":0")` → else 409 `no_cc`.
4. `CCOperator.Interrupt` when readiness ≠ idle (10 s), then
   `CCOperator.Exit` (10 s). Failure → 504 `cc_exit_timeout` with the pane's
   last state.
5. 200 `{exited: true}`. No mode change, no relay, no `cc_session_id` write.

Everything else is SPA-orchestrated so the resume template, lease code and
pane content stay where they already live.

#### SPA: `handToNex(hostId, sessionCode, tmuxInstance)` (`lib/nex/handoff.ts`)

1. `useNexHostStore.ensure(hostId)`; require `phase === 'ready'` and
   `capabilities.delegate.resume_session_id === true` and
   `sandbox_profiles.includes('handoff')` → else throw `handoff_unsupported`
   (button is hidden in that case, see UI).
2. Identity: `GET /api/sessions/{code}/provenance`; require `found &&
   agent_type === 'cc' && session_id` and `tmux_instance ===
   expectedTmuxInstance`; `cwd` from the same response. Fallback when
   `!found`: the pane's `rebuild.agent.sessionId` + `rebuild.cwd` if both
   present; else throw `no_identity`. (F12 — must run **before** step 3.)
3. `POST /api/sessions/{code}/cc-exit {expected_tmux_instance}`.
4. `delegateExecution(hostId, {brief: '(handed off from tmux session
   <name>)', cwd, profile: 'handoff', resume_session_id, labels: {source:
   'purdex', handoff_session: code}, origin:
   'purdex://host/<hostId>/session/<code>'})`. `state === 'rejected'` →
   throw `rejected:<reason>` — the shell is idle in the pane and the user can
   `claude --resume` by hand (the toast says so; no automatic rollback,
   F4/F3 make a second delegate safe to retry).
5. Pane content ← `{kind: 'execution', executionId, host: hostId, from:
   {sessionCode, tmuxInstance, cachedName}}` (`types/tab.ts` gains the
   optional `from`; `contentMatches` ignores it).

#### SPA: `takeBack(hostId, executionId, from)` (`lib/nex/handoff.ts`)

1. `getExecution` → if `state === 'running'`: `attachControl` → `interrupt`
   → `release` (reuse `useExecutionLease`'s functions; `lease_held` → throw
   `held_by:<principal>`). Then re-`getExecution`; require `state ∈ {idle,
   failed, terminated}`.
2. `sid = summary.session_id ?? summary.resume_session_id` (F3) — none →
   throw `no_session_id`.
3. Compose `resolveResumeCommand(hostId, 'cc', sid)` (F14) and `sendKeys(code,
   command, from.tmuxInstance)` (generation-guarded; 409 → throw
   `tmux_restarted`).
4. Pane content ← `{kind: 'tmux-session', hostId, sessionCode, mode:
   'terminal', cachedName, tmuxInstance}`. The execution is left `idle`
   (not archived) so it can be handed off again; the Executions view shows
   it under "purdex" with the `↩` marker.

#### UI

- Terminal session pane: a small action in the pane header (the same place
  the split/close controls live — measure in plan) **"Hand to nex"**, shown
  only when `useNexHostStore.selectReady(hostId)` and the readiness reasons
  in step 1 hold and the pane's agent is `cc` (from `rebuild.agent.type` or
  `session.cc_session_id`/provenance). Busy state + toast on error
  (`handoff.error.<code>`).
- Execution pane header (`ExecutionHeader.tsx`): **"Take back to terminal"**
  when `content.from` is set. Confirm dialog when a turn is running
  ("interrupt the current turn?").
- Progress is local to the pane (busy spinner on the button); no WS
  `handoff` events (those belong to the relay path).

### 4.5 What does not change

- `internal/module/stream`, `internal/relay`, the legacy `/handoff`
  endpoint, `useStreamStore`, `ConversationView`, `HandoffButton`.
- Nexen: pin stays v0.11.2.
- `useExecutionStore`, the reducer, `nex-sse.ts` (the site-wide stream
  reuses `openNexSse` as today).

## 5. Phases

Three PRs, in order, each independently shippable.

- **P-C.1** — `useNexHostStore` (4.1), `delegateExecution` + typed
  capabilities (4.2), Headless NewTab section. Tests: store (phase matrix,
  dedup, invalidate, clearHost), api (body mapping, fail-closed
  `resume_session_id`, 400 mapping, `rejected` passthrough), provider source
  (one per host, ready gating), launcher (byte counter, root select +
  sub-path validation, rejected/400/network rendering, success calls
  `onSelect` with the execution content, last-used memory).
- **P-C.2** — `useHostExecutions` + store (refcounted SSE), `ExecutionsView`
  + registration, `NexExecutionsTable` on the hook. Tests: hook (single
  SSE per host with two subscribers, debounce, reconnect refetch, gating),
  view (grouping, marker, click opens singleton tab, disabled/unavailable
  hints), table tests unchanged.
- **P-C.3** — daemon `cc-exit` (+ lock move), `handoff.ts` (`handToNex`,
  `takeBack`), pane content `from`, header actions. Go tests: lock 409,
  generation 409, `no_cc`, interrupt-then-exit ordering with a fake
  operator, timeout → 504. SPA tests: step order (provenance **before**
  cc-exit — a test that asserts the request sequence), fallback identity,
  rejected → no pane change + toast, success → pane content swap; take back:
  running → interrupt path with lease, `held_by`, `session_id` preference
  over `resume_session_id`, 409 generation, pane swap.

## 6. Acceptance (mlab, after each PR; playwright cli session `pc-launch-ui`)

1. NewTab → Headless (mlab): roots list shows `~/Workspace` (canonical
   path); brief "reply with the word ok, no tools", default profile →
   execution pane opens, result arrives. Over-limit brief blocks submit.
   Sub-path `../x` blocks submit; a sub-path outside the root submitted via
   devtools returns `rejected` and the form shows the reason.
2. Sidebar → add "Executions": the execution from step 1 appears under
   "purdex", state dot flips idle; second tab shows the same list within
   ~1 s (one SSE per host — check `pdx` logs / network tab shows one
   `/v1/events` per host per tab).
3. tmux session with interactive CC (`claude` in `~/Workspace/wake/nex-acceptance-scratch`),
   say one thing, then "Hand to nex": the pane becomes an execution whose
   history shows the earlier exchange (resumed), the tmux window shows an
   idle shell, `pdx nex show <id>` has `resume_session_id` + `effective_profile:
   handoff`. Send a follow-up from the pane: hooks fire (statusline/lights
   visible in Purdex — F5).
4. "Take back": the pane is a terminal again running `claude --resume`, the
   conversation continues with the follow-up from step 3 visible;
   `pdx nex show` state `idle`.
5. Hand to nex on a session without CC → toast `no_cc`; on a host with
   `max_profile = "trusted"` → button hidden.
6. Double-click "Hand to nex" → second call gets 409, one execution only.

## 7. Risks

- **Screen-scraped readiness/exit** (F13) is the same fragility the legacy
  path has; P-C reuses the operator rather than rewriting it. If `Exit` times
  out the pane is left with CC still running and nothing delegated — the
  toast says "CC did not exit; nothing changed".
- **Two writers on one transcript** if a user hands off and then runs
  `claude --resume` by hand while the execution runs (F4: Nexen does not
  lock). Mitigation: the pane is the execution now, and the Executions view
  shows it; documented, not enforced.
- **Handoff profile = bypassPermissions with inherited settings** (F5): a
  handed-off turn can do anything the interactive session could, without
  prompts. This is the user's explicit choice per handoff; the button label
  says "no permission prompts".
- **`session_id` drift** (F3): covered by reading the summary in take-back.
- **Site-wide SSE fan-in**: one per host per client, refcounted; the 4-per-host
  live cap from P-B applies to execution panes, not to this stream.

## 8. Open questions

- Q1 Where exactly does the "Hand to nex" control sit on a terminal pane
  (pane header vs StatusBar view-mode dropdown)? Plan measures the header
  component and picks; default = pane header next to the split control.
- Q2 Should take-back `archive` the execution? Default no (re-handoff is
  cheaper than re-delegate; F4 makes archive refuse while running anyway).
