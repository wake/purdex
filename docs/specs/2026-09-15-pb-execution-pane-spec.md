# Spec — P-B: execution pane on the Nexen SSE transport + Host "Nex" page

- Status: draft v1 (2026-09-15)
- Predecessor: `2026-09-15-pa-nex-module-spec.md` (P-A, shipped alpha.346/347).
  §4.3 of that spec is **binding** here: fetch + `Authorization: Bearer` +
  `Last-Event-ID` header, `X-Pdx-Client` per-client principal suffix, no
  tickets on `/api/nex/…`.
- Contract source of truth: `nexen/docs/contract/{consumer-guide,capability-matrix}.md`
  and `GET /api/nex/v1/capabilities` (feature-detect, never hard-code).
- Successors: P-B2 (typewriter deltas + server-side tool summaries, blocked on
  Nexen N2), P-C (launch UI / sidebar / handoff), P-D (rip out stream/relay/M0).

## 1. Problem

P-A mounted Nexen inside the pdx daemon at `/api/nex`, but nothing in the SPA
can consume it. The only conversation renderer (`ConversationView`) is wired to
the legacy Stream mode (`stream-ws.ts` WebSocket + relay + `useStreamStore`
keyed by tmux session), and the `{kind:'execution'}` pane still renders the
dead M0 projection (`GET /api/execution/{id}`). There is also no way to see or
change `[nex]` from the app: `PUT /api/config` rejects the key, `GET /api/info`
only says `{configured, mounted}`, and a bad `[nex]` block or a failed
`nexen.Assemble` takes the whole daemon down (`Init` error → `log.Fatalf`).

## 2. Goals / non-goals

### Goals

- G1 A browser client for the Nexen HTTP+SSE contract living in the SPA:
  `nex-api.ts` (REST verbs) + `nex-sse.ts` (fetch/ReadableStream SSE with
  resume) + `useExecutionStore` keyed by `(hostId, executionId)`.
- G2 `{kind:'execution'}` pane renders a Nexen execution with the existing
  message renderer (`MessageBubble`/`ToolCallBlock`/`ThinkingBlock`/
  `ToolResultBlock`) and `StreamInput`; send and interrupt work through a
  control lease that the pane acquires lazily and releases when it goes away.
- G3 Host page gains a **Nex** sub-page: edit `[nex]` (persisted, restart to
  apply) and monitor the engine (mount state, effective config, account/quota,
  capabilities, execution list with open/terminate/archive).
- G4 The daemon survives a broken `[nex]`: `Init` failure is recorded, not
  fatal; `PUT /api/config` validates `nex` before persisting.
- G5 Each sub-phase (P-B.1/.2/.3) is independently reviewable and shippable.

### Non-goals (later phases)

- Typewriter rendering of `stream_event` / `stream_snapshot` (P-B2). The
  reducer **ignores** transient frames in P-B; the store shape leaves room.
