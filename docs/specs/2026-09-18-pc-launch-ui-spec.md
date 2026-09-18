# Spec — P-C: exec mode launch UI (Headless section, Executions view, handoff)

- Status: v1.3 (2026-09-18) — codex spec review `task-mu6iu5ek-1jb811` applied; P-C.2 fix wave (§9)
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
- G6 Legacy relay / Stream mode **semantics** unchanged (files touched only
  for the shared lock type, see 4.5); nothing in Nexen changes. The existing
  Host → Nex components migrate onto the shared stores (4.1, 4.3) so there
  is one source of nex truth in the SPA, not four.

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
- F14 Sending a command into a pane: `POST /api/sessions/{code}/send-keys
  {keys, expected_tmux_instance}` (`session/handler.go:373-432`,
  generation-guarded, 409 on mismatch); the SPA wrapper is
  `pinHost(hostId).sendKeys(code, command, tmuxInstance)`
  (`lib/rebuild/transport.ts:83, 130-145`) — not a top-level function. The
  daemon's own `tmux.Executor.SendKeys(target, keys)` appends Enter
  (`internal/tmux/executor.go:298`). Resume command per host:
  `lib/rebuild/composer.ts:27 resolveResumeCommand(record: PaneRebuildRecord,
  templates)` + `lib/resume-templates.ts` (default cc `claude --resume {id}`)
  — P-C adds a sibling `resolveResumeCommandFor(templates, agentType, id)`
  because take-back has no rebuild record.
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
- `NexEngineStatus` (`hosts/nex/NexEngineStatus.tsx:67-70`) reads
  `capabilities` and readiness from this store in P-C.1 instead of its own
  `Promise.all` (its tests move onto the store). `useNexHostData` keeps
  owning `/api/config` (the editable form) but takes `info`/`phase` from the
  store, and calls `invalidate(hostId)` in `onConfigSaved`. One truth for
  "is nex ready here": this store. (codex §9.5)
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
    - **Brief**: textarea, required, counter `bytes / max_bytes` measured
      with `new TextEncoder().encode(text).length` (F1 counts UTF-8 bytes),
      submit blocked above the limit.
    - **Directory**: `<select>` of `roots` (canonical path + kind badge) + a
      text field "sub-path". Client rules (hint only; the server's
      `rejected` is the authority, F2): relative only — rejects a leading
      `/`, a leading `~`, any `..` segment, and empty segments (`a//b`); no
      expansion of `~` or env vars; trailing `/` trimmed. Submitted `cwd` =
      `root.path + '/' + sub` (or `root.path` when empty). Everything the
      client cannot know — the directory does not exist, a symlink inside
      the root escapes it — comes back as `state: 'rejected'` with
      `reject_reason` and is shown inline, never as a 400. `roots.length ===
      0` → form disabled with `newtab.headless.no_roots`.
    - **Profile**: `<select>` of `sandbox_profiles`, preselected
      `sandbox_default_profile`; a muted note shows `sandbox_max_profile`.
    - Submit: liveness gate (`isHostLive`), `busy` flag, `delegateExecution`
      with `labels: {source: 'purdex'}` and `origin:
      'purdex://host/<hostId>/newtab'`. `state === 'rejected'` → inline error
      `newtab.headless.rejected` with `reject_reason` (the row exists; the
      Executions view will show it as rejected); HTTP 400 (`invalid_brief`,
      `invalid_labels`, `invalid_origin`, `malformed_body` — client bugs or
      limit drift) → `newtab.headless.bad_request` with the code; 503
      `nex_unavailable` / network → `newtab.headless.unavailable` and the
      store is invalidated; otherwise `onSelect({kind: 'execution',
      executionId: id, host: hostId})` (the pane becomes the execution, F7).
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
  reconnect; gated by info readiness (`isNexReady(byHost[hostId].info)`),
  the same predicate the Host → Nex table used before P-C.2 — the list
  needs the engine to be serving, not the capabilities document; gating on
  `selectReady` would make the table appear later than it did before (codex
  plan review `task-mu6lptei-rhye4d` §11). `NexExecutionsTable`
  switches to the hook in the same PR (pure behaviour move, its 256-line test
  file is the guard).
- **Connection budget** (codex §9.7): the site-wide stream is a long-lived
  connection to the same host:port as the execution panes' live SSEs, and
  P-B capped those at `MAX_LIVE_SUBSCRIPTIONS_PER_HOST = 4` to leave REST
  room under the browser's 6-per-origin limit. `lib/nex/subscription-slots.ts`
  gains `reserve(hostId, 'site-wide')` / `unreserve`; while a reservation is
  held the execution-pane cap for that host is **3** (the LRU eviction rule
  runs immediately on reserve). The list store holds the reservation for as
  long as its SSE is open.
- `ViewDefinition {id: 'executions', label: 'Executions', icon:
  Phosphor `Lightning`, scope: 'system', component: ExecutionsView}`
  registered on the `execution` module (`register-modules/index.tsx:227-231`).
  `ExecutionsView({hostId})`:
  - header: host name + nex phase dot; `disabled`/`unavailable` → one-line
    hint (same keys as 4.2).
  - list: non-archived, newest first, grouped by `labels.source ?? 'local'`
    (group header i18n: `executions.group.local`, `executions.group.purdex`,
    any other value shown raw — this is the Aigora hook, no Aigora code);
    row = state dot (`STATE_DOT_CLASSES` is a file-local const in
    `NexExecutionRow.tsx:19-26` today — P-C.2 moves it to
    `lib/nex/state-dot.ts` and both consumers import it),
    brief (1 line, ellipsis), relative age of `updated_at`, and a
    `↩ from <session>` marker when `origin` starts with
    `purdex://host/<hostId>/session/`.
  - click → `openSingletonTab({kind: 'execution', executionId, host: hostId})`.
  - empty → `executions.empty`.
- `HostInfo.nex` stays uncached elsewhere; only the store reads it.

### 4.4 P-C.3 — Hand to nex / take back (G3, G4)

Both directions are **daemon-orchestrated under one per-session lock** (the
legacy `runHandoff` shape, F13); the SPA only composes the resume command,
asks, and swaps pane content. Rationale (codex spec review §10.1): a thin
`cc-exit` endpoint releases the lock between "CC exited" and "execution
delegated", leaving half states on double-click, second client, rejected
delegate, or tmux restart. In the daemon the whole sequence is atomic per
session and the embedded Nexen `Service` is a Go call, not an HTTP hop.

#### Daemon: `POST /api/sessions/{code}/nex-handoff`

Lives in `internal/module/nex/handoff.go` (**not** `internal/module/stream`,
which P-D deletes). The nex module today declares no dependencies
(`module.go:91`) and its engine seam keeps only the HTTP handler; P-C.3a
makes it depend on `session` + `agent` (session provider, owner resolver,
prober, CC operator via the registry), widens the seam to carry the
embedded `Service`/`Store` behind narrow interfaces, and relaxes the
package's import-boundary test to admit `nexen/execution` and
`nexen/store`.
The per-session lock type moves from `internal/module/stream/locks.go` to
`internal/module/session/locks.go` as a pure move (both callers share it;
stream's imports/tests are updated — see §4.5).

Body: `{expected_tmux_instance, profile?, rollback_command?}`.
`rollback_command` is the host's resume template already rendered by the SPA
with the session id placeholder left as `{id}` (e.g. `claude --resume {id}`,
`cld-yolo --resume {id}`); the daemon substitutes the id it read. Steps,
all inside `TryLock(code)` (409 `handoff_in_progress`):

1. Generation: sample `TmuxInstance()`; mismatch with `expected_tmux_instance`
   → 409 `tmux_instance_mismatch`. Re-sampled **after step 3 and before
   any key is sent**, and again after step 4. A change after exit → 409
   `tmux_instance_mismatch` `{after_exit: true, rolled_back: false,
   session_id}` with **no rollback**: a new generation is a different tmux
   server, so the pane we exited no longer exists to receive a resume
   command; the SPA shows the session id for a manual resume.
2. Identity: the agent module's provenance resolver (exported as a provider
   interface the nex module receives at `Init`, like `session` is today) →
   `{found, agent_type, session_id, cwd}`. `!found || agent_type != "cc" ||
   session_id == ""` → 409 `no_identity` (F12: must run **before** exit).