- Server-side tool summaries / diffs / cost hover (P-B2, needs Nexen N2).
- NewTab "Headless" section, sidebar Executions list, deeplink polish,
  session-pane "hand to nex" / "take back to terminal" (P-C; needs nexen #65).
- Removing Stream mode, relay, bridge, M0 execution/dispatch (P-D).
- Hot-applying `[nex]` changes without a daemon restart (follow-up issue; the
  engine is assembled once at `Init`).
- Remote daemon restart button (follow-up issue; no restart endpoint exists).
- Permission prompts / `control_request` UI for executions: `-p` never asks;
  Nexen P4 owns the decision channel. The existing prompt components stay
  wired to Stream mode only.
- Nexen-side changes. Everything here consumes the v0.9.0 API as pinned by P-A.

## 3. Current state consumed by this phase

| Thing | Where | Notes |
|---|---|---|
| Nexen REST + SSE | `/api/nex/v1/…` behind the general auth chain | verbs: delegate/list/get/events/attach/renew/release/send/interrupt/archive/terminate/capabilities/host |
| Principal | `X-Pdx-Client: <id>` → `pdx:<host_id>/<id>`; absent → `pdx:<host_id>` | id must match `^[A-Za-z0-9._-]{1,64}$` |
| SSE | `GET /v1/events[?execution_id=&kind=]`, resume via `Last-Event-ID` header only; transient frames carry no `id:` | site-wide stream strips `brief`/`text` |
| History | `GET /v1/executions/{id}/events?after=&limit=` forward-only, `next_cursor` 0 = last page; server clamps `limit` to `MaxPageSize` | attach(observe) returns `cursor` to page from |
| Lease | TTL 120 s default (`capabilities.lease.ttl_seconds`); renew ≈ TTL/3; `renew`/`release` paths declared in capabilities | 409 codes: `lease_required`, `lease_expired`, `lease_mismatch`, `lease_held` |
| Summary | `executionView` (`id,state,provider,principal_id,cwd,brief,labels,origin,effective_profile,reject_reason,terminal_reason,last_turn_reason,session_id,observers,archived,event_count,duration_ms`; single-get adds `turn_count,live_turn_id,lease{principal_id,expires_at}`) | states: `queued running idle rejected failed terminated` |
| Message shapes | provider passthrough: `assistant/user/result/system/rate_limit_event/control_response` payload = raw stream-json | identical to `StreamMessage` in `stream-ws.ts` |
| `/api/info` | `nex: {configured, mounted}` | `mounted` = module was added (enabled at boot) |
| `PUT /api/config` | rejects `nex` with 400 | `NexConfig.Validate(home)` exists and covers roots/profiles/abs paths/durations |
| Host page | built-in sub-pages via `setHostBuiltinSections([...])` (overview/sessions/hooks/agents/uploads/logs) | `OverviewSection` is the pattern for info+config fetch |
| Execution pane | `ExecutionPaneWrapper` → `ExecutionDetailPage` (M0) | route `/execution/<id>` already parsed by `route-utils.ts`; `host` hint optional |
| Stream renderer | `ConversationView` (352 lines) mixes store reads, send/permission handlers, file attach, and the message list | list rendering is the reusable half |

## 4. Design

### 4.1 Sub-phases

```
P-B.1  client + store       spa/src/lib/nex/{client-id,nex-api,nex-sse,event-reducer}.ts
                            spa/src/stores/useExecutionStore.ts
P-B.2  execution pane       ConversationMessages (extracted), ExecutionView, useExecutionSubscription,
                            useExecutionLease, ExecutionPaneWrapper rewired, M0 page deleted
P-B.3  Host Nex page        daemon: nex soft-fail Init, /api/info.nex extended, PUT /api/config nex
                            spa: NexHostSection (+ NexConfigForm, NexEngineStatus, NexExecutionsTable)
```

P-B.1 has no UI and is fully unit-testable against a fake `fetch`. P-B.2 and
P-B.3 are independent of each other after P-B.1 lands (P-B.3's table opens
panes that P-B.2 renders; if P-B.3 ships first the row's "open" button just
lands on the P-B.2-less pane, which is acceptable for a day). Ship order is
.1 → .2 → .3; each is one PR with the two-round codex review.

### 4.2 P-B.1 — client and store

#### 4.2.1 Client id (`spa/src/lib/nex/client-id.ts`)

```ts
export function getNexClientId(): string   // stable for the life of this browser tab
```

- Stored in `sessionStorage['purdex-nex-client-id']`; generated as
  `t-<8 base36 chars>` from `crypto.getRandomValues`; regenerated per tab,
  never shared between tabs. Electron windows each own a `sessionStorage`,
  so two windows get two principals (that is the point: the daemon must be
  able to tell "this tab holds the lease" from "another tab holds it").
- Always matches `^[A-Za-z0-9._-]{1,64}$`. If `sessionStorage` is unavailable
  (throws), fall back to a module-level constant generated once per page
  load — same semantics, just not surviving reload.
- `useSyncStore.clientId` (per device, persisted) is **not** reused: two tabs
  on one device would collapse to one principal.

#### 4.2.2 REST client (`spa/src/lib/nex/nex-api.ts`)

All calls go through one helper:

```ts
function nexFetch(hostId: string, path: string, init?: RequestInit): Promise<Response>
// = hostFetch(hostId, `/api/nex${path}`, init) with headers
//   X-Pdx-Client: getNexClientId()
//   Content-Type: application/json (when a body is present)
```

Typed wrappers (names, not exhaustive signatures):

| Function | Endpoint | Returns |
|---|---|---|
| `fetchNexCapabilities(hostId)` | `GET /v1/capabilities` | `NexCapabilities` (only the fields we read are typed: `phase,host_id,verbs,providers,sandbox_profiles,sandbox_default_profile,sandbox_max_profile,roots,lease{ttl_seconds,renew,release},transient_events,send{max_text_bytes}`; the rest `[key: string]: unknown`) |
| `fetchNexHost(hostId)` | `GET /v1/host` | `{active_account, quota: null \| {…}}` |
| `listExecutions(hostId, {state?, includeArchived?, cursor?, limit?})` | `GET /v1/executions` | `{items: ExecutionSummary[], next_cursor}` |
| `getExecution(hostId, id)` | `GET /v1/executions/{id}` | `ExecutionSummary` (with `lease`, `turn_count`, `live_turn_id`) |
| `fetchExecutionEvents(hostId, id, {after, limit})` | `GET /v1/executions/{id}/events` | `{items: NexEvent[], next_cursor}` |
| `attachObserve(hostId, id)` | `POST …/attach {mode:'observe'}` | `{stream_url, cursor, state}` |
| `attachControl(hostId, id)` | `POST …/attach {mode:'control'}` | `{lease_id, expires_at}` |
| `renewLease(hostId, id, leaseId)` | `POST …/attach/renew` | `{lease_id, expires_at}` |
| `releaseLease(hostId, id, leaseId)` | `DELETE …/attach` | void |
| `sendMessage(hostId, id, leaseId, text)` | `POST …/messages` | `{turn_id, delivery}` |
| `interruptExecution(hostId, id, leaseId)` | `POST …/interrupt` | `{turn_id, state, reason?}` |
| `archiveExecution(hostId, id, undo?)` / `terminateExecution(hostId, id, leaseId?)` | `POST …/archive` / `…/terminate` | void |

Errors: every non-2xx is thrown as `NexApiError {status, code, message,
turn_id?}` parsed from the structured body `{error, code}`; a body that does
not parse yields `code: 'http_<status>'`. Callers switch on `code`, never on
status alone. `execution_not_found` (404) is surfaced, not swallowed.

`stream_url` from attach(observe) is **origin-relative** (already contains
`/api/nex`): the SSE layer resolves it against `getDaemonBase(hostId)`'s
origin and must not prepend `/api/nex` again. (I1)

#### 4.2.3 SSE transport (`spa/src/lib/nex/nex-sse.ts`)

```ts
export interface NexSseFrame { id: string | null; event: string; data: string }
export interface NexSseOptions {
  hostId: string
  url: string                       // origin-relative, e.g. /api/nex/v1/events?execution_id=…
  lastEventId?: string | null       // initial resume cursor
  onFrame: (f: NexSseFrame) => void
  onStatus: (s: 'connecting' | 'open' | 'reconnecting' | 'closed', err?: Error) => void
}
export function openNexSse(opts: NexSseOptions): { close(): void }
```

- Transport is `fetch` with `Accept: text/event-stream`, the host's auth
  headers (`getAuthHeaders`), `X-Pdx-Client`, and `Last-Event-ID` when a
  cursor is known; body read through `ReadableStream` + `TextDecoder`
  (`stream: true`). An `AbortController` backs `close()`.
- Parser follows the SSE spec subset Nexen emits: `id:`, `event:`, `data:`
  (multi-line joined with `\n`), comment lines (`:` keepalive) ignored, blank
  line dispatches. A frame with no `id:` line is dispatched with `id: null`
  — that is how the reducer tells transient from durable. **The transport
  never updates the cursor itself**; the reducer owns it (see 4.2.4), and
  reconnects read it back through a `getLastEventId()` callback option so a
  cursor advanced by history paging while the socket was down is honoured.
- Reconnect: on network error / non-2xx / stream end while not closed →
  `reconnecting`, exponential backoff 1 s → 2 → 4 → … capped at 30 s with
  ±20 % jitter, reset to 1 s after a connection that stayed open ≥ 10 s.
  `401`/`403` stop the loop (auth is the host's problem, surface `closed`
  with the error); `503 draining` retries. Reconnect is a **new fetch with
  the current `Last-Event-ID`** — no ticket, no URL change.
- `close()` is idempotent and stops any pending reconnect timer.

#### 4.2.4 Reducer and store

`spa/src/lib/nex/event-reducer.ts` is a pure function so it can be tested
exhaustively without React or fetch:

```ts
export interface ExecutionState {
  summary: ExecutionSummary | null
  messages: StreamMessage[]           // provider passthrough only, in seq order
  lastSeq: number                     // highest durable seq applied (history or SSE)
  historyLoaded: boolean
  sse: 'idle' | 'connecting' | 'open' | 'reconnecting' | 'closed'
  sseError: string | null
  lease: { leaseId: string; expiresAt: number } | null      // lease *this tab* holds
  leaseError: { code: string; heldBy?: string } | null
  pendingSend: boolean                 // send in flight (input disabled)
  lastTurn: { turnId: string; delivery: 'delivered' | 'queued' } | null
}
export function applyDurableEvent(s: ExecutionState, ev: NexEvent): ExecutionState
```

Rules:

- `NexEvent = {seq, execution_id, kind, payload, created_at}` — the same
  object from the history page and from an SSE frame (`data` parsed; `id` →
  `seq`).
- Idempotent by seq: `ev.seq <= s.lastSeq` → return `s` unchanged (history
  page and replay overlap is harmless).
- `kind ∈ {assistant, user, result, system, rate_limit_event, control_response}`
  (any kind **not** starting with `execution.` / `lease.` / listed in
  `transient_events`) → `messages.push(payload as StreamMessage)`. Provider
  kinds are an open set; unknown non-lifecycle kinds are still pushed (the
  renderer ignores what it cannot draw).
- `execution.message_accepted` → push a synthetic `user` message built from
  `payload.text` **only if** the last message is not already an identical
  synthetic one (the pane appends an optimistic bubble on send; the durable
  event confirms it). On the site-wide stream `text` is stripped — the
  reducer never sees that stream (site-wide is P-B.3's list refresh only).
- `execution.*` lifecycle → patch `summary.state` / `last_turn_reason` /
  `terminal_reason` / `observers` from the payload where present; do **not**
  invent fields the payload lacks.
- `lease.acquired` / `lease.released` → patch `summary.lease`; if the
  released/acquired principal is not ours and we hold a lease locally, keep
  ours (server is authoritative; the next renew tells us if we lost it).
- Frames with `id: null` (`stream_event`, `stream_snapshot`, `lease.renewed`)
  → **dropped** in P-B (P-B2 adds a partial buffer).
- `result` → also flips `pendingSend` false.

`spa/src/stores/useExecutionStore.ts` (zustand + `subscribeWithSelector`),
keyed by `compositeKey(hostId, executionId)` (split with `lastIndexOf(':')`
per [[reference_session_code_cross_host_collision]] — execution ids are
`exc_…` with no colon, but the helper is shared):

```ts
executions: Record<string, ExecutionState>
setSummary / applyEvents(hostId, id, events: NexEvent[]) / setHistoryLoaded
setSse(hostId, id, status, err?) / setLease / setLeaseError / setPendingSend / setLastTurn
appendLocalUser(hostId, id, text)          // optimistic bubble
clearExecution(hostId, id) / clearHost(hostId)
```

`clearHost` is called from the same host-lifecycle hook that clears
`useStreamStore` today (`host-lifecycle.ts`). The store holds **no**
connections; subscriptions live in hooks (4.3.2) so HMR and StrictMode
double-mount cannot leak sockets through the store.

### 4.3 P-B.2 — execution pane

#### 4.3.1 Extract `ConversationMessages`

`ConversationView.tsx` is split, behaviour-preserving for Stream mode:

- `ConversationMessages.tsx` — props `{ messages: StreamMessage[]; keyPrefix: string; showThinking: boolean; children?: ReactNode }`. Owns the scroll
  container, auto-scroll-on-append, the empty-state text, and the
  `assistant`/`user` block mapping (thinking / text / tool_use /
  tool_result / interrupted / slash-command bubbles) exactly as today.
  `children` renders after the list (Stream mode passes its pending control
  prompts).
- `ConversationView.tsx` keeps the Stream-specific parts (store reads,
  relay/handoff banners, permission handlers, file attach) and renders
  `<ConversationMessages …>{prompts}</ConversationMessages>` + `StreamInput`.
- Existing `ConversationView.test.tsx` keeps passing unchanged; the block
  rendering tests are duplicated against `ConversationMessages` directly.

#### 4.3.2 Hooks

`useExecutionSubscription(hostId, executionId, active: boolean)`:

1. `getExecution` → `setSummary`. `execution_not_found` → state
   `notFound` on the pane (stable landing, like the M0 page).
2. `attachObserve` → `{cursor, stream_url}`. History: page
   `fetchExecutionEvents` from `after = 0` with `limit = 500` until
   `next_cursor === 0` **or** `after >= cursor`, applying each page; then
   `setHistoryLoaded`. (Forward-only paging means the first render waits for
   the whole history; executions are bounded by turn count and this is
   acceptable for P-B. A "load older" window is a P-B2 concern.)
3. `openNexSse({url: stream_url, getLastEventId: () => store.lastSeq})`;
   frames → `applyEvents` (durable) or dropped (transient). Status →
   `setSse`.
4. Cleanup on unmount / host removal / executionId change: `close()`.
   The observe SSE stays open while the pane is **inactive** (an observer
   counts toward `ask_human` waiting; keeping the tab's view current is
   cheap). It closes when the pane is closed.

`useExecutionLease(hostId, executionId)` returns
`{ lease, leaseError, ensureLease(): Promise<string>, release(): Promise<void> }`:

- `ensureLease` is lazy: if a live lease exists (`expiresAt - now > 5 s`)
  return it; else `attachControl`. `409 lease_held` → `setLeaseError({code,
  heldBy: summary.lease?.principal_id})` and throw. Concurrent callers share
  one in-flight promise.
- While a lease is held: renew timer at `ttl/3` (ttl from
  `capabilities.lease.ttl_seconds`, fetched once per host and cached in a
  module-level map; default 120 s if the fetch fails). Renew errors
  `lease_expired`/`lease_mismatch` → drop local lease silently (next send
  re-acquires). Any other error → keep trying at the same cadence.
- **Idle policy**: if the pane has not sent or interrupted for `2 × ttl`,
  the timer stops renewing and the lease is allowed to expire (a phone or
  another tab can take over without the desktop actively holding it). The
  next send re-acquires. (I3)
- `release()` on: pane content change away from this execution, pane close,
  tab close, host removal, `beforeunload` (best-effort `fetch` with
  `keepalive: true`), and component unmount. Idempotent; ignores errors.

#### 4.3.3 `ExecutionView`

Replaces `ExecutionDetailPage` (deleted along with `execution-api.ts` and
their tests; `resolveExecutionHostId` moves to `lib/nex/`). Layout:

```
┌ header ────────────────────────────────────────────────────────────┐
│ ● running  claude · standard · ~/Workspace/wake/purdex             │
│ observers 2 · lease: pdx:mlab/t-a1b2c3d4 (you) · turns 7 · $0.42   │
│ [Interrupt] [Terminate]                          SSE: open         │
├ ConversationMessages ──────────────────────────────────────────────┤
│ …                                                                  │
├ StreamInput ───────────────────────────────────────────────────────┤
└────────────────────────────────────────────────────────────────────┘
```

- Header reads `summary`; cost is summed from `result.total_cost_usd` over
  `messages` (same as Stream today). Lease line shows `(you)` when
  `summary.lease.principal_id` ends with `/${getNexClientId()}`; otherwise the
  raw principal. `leaseError.code === 'lease_held'` renders an inline notice
  "Held by <principal> — try again when released" and keeps the input
  enabled (retry is a send).
- **Send**: `ensureLease()` → `appendLocalUser` → `setPendingSend(true)` →
  `sendMessage`. `delivery: 'queued'` shows a small "queued" tag on the
  bubble until the next `execution.message_accepted`/`assistant` arrives.
  Errors: `NexApiError.code` mapped to an i18n line under the input
  (`execution_archived`, `execution_terminal`, `invalid_text`,
  `turn_failed_to_launch` …); unknown codes show the server message.
- **Interrupt**: `ensureLease()` → `interruptExecution`; `no_live_turn` is
  a no-op; `interrupt_unconfirmed` (504) shows a warning, state stays as the
  server says.
- **Terminate** asks for confirmation with a two-click button ("Terminate"
  → "Confirm terminate", reverting after 4 s) — there is no shared confirm
  dialog outside the editor storage feature — and does not require a lease unless
  the server says `lease_required` (then `ensureLease` and retry once).
- Input disabled while `pendingSend`, or when `summary.state ∈ {rejected,
  failed, terminated}` or `archived`, with the reason as placeholder text.
- `system` init messages update a small model badge (as Stream does).
- Pane `content.host` hint resolution: `resolveExecutionHostId(host)`
  unchanged (known host or first host). Additionally the pane content gains
  nothing new — `{kind:'execution', executionId, host?}` stays.

#### 4.3.4 What is *not* touched

`useRelayWsManager`, `useMultiHostEventWs` relay events, `stream-ws.ts`,
`useStreamStore`, `SessionPaneContent` stream branch, handoff buttons. P-D
removes them; P-B only extracts the shared renderer.

### 4.4 P-B.3 — Host "Nex" page

#### 4.4.1 Daemon: soft-fail Init (`internal/module/nex/module.go`)

- `Init` no longer returns the assemble/config/data-dir error. It stores it
  in `m.initErr`, logs `nex: init failed (module stays unmounted): …`, and
  returns nil. `RegisterRoutes` then mounts a handler that answers every
  `/api/nex/…` request with `503 {"error": "<initErr>", "code": "nex_unavailable"}`
  (same structured error shape as Nexen so clients need one parser).
  `Start`/`Stop`/`Close` are no-ops in that state.
- Config **validation** errors (`NexConfig.Validate`) are still fatal at
  config load (unchanged) — they are caught at `PUT` time (4.4.2), so the
  only way to hit them is hand-editing `config.toml`, and a daemon that
  refuses a config it cannot parse is the existing rule for every section.
- `Core.Mounted("nex")` keeps meaning "added". A new `Module.Ready() bool`
  is not introduced; instead the nex module exposes `Status()` (4.4.2).

#### 4.4.2 Daemon: `/api/info.nex` and `PUT /api/config`

`GET /api/info` `nex` object becomes:

```jsonc
"nex": {
  "configured": true,          // Cfg.Nex.Enabled at boot (unchanged meaning)
  "mounted":    true,          // module added (unchanged)
  "ready":      true,          // mounted && initErr == nil
  "init_error": "",            // initErr text, "" when ready
  "effective": {               // what the running engine was assembled with; null when !mounted
    "data_dir": "/…/nex", "claude_bin": "/opt/homebrew/bin/claude",
    "max_profile": "handoff", "default_profile": "standard",
    "repo_roots": [...], "service_roots": [...], "path_prefix": "/a:/b",
    "lease_ttl": "2m0s", "interrupt": "…", "turn": "…"
  }
}
```

`effective` is produced by the nex module (`Status()`), read by
`info_handler.go` through a small `core`-level interface
(`type StatusReporter interface{ Status() map[string]any }`) so `core` does
not import `module/nex`. Values are the **expanded** ones (`~` resolved,
`claude_bin` after lazy `LookPath` if it has already run, else the configured
string or `""`).

`PUT /api/config` accepts `"nex": NexConfig` (full object, not partial —
the form always submits the whole section it displayed):

- `req.Nex.Validate(home)` with `home = os.UserHomeDir()`; error → `400`
  with the validator's message (already names the key).
- On success `cfg.Nex = *req.Nex` inside `UpdateConfig` (persisted to
  `config.toml` like the other sections). **Nothing is applied live.** The
  response is the redacted config, as today.
- `GET /api/config` already returns `nex` (`Config.Nex` has a `json:"nex"`
  tag and `Redacted()` deep-clones its slices — verified).

The SPA computes `restartRequired = !deepEqual(config.nex, normalized(info.nex.effective))`
only for the fields present in both (roots, bins, profiles, timeouts, path
prepend); `enabled` vs `mounted` is compared directly.

#### 4.4.3 SPA: `NexHostSection`

Registered as a built-in host sub-page `{ localId: 'nex', labelKey: 'hosts.nex', order: 6 }`.
Three stacked cards; each card is its own component with its own tests:

1. **`NexEngineStatus`** — from `/api/info.nex` + `GET /v1/host` +
   `GET /v1/capabilities`: badge (Disabled / Enabled, not running /
   Unavailable: `init_error` / Ready), phase & host_id, active account +
   quota bars (5 h / 7 d, `null` rendered as "unknown" — never 0), roots
   list, profiles (max/default), lease TTL, providers. Refresh button +
   refetch on host reconnect. When `ready === false` the two Nexen calls
   are skipped (they would 503).
2. **`NexConfigForm`** — fields: `enabled` (toggle), `repo_roots[]`,
   `service_roots[]`, `path_prepend[]` (list editors with add/remove, `~`
   allowed), `claude_bin`, `cswap_bin`, `sandbox.max_profile`,
   `sandbox.default_profile` (selects from `readonly|standard|trusted|handoff`
   plus empty = Nexen default; the list comes from a constant mirrored from
   `sandbox.ValidName` because capabilities only lists *clamped* profiles),
   `timeouts.{lease_ttl,interrupt,turn}` (duration strings). Draft/commit
   pattern as `EditorHomePathHostSection` (no clobber while focused). Save →
   `PUT /api/config {nex}`; 400 message shown inline next to the offending
   field when the message starts with `nex.<key>`. After a successful save
   the card shows the persistent notice "Saved. Restart the daemon on
   <host> for changes to take effect (`pdx stop && pdx start`)" until
   `info.nex.effective` matches.
3. **`NexExecutionsTable`** — `listExecutions` (default: `include_archived`
   off, toggle to show), columns: state dot, id (short), provider/profile,
   cwd (basename with full path tooltip), brief (first line, truncated),
   observers, lease holder (`(you)` decoration as 4.3.3), last_turn_reason,
   updated_at relative. Row actions: **Open** (opens/focuses an
   `{kind:'execution'}` pane in the current tab via the same helper the
   deeplink resolver uses), **Terminate** (confirm), **Archive/Unarchive**.
   Live refresh: one site-wide SSE (`/api/nex/v1/events`, no
   `execution_id`) per mounted table, debounced 500 ms → refetch list; the
   table never applies frame contents (site-wide strips text anyway). Closed
   on unmount. Manual refresh button as fallback.

i18n: all new strings in `en.json` + `zh-TW.json` (`locale-completeness.test`
enforces parity).

### 4.5 Error and edge handling (cross-cutting)

| Situation | Behaviour |
|---|---|
| Host has no nex (`info.nex.mounted === false`) | Execution pane shows "Nex is not enabled on <host>" with a link to the Host → Nex page; Nex page shows the config form with the status badge "Disabled". |
| `503 nex_unavailable` (init failed) | Same as above with `init_error` text. SSE does not retry on this code (it is not `draining`). |
| Daemon restart mid-turn | SSE reconnects with `Last-Event-ID`; replay fills the gap; summary refetched on `open` after a reconnect. Lease is gone after restart (in-memory) — `renew` gets `lease_expired`, local lease dropped, next send re-acquires. |
| Two tabs, same execution | Both observe. First to send holds the lease; the second gets `lease_held` with the first's principal shown. When the first tab closes (release) or idles past `2×ttl`, the second's next send succeeds. |
| Execution archived while pane open | `execution.archived` event patches summary → input disabled with "archived" placeholder; Unarchive available from the Nex page. |
| History larger than one page | Paged in order; render waits for `historyLoaded` with a spinner and the summary header visible. |
| Malformed SSE data (non-JSON) | Frame dropped, `console.warn` once per connection, cursor **not** advanced past it (the frame's `id` is only committed after a successful parse). |
| `X-Pdx-Client` rejected/ignored by an older daemon | Principal falls back to bare host; everything still works, only cross-tab lease arbitration degrades. No client-side detection needed. |

## 5. Invariants (each has a test)

- I1 `nex-sse` resolves `stream_url` against the daemon origin only; a
  `stream_url` of `/api/nex/v1/events?execution_id=x` never becomes
  `/api/nex/api/nex/…`.
- I2 Every request issued by `nex-api`/`nex-sse` carries `Authorization` from
  `getAuthHeaders` and `X-Pdx-Client` matching `^[A-Za-z0-9._-]{1,64}$`;
  none carries `?ticket=`.
- I3 A pane that has not sent for `2×ttl` stops renewing; a pane that sent
  within that window keeps its lease alive across at least three renew
  cycles (fake timers).
- I4 `applyDurableEvent` is idempotent by seq and drops `id: null` frames
  without changing `lastSeq`.
- I5 Reconnect after a stream error sends `Last-Event-ID` equal to the
  store's `lastSeq` at reconnect time, including a value advanced by history
  paging after the first connect.
- I6 Unmounting `ExecutionView` (or switching its executionId) closes the
  SSE and issues exactly one `DELETE …/attach` when a lease is held, none
  otherwise.
- I7 `ConversationView` (Stream mode) renders byte-identical DOM for the
  existing fixture set before and after the `ConversationMessages`
  extraction (snapshot test carried across the refactor commit).
- I8 nex `Init` failure leaves the daemon serving `/api/health` and answers
  `/api/nex/v1/capabilities` with `503 nex_unavailable`; `/api/info.nex.ready === false` and `init_error` non-empty.
- I9 `PUT /api/config` with an invalid `nex` (e.g. enabled with no roots,
  relative `claude_bin`, bad duration) returns 400 naming the key and leaves
  `config.toml` untouched; with a valid `nex` it persists and the next `GET`
  returns it while `/api/info.nex.effective` is unchanged.
- I10 `NexExecutionsTable` opens `{kind:'execution', executionId, host}` in
  the current tab and focuses an existing pane for the same
  `(host, executionId)` instead of opening a second one.

## 6. Acceptance (live, mlab)

Pre-req: mlab daemon rebuilt from this branch, `[nex] enabled = true`,
`repo_roots = ["~/Workspace"]`, `sandbox.max_profile = "handoff"`, restarted.

1. Host → Nex shows Ready, account + quota, roots, profiles; config form
   mirrors `config.toml`.
2. Change `default_profile` in the form → save → notice "restart required";
   `config.toml` updated; `effective.default_profile` unchanged until restart.
3. `pdx nex delegate --profile standard --cwd ~/Workspace/wake/purdex "list the top-level dirs"` →
   row appears in the table without manual refresh (site-wide SSE).
4. Open → pane shows history, header state `idle`, SSE open.
5. Send "and count the Go files" → optimistic bubble → assistant messages
   stream in (as whole messages, no typewriter) → `result` re-enables input;
   cost updates.
6. Second browser tab (or Electron window) opens the same execution → send
   → "Held by pdx:mlab/t-…" notice. Close the first tab → wait ≤ 5 s → second
   tab's send succeeds.
7. `pdx stop && pdx start` while the pane is open → SSE goes reconnecting →
   open; send works (execution came back `idle`).
8. Break `[nex]` deliberately (`claude_bin = "/nonexistent"` is *valid* shape
   but Assemble should still succeed lazily — instead point `repo_roots` at
   a file, or make `data_dir` unwritable) → restart → daemon up, Nex page
   shows "Unavailable: …", terminal panes unaffected.
9. Air (a26) after dev update: Host → Nex on the mlab host works over
   Tailscale; the a26 host itself shows "Disabled" until configured.

Each turn in step 5 costs real Claude usage (~$0.3); keep the brief small.

## 7. Risks

- **Forward-only history paging** makes the first open O(events). Bounded
  by turn count for P-B; P-B2 revisits with a "tail" window when Nexen
  offers `before=` or when it hurts.
- **Lease idle policy** trades desktop convenience for takeover-ability: a
  user who reads for 5 minutes then types pays one extra round-trip
  (`attachControl`). Acceptable; the alternative (hold forever) blocks
  scenario 2 of the kickoff.
- **`beforeunload` release is best-effort**; a killed tab leaves the lease to
  expire naturally (≤ 120 s). Documented in the UI notice text.
- **Soft-fail Init widens the daemon's config surface**: a malformed `[nex]`
  from the API is caught by `Validate`; a semantically wrong one (roots that
  exist but are wrong) simply yields rejected delegates, which the table
  shows. No new fatal path.
- **`ConversationView` extraction** is a refactor of a 352-line component
  with a large test file; I7's snapshot test is the guard, and the
  extraction is its own commit before any behaviour change.

## 8. Open questions

None blocking. Decisions taken in the 2026-09-15 design discussion (do not
reopen): Nex page lives under Host (per-host daemon config convention), no
hot apply, no restart button in P-B, transient frames dropped until P-B2.

## 9. Related

- P-A spec/plan/acceptance: `2026-09-15-pa-nex-module-{spec,plan,acceptance}.md`
- Kickoff memory: `kickoff_nexen_into_purdex.md`; Nexen contract docs
  (`consumer-guide.md` §0.5/§4/§9, `capability-matrix.md` §0/§3/§3.5/錯誤碼).
- Follow-ups to open at ship time: hot-apply `[nex]`; daemon restart
  endpoint + button; history tail window; #1033 (Stop closes store).