3. `Prober.IsAliveFor("cc", name+":0")` → else 409 `no_cc`.
4. `CCOperator.Interrupt` when readiness ≠ idle (10 s), then
   `CCOperator.Exit` (10 s). Timeout → 504 `cc_exit_timeout`; CC may still be
   running; nothing delegated; lock released.
5. Delegate via the embedded `nexen.System.Service` (Go): `provider claude`,
   `brief "(handed off from tmux session <name>)"`, mount `{cwd, role cwd,
   writable}`, `sandbox_profile = profile ?? "handoff"`, `resume_session_id`,
   `labels {source: purdex, handoff_session: <code>}`, `origin
   purdex://host/<hostId>/session/<code>` (hostId = this daemon's id).
   Principal = the request's principal (same as `/api/nex` would derive).
6. Outcome:
   - `state != rejected` → 200 `{execution_id, state, effective_profile,
     session_id, cwd}`.
   - `rejected` (F2: cwd outside roots, transcript missing, …) or delegate
     error → **rollback**: if `rollback_command` was given, `SendKeys(target,
     render(rollback_command, session_id))` and wait `IsAliveFor("cc")` ≤ 15 s;
     respond 409 `delegate_rejected` `{reject_reason, rolled_back: bool}`.
     Without a rollback command the shell is left idle and `rolled_back:
     false` tells the SPA to say so.
7. No `cc_session_id` write, no mode change, no WS `handoff` event.

Precondition checks the daemon makes before step 1 (cheap, no lock):
`m.sys` ready (else 503 `nex_unavailable`); `handoff` ∈
`sandbox.UsableProfiles(policy)` (else 409 `handoff_unsupported`).
`delegate.resume_session_id` is a property of the pinned Nexen build, not
of host config, so it is pinned by a test that serves `GET /v1/capabilities`
through the real assembled handler and asserts `true` — a pin bump that
drops it fails the suite instead of failing handoffs at runtime.

#### Daemon: `POST /api/sessions/{code}/nex-takeback`

Same file, same lock. Body: `{expected_tmux_instance, execution_id,
resume_command, lease_id?}`.

1. **Preflight, before touching the execution**: session `code` exists in
   the session provider (else 404 `session_missing`); generation matches
   (else 409 `tmux_instance_mismatch`); `!IsAliveFor("cc", target)` (else 409
   `cc_already_running` — the user already resumed by hand).
2. Execution: `Service.Get(execution_id)`; `state == running` → needs a
   control lease: if `lease_id` given and valid, `Service.Interrupt` with it;
   otherwise acquire one (`AttachControl`) → interrupt → release. `lease_held`
   by someone else → 409 `held_by` `{principal}`. Interrupt timeout → 504
   `interrupt_unconfirmed`, nothing else done. After interrupt re-`Get`;
   require `state ∈ {idle, failed, terminated}`.
3. `sid = summary.session_id ?? summary.resume_session_id` (F3); none → 409
   `no_session_id`.
4. `SendKeys(target, render(resume_command, sid))`, wait `IsAliveFor("cc")`
   ≤ 15 s (else 504 `cc_start_timeout`; the execution is already idle — the
   response says which step failed so the SPA can offer "retry resume").
5. `Service.Archive(execution_id)` (no lease needed, idle) — **archive on
   success** (codex §10.3): the terminal is now the writer of that
   transcript; a second handoff creates a new execution. Archive failure is
   logged, not fatal (200 with `archived: false`).
6. 200 `{session_id, archived}`.

#### SPA (`lib/nex/handoff.ts`, imperative, no hooks)

- `handToNex(hostId, sessionCode, tmuxInstance, cachedName)`:
  `useNexHostStore.ensure(hostId)` (button is hidden unless ready +
  `handoff` ∈ profiles + `delegate.resume_session_id`); compose
  `rollback_command` from the host's cc resume template
  (`resolveResumeCommandFor(templates, 'cc', '{id}')` — a new small helper
  beside `composer.ts:27 resolveResumeCommand(record, templates)` that takes
  agent type + id instead of a rebuild record; F14); `POST nex-handoff`; on
  200 → pane content ← `{kind: 'execution', executionId, host: hostId,
  from: {sessionCode, tmuxInstance, cachedName}}`. If the swap throws (pane
  gone) → toast with an "open execution" action (`openSingletonTab`). On 409
  `delegate_rejected` → toast `handoff.error.rejected` with the reason and
  whether the terminal was restored.
- `takeBack(hostId, executionId, from, leaseId?)`: compose
  `resume_command` the same way; `POST nex-takeback` with the pane's current
  `lease_id` when this tab holds one (the pane's lease hook exposes it; no
  hook is called from `lib/`); on 200 → pane content ← `{kind:
  'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName,
  tmuxInstance}`. `404 session_missing` / `409 tmux_instance_mismatch` →
  toast, **execution pane untouched, no interrupt happened** (daemon
  preflight guarantees it). `409 held_by` → toast with the principal.
- Both send `expected_tmux_instance` from the pane content / `from`.

#### UI

- Terminal session pane: **"Hand to nex"** in the pane header (plan
  measures the header component; Q1), shown only when
  `useNexHostStore.selectHandoffReady(hostId)` and the pane's agent is `cc`
  (`rebuild.agent.type === 'cc'` or `session.cc_session_id`). Confirm dialog
  text states "no permission prompts (handoff profile)". Busy spinner; toast
  on error (`handoff.error.<code>`).
- Execution pane header (`ExecutionHeader.tsx`): **"Take back to terminal"**
  when `content.from` is set. When `summary.state === 'running'` the confirm
  dialog says the turn will be interrupted. After success the execution is
  archived; the Executions view drops it.
- `contentMatches` (`pane-utils.ts:57-60`) ignores `from`; the route builder
  ignores it; persistence keeps it (it is plain data on `PaneContent`).

### 4.5 Blast radius (what changes, what stays)

Legacy relay **semantics** are unchanged: `internal/module/stream`'s
orchestrator, `/api/sessions/{code}/handoff`, `useStreamStore`,
`ConversationView`, `HandoffButton` keep their behaviour. Files that are
touched anyway: `internal/module/stream/{handler,orchestrator}.go` +
tests (import the moved lock type), `spa/src/types/tab.ts` (`execution.from`),
`pane-utils.ts` tests (matching ignores `from`), `ExecutionHeader.tsx`,
`SessionPaneContent.tsx` (or the pane header component), `locales/*.json`,
`NexEngineStatus.tsx` / `NexExecutionsTable.tsx` (migrated to the shared
stores, 4.1/4.3), `lib/nex/subscription-slots.ts` (site-wide reservation,
4.3), `lib/rebuild/composer.ts` (new helper). Nexen: pin stays v0.11.2.
`useExecutionStore`, the reducer, `nex-sse.ts`: untouched.

## 5. Phases

Three PRs, in order, each independently shippable.

- **P-C.1** — `useNexHostStore` (4.1, including `NexEngineStatus` reading
  readiness/capabilities from it), `delegateExecution` + typed capabilities
  (4.2), Headless NewTab section. Tests: store (phase matrix, dedup,
  invalidate, clearHost), api (body mapping, fail-closed
  `resume_session_id`, 400 mapping, `rejected` passthrough), provider source
  (one per host, ready gating), launcher (byte counter via `TextEncoder`,
  root select + sub-path rules, rejected/400/network rendering, success
  calls `onSelect` with the execution content, last-used memory),
  `NexEngineStatus` tests moved onto the store.
- **P-C.2** — `useHostExecutions` + list store with refcounted site-wide SSE
  and the reserved connection slot (4.3), `ExecutionsView` + registration,
  `NexExecutionsTable` on the hook. Tests: hook (single SSE per host with
  two subscribers, debounce, reconnect refetch, gating), slots (cap 3 while
  reserved, back to 4 on release), view (grouping, marker, click opens
  singleton tab, disabled/unavailable hints), table tests unchanged.
- **P-C.3** — daemon `nex-handoff` + `nex-takeback` (+ lock move, provenance
  provider interface), `handoff.ts`, `resolveResumeCommandFor`, pane content
  `from`, header actions. Go tests with fake operator/prober/service:
  lock 409; generation 409 before and after exit; `no_identity` before any
  key is sent (assert operator never called); `no_cc`; interrupt-then-exit
  ordering; exit timeout → 504 and no delegate; rejected → rollback keys
  sent with the substituted id and `rolled_back: true`; take-back preflight
  order (session missing → no interrupt call; generation mismatch → no
  interrupt); `held_by`; `session_id` preferred over `resume_session_id`;
  archive on success / archive failure non-fatal. SPA tests: request bodies,
  pane swaps, toast per error code, swap-failure recovery action.

## 6. Acceptance (mlab, after each PR; playwright cli session `pc-launch-ui`)

1. NewTab → Headless (mlab): roots list shows `~/Workspace` (canonical
   path); brief "reply with the word ok, no tools", default profile →
   execution pane opens, result arrives. Over-limit brief blocks submit.
   Sub-path `../x` blocks submit; a sub-path that does not exist on disk
   submitted normally returns `rejected` and the form shows the reason.
2. Sidebar → add "Executions": the execution from step 1 appears under
   "purdex", state dot flips idle; second tab shows the same list within
   ~1 s. Open 4 execution panes + the sidebar in one tab: a REST call
   (`pdx nex ls` equivalent from the Nex page) still answers promptly
   (connection budget, 4.3); network tab shows one `/v1/events` without
   `execution_id` per host.
3. tmux session with interactive CC (`claude` in `~/Workspace/wake/nex-acceptance-scratch`),
   say one thing, then "Hand to nex": the pane becomes an execution whose
   history shows the earlier exchange (resumed), the tmux window shows an
   idle shell, `pdx nex show <id>` has `resume_session_id` + `effective_profile:
   handoff`. Send a follow-up from the pane: hooks fire (statusline/lights
   visible in Purdex — F5).
4. "Take back": the pane is a terminal again running `claude --resume`, the
   conversation continues with the follow-up from step 3 visible;
   `pdx nex ls --all` shows the execution archived.
5. Hand to nex on a session without CC → toast `no_cc`; hand to nex with the
   scratch dir temporarily outside roots (edit config, restart daemon) →
   `delegate_rejected`, terminal restored (`rolled_back: true`, CC prompt
   back). On a host with `max_profile = "trusted"` → button hidden.
6. Double-click "Hand to nex" → second call gets 409, one execution only.
   Kill the tmux session, then "Take back" → 404 `session_missing`, execution
   still running, pane untouched.

### 6.1 Acceptance run 2026-09-18 (P-C.1, at `2b5496ff`)

mlab, worktree dev server `npx vite --host 100.64.0.2 --port 5175 --strictPort`,
playwright cli session `pc-launch-ui`, host `pc1host` seeded into
`purdex-hosts` localStorage with the daemon token, daemon alpha.378 (nex
`configured: true, ready: true`). Capabilities measured via curl: `roots
[{path: "/Users/wake/Workspace", kind: "dev"}]`, `sandbox_profiles
[readonly, standard, trusted, handoff]`, default `standard`, max `handoff`,
`brief.max_bytes 65536`, `delegate.resume_session_id true`. All executions
archived afterwards; scratch dir removed.

1. **PASS** — New Tab shows "Headless — mlab" after Editor / Storage /
   Sessions · mlab. Directory select lists `/Users/wake/Workspace` with the
   `dev` badge (canonical path, matches capabilities); profile select
   preselects `standard`; note "host allows up to handoff"; Launch disabled
   with an empty brief.
2. **PASS** — brief "reply with the word ok, no tools", sub-path
   `wake/nex-acceptance-scratch`, default profile → URL became
   `/execution/pc1host/06GB6GNZWVT3F69YZWSM42FY64`, header
   `claude · standard`, reply paragraph `ok` arrived, `$0.03`. `pdx nex show`:
   `origin: purdex://host/pc1host/newtab`, `labels: {source: purdex,
   nex.host, nex.provider}`, `effective_profile: standard`.
3. **PASS** — 70 000-byte brief: counter `70000 / 65536 bytes` rendered in
   the error colour (screenshot), Launch disabled.
4. **PASS** — sub-path `../x` → inline `.. segments are not allowed`
   (`headless-subpath-error`), Launch disabled. Sub-path
   `does-not-exist-q7x2m9` → Launch enabled → server answered `state:
   rejected`; the form showed `Rejected: mount
   "/Users/wake/Workspace/does-not-exist-q7x2m9" is not under any
   allowlisted root` (Nexen's resolver canonicalises via `EvalSymlinks`, so a
   missing directory fails the root match with this wording — F2 as
   expected). The pane stayed on New Tab. Hosts → Nex table listed the row
   `06GB6H1MC4DM` with state `rejected`.
5. **PASS** — launched once with profile `readonly` (execution
   `06GB6H9B1W9CB3DMDPWMWZ27HR`, header `claude · readonly`);
   `purdex-headless-launcher` localStorage held `{pc1host: {root:
   "/Users/wake/Workspace", profile: "readonly"}}`; after a full page reload
   a fresh New Tab preselected `/Users/wake/Workspace` and `readonly`.
6. **PASS** — console: 3 messages, 0 errors, 0 warnings.

### 6.2 Acceptance run 2026-09-18 (P-C.2, at `2e9019ca`)

mlab, worktree dev server `npx vite --host 100.64.0.2 --port 5175 --strictPort`,
playwright cli session `pc-launch-ui`, host `pc2host` seeded into
`purdex-hosts` (version 1, daemon token) then navigated with `goto` — note
`open` recreates the context and the seed is lost; `goto` keeps it. Daemon
alpha.378. All executions archived afterwards; the Executions view removed
from the region again (`primary-sidebar.views` back to
`["file-tree-workspace"]`); scratch dir removed.

1. **PASS** — Headless section launch (`reply ok, no tools`, sub-path
   `wake/nex-acceptance-scratch`) → `/execution/pc2host/06GB7156KB46T161926B1EREEG`,
   reply `ok`.
2. **PASS** — RegionManager lists "Executions" under 可加入; after Add it
   renders: header `mlab` with the nex phase dot, group **Purdex**, one row
   `idle · reply ok, no tools · just now`, no `↩` marker (NewTab origin).
3. **PASS** — `pdx nex delegate` (no source label) → row appeared under
   **Local** as `running` within 2 s without reload
   (`06GB71CPGG1THZXZB9C3DH41GR`); `pdx nex archive` → gone within 2 s.
4. **PASS** — clicking a row opens the execution tab; clicking again
   focuses the same tab (single `tab "Execution"`, same ref).
5. **PASS** — connection budget: three more executions
   (`06GB71HQJ5…`, `06GB71HRQM…`, `06GB71HT1K…`), four execution tabs +
   sidebar. Every tab reads `live` when viewed (activation claims a slot and
   pauses the hidden LRU sibling), and the network log shows the cap in
   action: after the four initial `events?execution_id=` streams
   (#587–#614) each tab activation re-opened its stream (#622–#646) — with a
   cap of 4 no pane would ever have been paused and re-opened. Site-wide
   `/api/nex/v1/events` (no `execution_id`): exactly **one** `200` (#575;
   #573 was aborted by the view remount when the manager was toggled), and
   the count stayed at one with Hosts → Nex open alongside the sidebar.
   Hosts → Nex table **Refresh** completed (`/v1/executions` 200) within the
   2 s window with 3 pane streams + the site-wide stream open.
6. **PASS** — second browser tab shows the same list; a CLI delegate
   (`06GB724GGFR325RPV4VYDCVJFW`) appeared as `running` within 1.5 s and
   vanished within 1.5 s of `pdx nex archive`.
7. **PASS** — console: both tabs 0 errors, 0 warnings.

## 7. Risks

- **Screen-scraped readiness/exit** (F13) is the same fragility the legacy
  path has; P-C reuses the operator rather than rewriting it. Exit timeout
  leaves CC running and nothing delegated — reported as `cc_exit_timeout`.
- **Rollback is best-effort**: if `SendKeys` of the resume command itself
  fails after CC exited, the shell is idle and the response says
  `rolled_back: false`; the user resumes by hand. The session id is in the
  response for that purpose.
- **Handoff profile = bypassPermissions with inherited settings** (F5): a
  handed-off turn can do anything the interactive session could, without
  prompts. Per-handoff explicit choice; the confirm dialog says so.
- **`session_id` drift** (F3): covered by reading the summary in take-back.
- **Two writers**: closed by archive-on-takeback; the only remaining window
  is a user running `claude --resume` by hand while an execution is live,
  which no layer can detect (F4) — documented.
- **Connection budget**: the site-wide SSE takes a reserved slot per host;
  the execution live cap drops to 3 while it is held (4.3).

## 8. Open questions

- Q1 Where exactly does the "Hand to nex" control sit on a terminal pane
  (pane header vs StatusBar view-mode dropdown)? Plan measures the header
  component and picks; default = pane header next to the split control.
- Q2 (resolved v1.1) Take-back archives the execution; re-handoff creates a
  new one.

## 9. Review log

- v1 → v1.1, codex `task-mu6iu5ek-1jb811` (gpt-5.5), 10 findings, all
  applied: (1) P1 thin `cc-exit` cut → daemon-orchestrated `nex-handoff`
  under one lock with rollback; (2) P1 take-back interrupts before checking
  the tmux session → preflight first, daemon-side; (3) P1 idle execution =
  two writers → archive on successful take-back; (4) P1 hook called from
  `lib/` → daemon owns the lease dance, SPA passes `lease_id` only; (5) P2
  fourth nex truth source → `NexEngineStatus` migrates in P-C.1, table in
  P-C.2; (6) P2 cwd rules (relative only, no `~`, `TextEncoder` bytes,
  rejected vs 400); (7) P2 site-wide SSE counted in the per-host connection
  budget (reserved slot, cap 4→3); (8) P2 wrong wiring facts
  (`STATE_DOT_CLASSES` not exported, `resolveResumeCommand(record,
  templates)`, `pinHost().sendKeys`) → corrected in 4.3/4.4/F14; (9) P3 §4.5
  rewritten as blast radius; (10) P2 P-C.3 not shippable as written →
  redesigned per (1)–(4).
- v1.2 — P-C.2 fix wave: list gate is info readiness (drift resolved in
  favour of the plan); validation at the API boundary; refresh revision per
  attempt.
- v1.3 — P-C.3a plan review `task-mu6om8rv-beojrl` (9 findings applied):
  nex module dependencies/seam/import boundary stated as they are; second
  generation sample placed after the liveness check; after-exit generation
  change does not roll back (different tmux server); resume-support
  precondition pinned by a capabilities test; delegate infra error shares
  the `delegate_rejected` rollback path; acquired lease released on every
  exit path; `store.ErrNotFound` vs other store errors distinguished.
