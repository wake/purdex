# P-B.1 — Nexen client + execution store Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the SPA a tested, UI-free client for the Nexen contract mounted at `/api/nex` — per-tab client id, typed REST wrappers, a fetch/ReadableStream SSE transport with `Last-Event-ID` resume, a pure event reducer, and a zustand store keyed by `(hostId, executionId)` — plus the host-removal store cleanup.

**Architecture:** Everything lives under `spa/src/lib/nex/` (one file per responsibility) and `spa/src/stores/useExecutionStore.ts`. No React components, no hooks; P-B.2 builds the pane on these. All network goes through the existing `hostFetch` / `useHostStore.getAuthHeaders` so auth stays in one place; the SSE layer owns reconnection but not the cursor (the store does).

**Tech Stack:** TypeScript 5 / React 19 (types only) / Zustand 5 / Vitest (jsdom env; SSE transport tests use `// @vitest-environment node` for a real `ReadableStream`).

**Spec:** `docs/specs/2026-09-15-pb-execution-pane-spec.md` §4.2 (P-B.1), §4.3.4 (clearHost ordering), §5 I1, I2, I4, I5, I11 (reducer half), I12 (reducer half).

## Global Constraints

- Every `/api/nex/…` request carries `Authorization` from `useHostStore.getState().getAuthHeaders(hostId)` and `X-Pdx-Client` matching `^[A-Za-z0-9._-]{1,64}$`; **never** `?ticket=` (spec §4.3 of P-A, §5 I2).
- `stream_url` from attach(observe) is origin-relative and already contains `/api/nex`; resolve against the daemon origin only (I1).
- Transient SSE frames (no `id:` line) never advance the cursor (I4).
- Local lease state is written only from `attach(control)` / `renew` responses (I11).
- Files: one responsibility each; no file over ~300 lines; tests next to the file as `<name>.test.ts`.
- Language: code + comments + commit messages in English; keep the surrounding comment density (see `stream-ws.ts`, `useStreamStore.ts`).
- Commands run from the worktree: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run <file>`; lint `pnpm run lint`; build `pnpm run build`.
- One commit per task, `git commit --only <files>` (other sessions share the index).

---

## File map

| File | Responsibility |
|---|---|
| `spa/src/lib/nex/client-id.ts` | per-tab client id (`sessionStorage`) |
| `spa/src/lib/nex/types.ts` | wire types: `NexCapabilities`, `NexHostInfo`, `ExecutionSummary`, `NexEvent`, attach/send/interrupt responses, `NexApiError` |
| `spa/src/lib/nex/nex-api.ts` | `nexFetch` + typed REST wrappers |
| `spa/src/lib/nex/sse-parser.ts` | incremental SSE line parser (pure) |
| `spa/src/lib/nex/nex-sse.ts` | `openNexSse` transport with reconnect/backoff |
| `spa/src/lib/nex/event-reducer.ts` | `ExecutionState`, `defaultExecutionState`, `applyDurableEvent` (pure) |
| `spa/src/stores/useExecutionStore.ts` | zustand store keyed by composite key |
| `spa/src/lib/host-api.ts` | + `hostAuthHeaders(hostId)` export |
| `spa/src/lib/host-lifecycle.ts` | + `useExecutionStore.clearHost` in the cascade |

---

### Task 1: Per-tab client id

**Files:**
- Create: `spa/src/lib/nex/client-id.ts`
- Test: `spa/src/lib/nex/client-id.test.ts`

**Interfaces:**
- Produces: `getNexClientId(): string`, `NEX_CLIENT_ID_RE: RegExp`, `resetNexClientIdForTests(): void`

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/lib/nex/client-id.test.ts
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { getNexClientId, NEX_CLIENT_ID_RE, resetNexClientIdForTests } from './client-id'

describe('getNexClientId', () => {
  beforeEach(() => {
    sessionStorage.clear()
    resetNexClientIdForTests()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('returns a value matching the daemon pattern and stores it in sessionStorage', () => {
    const id = getNexClientId()
    expect(id).toMatch(NEX_CLIENT_ID_RE)
    expect(id).toMatch(/^t-[0-9a-z]{8}$/)
    expect(sessionStorage.getItem('purdex-nex-client-id')).toBe(id)
  })

  it('is stable across calls and reuses a stored value', () => {
    sessionStorage.setItem('purdex-nex-client-id', 't-abcdefgh')
    expect(getNexClientId()).toBe('t-abcdefgh')
    expect(getNexClientId()).toBe('t-abcdefgh')
  })

  it('ignores a stored value that does not match the pattern', () => {
    sessionStorage.setItem('purdex-nex-client-id', 'bad value!')
    const id = getNexClientId()
    expect(id).not.toBe('bad value!')
    expect(id).toMatch(NEX_CLIENT_ID_RE)
  })

  it('falls back to an in-memory id when sessionStorage throws', () => {
    vi.stubGlobal('sessionStorage', {
      getItem: () => { throw new Error('denied') },
      setItem: () => { throw new Error('denied') },
    })
    const a = getNexClientId()
    const b = getNexClientId()
    expect(a).toBe(b)
    expect(a).toMatch(NEX_CLIENT_ID_RE)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/nex/client-id.test.ts`
Expected: FAIL — cannot resolve `./client-id`.

- [ ] **Step 3: Write minimal implementation**

```ts
// spa/src/lib/nex/client-id.ts — the per-tab principal suffix sent as
// X-Pdx-Client on every /api/nex request (P-A spec §4.3). One id per browser
// tab / Electron window so the daemon can arbitrate control leases between
// them; deliberately NOT the per-device sync clientId, which would collapse
// every tab into one principal.
const STORAGE_KEY = 'purdex-nex-client-id'

/** Mirrors the daemon's clientIDPattern (internal/module/nex). */
export const NEX_CLIENT_ID_RE = /^[A-Za-z0-9._-]{1,64}$/

let memoryId: string | null = null

function generate(): string {
  const bytes = new Uint8Array(6)
  crypto.getRandomValues(bytes)
  let n = 0
  for (const b of bytes) n = n * 256 + b
  return `t-${n.toString(36).padStart(8, '0').slice(-8)}`
}

/** Stable for the life of this tab; survives reload via sessionStorage. */
export function getNexClientId(): string {
  if (memoryId) return memoryId
  try {
    const stored = sessionStorage.getItem(STORAGE_KEY)
    if (stored && NEX_CLIENT_ID_RE.test(stored)) {
      memoryId = stored
      return stored
    }
    const fresh = generate()
    sessionStorage.setItem(STORAGE_KEY, fresh)
    memoryId = fresh
    return fresh
  } catch {
    // sessionStorage unavailable (privacy mode, sandboxed frame): same
    // semantics for this page load, just not surviving a reload.
    memoryId = generate()
    return memoryId
  }
}

/** Test hook: forget the memoised id so the next call re-reads storage. */
export function resetNexClientIdForTests(): void {
  memoryId = null
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/nex/client-id.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/lib/nex/client-id.ts spa/src/lib/nex/client-id.test.ts && git commit --only spa/src/lib/nex/client-id.ts spa/src/lib/nex/client-id.test.ts -m "feat(spa): per-tab nex client id for X-Pdx-Client"
```

---

### Task 2: Wire types + `NexApiError`

**Files:**
- Create: `spa/src/lib/nex/types.ts`
- Test: `spa/src/lib/nex/types.test.ts`

**Interfaces:**
- Produces (all exported from `types.ts`):

```ts
export type ExecutionStateName = 'queued' | 'running' | 'idle' | 'rejected' | 'failed' | 'terminated'

export interface ExecutionLeaseView { principal_id: string; expires_at: number }

export interface ExecutionSummary {
  id: string; state: ExecutionStateName | string; provider: string; principal_id: string
  cwd: string; mount_kind: string; account_id?: string; brief: string
  origin?: string; labels: Record<string, string>
  requested_profile?: string; effective_profile?: string
  reject_reason?: string; terminal_reason?: string; last_turn_reason?: string
  session_id?: string; transcript_path?: string; pid?: number
  created_at: number; updated_at: number; duration_ms: number | null
  event_count: number; observers: number; archived: boolean
  turn_count?: number; live_turn_id?: string; lease?: ExecutionLeaseView
}

export interface NexEvent { seq: number; execution_id: string; kind: string; payload: Record<string, unknown>; created_at: number }

export interface ExecutionsPage { items: ExecutionSummary[]; next_cursor: string }
export interface EventsPage { items: NexEvent[]; next_cursor: number }

export interface NexCapabilities {
  phase: string; host_id: string; verbs: string[]; providers: string[]
  events: string[]; provider_events: string[]; transient_events: string[]
  sandbox_profiles: string[]; sandbox_default_profile: string; sandbox_max_profile: string
  roots: Array<{ path: string; kind: 'dev' | 'service' | string }>
  lease: { ttl_seconds: number; scope: string; renew: { method: string; path: string }; release: { method: string; path: string } }
  send: { delivery: string[]; max_text_bytes: number }
  [key: string]: unknown
}

export interface NexQuota { five_hour_pct: number; seven_day_pct: number; resets_at: number; source: 'cswap' | 'provider_event' | string }
export interface NexHostInfo { active_account: string; quota: NexQuota | null }

export interface AttachObserveResponse { mode: 'observe'; stream_url: string; cursor: number; state: string }
export interface AttachControlResponse { mode: 'control'; lease_id: string; expires_at: number }
export interface SendResponse { turn_id: string; delivery: 'delivered' | 'queued' }
export interface InterruptResponse { turn_id: string; state: string; reason?: string }

export class NexApiError extends Error {
  readonly status: number
  readonly code: string
  readonly turnId?: string
  constructor(status: number, code: string, message: string, turnId?: string)
}

/** Build a NexApiError from a non-2xx Response, tolerating non-JSON bodies. */
export async function nexErrorFromResponse(res: Response): Promise<NexApiError>
```

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/lib/nex/types.test.ts
import { describe, it, expect } from 'vitest'
import { NexApiError, nexErrorFromResponse } from './types'

describe('nexErrorFromResponse', () => {
  it('parses the structured {error, code} body', async () => {
    const res = new Response(JSON.stringify({ error: 'somebody holds it', code: 'lease_held' }), { status: 409 })
    const err = await nexErrorFromResponse(res)
    expect(err).toBeInstanceOf(NexApiError)
    expect(err.status).toBe(409)
    expect(err.code).toBe('lease_held')
    expect(err.message).toBe('somebody holds it')
    expect(err.turnId).toBeUndefined()
  })

  it('carries turn_id when the body has one', async () => {
    const res = new Response(JSON.stringify({ error: 'x', code: 'turn_stalled', turn_id: 'trn_1' }), { status: 409 })
    const err = await nexErrorFromResponse(res)
    expect(err.turnId).toBe('trn_1')
  })

  it('falls back to http_<status> for a non-JSON body', async () => {
    const res = new Response('<html>nope</html>', { status: 502 })
    const err = await nexErrorFromResponse(res)
    expect(err.code).toBe('http_502')
    expect(err.message).toContain('502')
  })

  it('falls back to http_<status> for JSON without a code', async () => {
    const res = new Response(JSON.stringify({ message: 'legacy' }), { status: 400 })
    const err = await nexErrorFromResponse(res)
    expect(err.code).toBe('http_400')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/nex/types.test.ts`
Expected: FAIL — cannot resolve `./types`.

- [ ] **Step 3: Write minimal implementation**

```ts
// spa/src/lib/nex/types.ts — wire shapes of the Nexen contract as mounted at
// /api/nex (nexen/docs/contract/capability-matrix.md; api/handlers.go
// executionView / eventView, api/interact.go *Wire). Only the fields the SPA
// reads are typed; index signatures keep unknown additions harmless.

export type ExecutionStateName = 'queued' | 'running' | 'idle' | 'rejected' | 'failed' | 'terminated'

export interface ExecutionLeaseView {
  principal_id: string
  expires_at: number
}

export interface ExecutionSummary {
  id: string
  state: ExecutionStateName | string
  provider: string
  principal_id: string
  cwd: string
  mount_kind: string
  account_id?: string
  brief: string
  origin?: string
  labels: Record<string, string>
  requested_profile?: string
  effective_profile?: string
  reject_reason?: string
  terminal_reason?: string
  last_turn_reason?: string
  session_id?: string
  transcript_path?: string
  pid?: number
  created_at: number
  updated_at: number
  duration_ms: number | null
  event_count: number
  observers: number
  archived: boolean
  // Single-get only (GET /v1/executions/{id}); absent on list rows.
  turn_count?: number
  live_turn_id?: string
  lease?: ExecutionLeaseView
}

export interface NexEvent {
  seq: number
  execution_id: string
  kind: string
  payload: Record<string, unknown>
  created_at: number
}

export interface ExecutionsPage {
  items: ExecutionSummary[]
  /** Opaque; "" means this was the last page. */
  next_cursor: string
}

export interface EventsPage {
  items: NexEvent[]
  /** Seq to pass back as `after`; 0 means this was the last page. */
  next_cursor: number
}

export interface NexCapabilities {
  phase: string
  host_id: string
  verbs: string[]
  providers: string[]
  events: string[]
  provider_events: string[]
  transient_events: string[]
  sandbox_profiles: string[]
  sandbox_default_profile: string
  sandbox_max_profile: string
  roots: Array<{ path: string; kind: 'dev' | 'service' | string }>
  lease: {
    ttl_seconds: number
    scope: string
    renew: { method: string; path: string }
    release: { method: string; path: string }
  }
  send: { delivery: string[]; max_text_bytes: number }
  [key: string]: unknown
}

export interface NexQuota {
  five_hour_pct: number
  seven_day_pct: number
  resets_at: number
  source: 'cswap' | 'provider_event' | string
}

export interface NexHostInfo {
  active_account: string
  /** null whenever the daemon cannot say whose quota it would be. */
  quota: NexQuota | null
}

export interface AttachObserveResponse {
  mode: 'observe'
  /** Origin-relative absolute path, already prefixed with /api/nex. */
  stream_url: string
  /** The execution's latest durable seq at attach time. */
  cursor: number
  state: string
}

export interface AttachControlResponse {
  mode: 'control'
  lease_id: string
  expires_at: number
}

export interface SendResponse {
  turn_id: string
  delivery: 'delivered' | 'queued'
}

export interface InterruptResponse {
  turn_id: string
  state: string
  reason?: string
}

/**
 * Every non-2xx from /api/nex. `code` is the contract's structured error
 * code (capability-matrix "錯誤碼"), or `http_<status>` when the body is not
 * the {error, code} shape (a proxy page, an older daemon, the pdx 503
 * nex_unavailable fallback still parses because it uses the same shape).
 */
export class NexApiError extends Error {
  readonly status: number
  readonly code: string
  readonly turnId?: string

  constructor(status: number, code: string, message: string, turnId?: string) {
    super(message)
    this.name = 'NexApiError'
    this.status = status
    this.code = code
    this.turnId = turnId
  }
}

export async function nexErrorFromResponse(res: Response): Promise<NexApiError> {
  const fallback = `http_${res.status}`
  let text = ''
  try {
    text = await res.text()
  } catch {
    return new NexApiError(res.status, fallback, `nex: HTTP ${res.status}`)
  }
  try {
    const body = JSON.parse(text) as { error?: unknown; code?: unknown; turn_id?: unknown }
    if (typeof body.code === 'string' && body.code !== '') {
      const message = typeof body.error === 'string' && body.error !== '' ? body.error : `nex: HTTP ${res.status}`
      const turnId = typeof body.turn_id === 'string' && body.turn_id !== '' ? body.turn_id : undefined
      return new NexApiError(res.status, body.code, message, turnId)
    }
  } catch {
    // not JSON — fall through
  }
  return new NexApiError(res.status, fallback, `nex: HTTP ${res.status}${text ? `: ${text.slice(0, 200)}` : ''}`)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/nex/types.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/lib/nex/types.ts spa/src/lib/nex/types.test.ts && git commit --only spa/src/lib/nex/types.ts spa/src/lib/nex/types.test.ts -m "feat(spa): nex wire types and NexApiError"
```

---

### Task 3: `hostAuthHeaders` + `nexFetch` + REST wrappers

**Files:**
- Modify: `spa/src/lib/host-api.ts` (add one export next to `hostFetch`, ~line 36)
- Create: `spa/src/lib/nex/nex-api.ts`
- Test: `spa/src/lib/nex/nex-api.test.ts`

**Interfaces:**
- Consumes: `getNexClientId` (Task 1), types + `nexErrorFromResponse` (Task 2), `hostFetch` (existing).
- Produces:

```ts
// host-api.ts
export function hostAuthHeaders(hostId: string): Record<string, string>   // = useHostStore.getState().getAuthHeaders(hostId)

// nex-api.ts
export function nexFetch(hostId: string, path: string, init?: RequestInit): Promise<Response>
export function fetchNexCapabilities(hostId: string): Promise<NexCapabilities>
export function fetchNexHost(hostId: string): Promise<NexHostInfo>
export interface ListExecutionsOptions { state?: string; includeArchived?: boolean; cursor?: string; limit?: number }
export function listExecutions(hostId: string, opts?: ListExecutionsOptions): Promise<ExecutionsPage>
export function getExecution(hostId: string, executionId: string): Promise<ExecutionSummary>
export function fetchExecutionEvents(hostId: string, executionId: string, opts: { after: number; limit?: number }): Promise<EventsPage>
export function attachObserve(hostId: string, executionId: string): Promise<AttachObserveResponse>
export function attachControl(hostId: string, executionId: string): Promise<AttachControlResponse>
export function renewLease(hostId: string, executionId: string, leaseId: string): Promise<AttachControlResponse>
export function releaseLease(hostId: string, executionId: string, leaseId: string): Promise<void>
export function sendMessage(hostId: string, executionId: string, leaseId: string, text: string): Promise<SendResponse>
export function interruptExecution(hostId: string, executionId: string, leaseId: string): Promise<InterruptResponse>
export function archiveExecution(hostId: string, executionId: string, undo?: boolean): Promise<void>
export function terminateExecution(hostId: string, executionId: string, leaseId: string): Promise<void>
export function resolveExecutionHostId(host?: string): string   // moved here from execution-api.ts (same body)
```

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/lib/nex/nex-api.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import {
  nexFetch, fetchNexCapabilities, listExecutions, fetchExecutionEvents,
  attachObserve, attachControl, sendMessage, releaseLease, resolveExecutionHostId,
} from './nex-api'
import { NexApiError } from './types'
import { NEX_CLIENT_ID_RE } from './client-id'

const testGlobal = globalThis as typeof globalThis & { fetch: ReturnType<typeof vi.fn> }

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('nex-api', () => {
  beforeEach(() => {
    useHostStore.getState().reset()
    useHostStore.getState().addHost({ name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok-1' } as never)
    vi.stubGlobal('fetch', vi.fn())
  })
  afterEach(() => vi.unstubAllGlobals())

  function hostId(): string {
    return useHostStore.getState().hostOrder.find((id) => useHostStore.getState().hosts[id].name === 'mlab')!
  }

  it('nexFetch prefixes /api/nex, sends Bearer and X-Pdx-Client, never a ticket', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({}))
    await nexFetch(hostId(), '/v1/capabilities')
    const [url, init] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/capabilities')
    expect(url).not.toContain('ticket')
    const h = new Headers(init.headers)
    expect(h.get('Authorization')).toBe('Bearer tok-1')
    expect(h.get('X-Pdx-Client')).toMatch(NEX_CLIENT_ID_RE)
  })

  it('fetchNexCapabilities returns the parsed body', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ phase: 'P1a', host_id: 'mlab', verbs: ['attach'], lease: { ttl_seconds: 120 } }))
    const caps = await fetchNexCapabilities(hostId())
    expect(caps.phase).toBe('P1a')
    expect(caps.lease.ttl_seconds).toBe(120)
  })

  it('listExecutions builds the query string', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ items: [], next_cursor: '' }))
    await listExecutions(hostId(), { state: 'running', includeArchived: true, cursor: 'c1', limit: 20 })
    const [url] = testGlobal.fetch.mock.calls[0]
    const u = new URL(url)
    expect(u.pathname).toBe('/api/nex/v1/executions')
    expect(u.searchParams.get('state')).toBe('running')
    expect(u.searchParams.get('include_archived')).toBe('true')
    expect(u.searchParams.get('cursor')).toBe('c1')
    expect(u.searchParams.get('limit')).toBe('20')
  })

  it('fetchExecutionEvents passes after/limit and encodes the id', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ items: [], next_cursor: 0 }))
    await fetchExecutionEvents(hostId(), 'exc a', { after: 41, limit: 500 })
    const [url] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc%20a/events?after=41&limit=500')
  })

  it('attachObserve / attachControl post the mode as JSON', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ mode: 'observe', stream_url: '/api/nex/v1/events?execution_id=exc_1', cursor: 7, state: 'idle' }))
    const obs = await attachObserve(hostId(), 'exc_1')
    expect(obs.cursor).toBe(7)
    let [, init] = testGlobal.fetch.mock.calls[0]
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual({ mode: 'observe' })
    expect(new Headers(init.headers).get('Content-Type')).toBe('application/json')

    testGlobal.fetch.mockResolvedValueOnce(json({ mode: 'control', lease_id: 'ls_1', expires_at: 1 }))
    const ctl = await attachControl(hostId(), 'exc_1')
    expect(ctl.lease_id).toBe('ls_1')
    ;[, init] = testGlobal.fetch.mock.calls[1]
    expect(JSON.parse(init.body)).toEqual({ mode: 'control' })
  })

  it('sendMessage posts lease_id + text; releaseLease sends DELETE with lease_id', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ turn_id: 'trn_1', delivery: 'queued' }))
    const r = await sendMessage(hostId(), 'exc_1', 'ls_1', 'hello')
    expect(r.delivery).toBe('queued')
    let [url, init] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1/messages')
    expect(JSON.parse(init.body)).toEqual({ lease_id: 'ls_1', text: 'hello' })

    testGlobal.fetch.mockResolvedValueOnce(new Response(null, { status: 204 }))
    await releaseLease(hostId(), 'exc_1', 'ls_1')
    ;[url, init] = testGlobal.fetch.mock.calls[1]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1/attach')
    expect(init.method).toBe('DELETE')
    expect(JSON.parse(init.body)).toEqual({ lease_id: 'ls_1' })
  })

  it('throws NexApiError with the structured code on non-2xx', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ error: 'held', code: 'lease_held' }, 409))
    await expect(attachControl(hostId(), 'exc_1')).rejects.toMatchObject({ code: 'lease_held', status: 409 })
    testGlobal.fetch.mockResolvedValueOnce(json({ error: 'gone', code: 'execution_not_found' }, 404))
    const err = await attachObserve(hostId(), 'nope').catch((e) => e)
    expect(err).toBeInstanceOf(NexApiError)
    expect(err.code).toBe('execution_not_found')
  })

  it('resolveExecutionHostId prefers a known host and falls back to the first', () => {
    const id = hostId()
    expect(resolveExecutionHostId(id)).toBe(id)
    expect(resolveExecutionHostId('unknown')).toBe(useHostStore.getState().hostOrder[0])
    expect(resolveExecutionHostId(undefined)).toBe(useHostStore.getState().hostOrder[0])
  })
})
```

> `addHost` takes `{ id?, name, ip, port, token? }` and returns the id (`useHostStore.ts:54`) — use the return value instead of searching `hostOrder`: `hostId = useHostStore.getState().addHost({ name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok-1' })`. Same in Task 5.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/nex/nex-api.test.ts`
Expected: FAIL — cannot resolve `./nex-api`.

- [ ] **Step 3: Write minimal implementation**

In `spa/src/lib/host-api.ts`, directly after `hostFetch`:

```ts
/**
 * The auth headers `hostFetch` attaches, exported for transports that cannot
 * go through `hostFetch` (the nex SSE reader builds its own fetch so it can
 * stream the body). One source of truth for "how do we authenticate to host X".
 */
export function hostAuthHeaders(hostId: string): Record<string, string> {
  return useHostStore.getState().getAuthHeaders(hostId)
}
```

Create `spa/src/lib/nex/nex-api.ts`:

```ts
// spa/src/lib/nex/nex-api.ts — typed REST client for the Nexen contract
// mounted at /api/nex on each pdx daemon (P-A spec §4.3). Every call goes
// through hostFetch (Bearer from the host store) plus the per-tab
// X-Pdx-Client header; tickets are never involved on this path.
import { hostFetch } from '../host-api'
import { useHostStore } from '../../stores/useHostStore'
import { getNexClientId } from './client-id'
import {
  nexErrorFromResponse,
  type AttachControlResponse,
  type AttachObserveResponse,
  type EventsPage,
  type ExecutionSummary,
  type ExecutionsPage,
  type InterruptResponse,
  type NexCapabilities,
  type NexHostInfo,
  type SendResponse,
} from './types'

const PREFIX = '/api/nex'

export function nexFetch(hostId: string, path: string, init?: RequestInit): Promise<Response> {
  const headers = new Headers(init?.headers)
  headers.set('X-Pdx-Client', getNexClientId())
  if (init?.body != null && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  return hostFetch(hostId, `${PREFIX}${path}`, { ...init, headers })
}

async function okJson<T>(res: Response): Promise<T> {
  if (!res.ok) throw await nexErrorFromResponse(res)
  return (await res.json()) as T
}

async function okVoid(res: Response): Promise<void> {
  if (!res.ok) throw await nexErrorFromResponse(res)
}

function postJson(hostId: string, path: string, body: unknown, method = 'POST'): Promise<Response> {
  return nexFetch(hostId, path, { method, body: JSON.stringify(body) })
}

function execPath(executionId: string, suffix = ''): string {
  return `/v1/executions/${encodeURIComponent(executionId)}${suffix}`
}

export function fetchNexCapabilities(hostId: string): Promise<NexCapabilities> {
  return nexFetch(hostId, '/v1/capabilities').then((r) => okJson<NexCapabilities>(r))
}

export function fetchNexHost(hostId: string): Promise<NexHostInfo> {
  return nexFetch(hostId, '/v1/host').then((r) => okJson<NexHostInfo>(r))
}

export interface ListExecutionsOptions {
  state?: string
  includeArchived?: boolean
  cursor?: string
  limit?: number
}

export function listExecutions(hostId: string, opts: ListExecutionsOptions = {}): Promise<ExecutionsPage> {
  const q = new URLSearchParams()
  if (opts.state) q.set('state', opts.state)
  if (opts.includeArchived) q.set('include_archived', 'true')
  if (opts.cursor) q.set('cursor', opts.cursor)
  if (opts.limit) q.set('limit', String(opts.limit))
  const qs = q.toString()
  return nexFetch(hostId, `/v1/executions${qs ? `?${qs}` : ''}`).then((r) => okJson<ExecutionsPage>(r))
}

export function getExecution(hostId: string, executionId: string): Promise<ExecutionSummary> {
  return nexFetch(hostId, execPath(executionId)).then((r) => okJson<ExecutionSummary>(r))
}

export function fetchExecutionEvents(
  hostId: string,
  executionId: string,
  opts: { after: number; limit?: number },
): Promise<EventsPage> {
  const q = new URLSearchParams({ after: String(opts.after) })
  if (opts.limit) q.set('limit', String(opts.limit))
  return nexFetch(hostId, `${execPath(executionId, '/events')}?${q.toString()}`).then((r) => okJson<EventsPage>(r))
}

export function attachObserve(hostId: string, executionId: string): Promise<AttachObserveResponse> {
  return postJson(hostId, execPath(executionId, '/attach'), { mode: 'observe' }).then((r) => okJson<AttachObserveResponse>(r))
}

export function attachControl(hostId: string, executionId: string): Promise<AttachControlResponse> {
  return postJson(hostId, execPath(executionId, '/attach'), { mode: 'control' }).then((r) => okJson<AttachControlResponse>(r))
}

export function renewLease(hostId: string, executionId: string, leaseId: string): Promise<AttachControlResponse> {
  return postJson(hostId, execPath(executionId, '/attach/renew'), { lease_id: leaseId }).then((r) => okJson<AttachControlResponse>(r))
}

export function releaseLease(hostId: string, executionId: string, leaseId: string): Promise<void> {
  return postJson(hostId, execPath(executionId, '/attach'), { lease_id: leaseId }, 'DELETE').then(okVoid)
}

export function sendMessage(hostId: string, executionId: string, leaseId: string, text: string): Promise<SendResponse> {
  return postJson(hostId, execPath(executionId, '/messages'), { lease_id: leaseId, text }).then((r) => okJson<SendResponse>(r))
}

export function interruptExecution(hostId: string, executionId: string, leaseId: string): Promise<InterruptResponse> {
  return postJson(hostId, execPath(executionId, '/interrupt'), { lease_id: leaseId }).then((r) => okJson<InterruptResponse>(r))
}

export function archiveExecution(hostId: string, executionId: string, undo = false): Promise<void> {
  // api/interact.go:81 archiveRequestWire — the flag is the target state.
  return postJson(hostId, execPath(executionId, '/archive'), { archived: !undo }).then(okVoid)
}

export function terminateExecution(hostId: string, executionId: string, leaseId: string): Promise<void> {
  return postJson(hostId, execPath(executionId, '/terminate'), { lease_id: leaseId }).then(okVoid)
}

/**
 * Resolve an optional `host` hint (pane content / deeplink) onto a known SPA
 * hostId; falls back to the first host so an execution always has a daemon
 * to talk to. (Moved from the M0 execution-api.ts, which P-B.2 deletes.)
 */
export function resolveExecutionHostId(host?: string): string {
  const { hostOrder } = useHostStore.getState()
  if (host && hostOrder.includes(host)) return host
  return hostOrder[0] ?? ''
}
```

> `archiveRequestWire` is `{ "archived": bool }` (`api/interact.go:81`) — `archiveExecution(hostId, id, undo)` must post `{ archived: !undo }`. Fix the body in the code above accordingly.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/nex/nex-api.test.ts src/lib/host-api.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/lib/host-api.ts spa/src/lib/nex/nex-api.ts spa/src/lib/nex/nex-api.test.ts && git commit --only spa/src/lib/host-api.ts spa/src/lib/nex/nex-api.ts spa/src/lib/nex/nex-api.test.ts -m "feat(spa): typed nex REST client with per-tab principal"
```

---

### Task 4: Incremental SSE parser (pure)

**Files:**
- Create: `spa/src/lib/nex/sse-parser.ts`
- Test: `spa/src/lib/nex/sse-parser.test.ts`

**Interfaces:**
- Produces:

```ts
export interface NexSseFrame { id: string | null; event: string; data: string }
export class SseParser {
  /** Feed a decoded text chunk; returns every frame completed by it. */
  push(chunk: string): NexSseFrame[]
}
```

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/lib/nex/sse-parser.test.ts
import { describe, it, expect } from 'vitest'
import { SseParser } from './sse-parser'

describe('SseParser', () => {
  it('parses a durable frame with id/event/data', () => {
    const p = new SseParser()
    const frames = p.push('id: 42\nevent: assistant\ndata: {"type":"assistant"}\n\n')
    expect(frames).toEqual([{ id: '42', event: 'assistant', data: '{"type":"assistant"}' }])
  })

  it('reports a transient frame (no id line) with id: null', () => {
    const p = new SseParser()
    const frames = p.push('event: stream_event\ndata: {"x":1}\n\n')
    expect(frames).toEqual([{ id: null, event: 'stream_event', data: '{"x":1}' }])
  })

  it('reassembles frames split across chunks and joins multi-line data', () => {
    const p = new SseParser()
    expect(p.push('id: 1\nev')).toEqual([])
    expect(p.push('ent: user\ndata: a\ndata: b\n')).toEqual([])
    expect(p.push('\n')).toEqual([{ id: '1', event: 'user', data: 'a\nb' }])
  })

  it('ignores comment keepalives and tolerates CRLF', () => {
    const p = new SseParser()
    expect(p.push(': keepalive\r\n\r\n')).toEqual([])
    expect(p.push('id: 2\r\nevent: result\r\ndata: {}\r\n\r\n')).toEqual([{ id: '2', event: 'result', data: '{}' }])
  })

  it('does not carry the previous frame id into the next frame', () => {
    const p = new SseParser()
    p.push('id: 5\nevent: a\ndata: 1\n\n')
    expect(p.push('event: b\ndata: 2\n\n')).toEqual([{ id: null, event: 'b', data: '2' }])
  })

  it('defaults event to "message" and skips frames with no data', () => {
    const p = new SseParser()
    expect(p.push('data: hi\n\n')).toEqual([{ id: null, event: 'message', data: 'hi' }])
    expect(p.push('event: nothing\n\n')).toEqual([])
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/nex/sse-parser.test.ts`
Expected: FAIL — cannot resolve `./sse-parser`.

- [ ] **Step 3: Write minimal implementation**

```ts
// spa/src/lib/nex/sse-parser.ts — the subset of text/event-stream Nexen
// emits (api/sse.go): `id:`, `event:`, `data:` (multi-line), `:` comments
// as keepalives, blank line dispatches. A frame with no `id:` line is a
// transient frame (capability-matrix §3.5) and is reported with id null —
// that distinction is the whole reason a native EventSource is not used
// (it would also be unable to send Last-Event-ID as a header).

export interface NexSseFrame {
  id: string | null
  event: string
  data: string
}

export class SseParser {
  private buffer = ''
  private id: string | null = null
  private event = ''
  private data: string[] = []

  push(chunk: string): NexSseFrame[] {
    this.buffer += chunk
    const out: NexSseFrame[] = []
    let nl: number
    while ((nl = this.buffer.indexOf('\n')) >= 0) {
      let line = this.buffer.slice(0, nl)
      this.buffer = this.buffer.slice(nl + 1)
      if (line.endsWith('\r')) line = line.slice(0, -1)
      if (line === '') {
        const frame = this.dispatch()
        if (frame) out.push(frame)
        continue
      }
      if (line.startsWith(':')) continue
      const colon = line.indexOf(':')
      const field = colon >= 0 ? line.slice(0, colon) : line
      let value = colon >= 0 ? line.slice(colon + 1) : ''
      if (value.startsWith(' ')) value = value.slice(1)
      switch (field) {
        case 'id': this.id = value; break
        case 'event': this.event = value; break
        case 'data': this.data.push(value); break
        default: break // retry:, unknown fields — not used
      }
    }
    return out
  }

  private dispatch(): NexSseFrame | null {
    const frame = this.data.length === 0
      ? null
      : { id: this.id, event: this.event || 'message', data: this.data.join('\n') }
    this.id = null
    this.event = ''
    this.data = []
    return frame
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/nex/sse-parser.test.ts`
Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/lib/nex/sse-parser.ts spa/src/lib/nex/sse-parser.test.ts && git commit --only spa/src/lib/nex/sse-parser.ts spa/src/lib/nex/sse-parser.test.ts -m "feat(spa): incremental SSE frame parser for nex streams"
```

---

### Task 5: SSE transport with resume + backoff

**Files:**
- Create: `spa/src/lib/nex/nex-sse.ts`
- Test: `spa/src/lib/nex/nex-sse.test.ts` (first line: `// @vitest-environment node`)

**Interfaces:**
- Consumes: `SseParser`/`NexSseFrame` (Task 4), `hostAuthHeaders` (Task 3), `getNexClientId` (Task 1), `useHostStore.getDaemonBase`.
- Produces:

```ts
export type NexSseStatus = 'connecting' | 'open' | 'reconnecting' | 'closed'
export interface NexSseOptions {
  hostId: string
  url: string                                   // origin-relative (stream_url) or absolute
  getLastEventId: () => number | null           // read at every (re)connect
  onFrame: (frame: NexSseFrame) => void
  onStatus: (status: NexSseStatus, err?: Error) => void
  fetchImpl?: typeof fetch                      // tests
  backoff?: Partial<{ initialMs: number; maxMs: number; jitter: number; stableMs: number }>
}
export interface NexSseHandle { close(): void }
export function openNexSse(opts: NexSseOptions): NexSseHandle
export function resolveNexStreamUrl(hostId: string, url: string): string   // I1
```

- [ ] **Step 1: Write the failing test**

```ts
// @vitest-environment node
// spa/src/lib/nex/nex-sse.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { openNexSse, resolveNexStreamUrl } from './nex-sse'
import type { NexSseFrame } from './sse-parser'

function streamOf(chunks: string[], opts: { hang?: boolean } = {}): ReadableStream<Uint8Array> {
  const enc = new TextEncoder()
  return new ReadableStream({
    start(ctrl) {
      for (const c of chunks) ctrl.enqueue(enc.encode(c))
      if (!opts.hang) ctrl.close()
    },
  })
}

function sseResponse(chunks: string[], opts?: { hang?: boolean }): Response {
  return new Response(streamOf(chunks, opts), { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
}

describe('nex-sse', () => {
  let hostId: string
  beforeEach(() => {
    useHostStore.getState().reset()
    useHostStore.getState().addHost({ name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok-1' } as never)
    hostId = useHostStore.getState().hostOrder.find((id) => useHostStore.getState().hosts[id].name === 'mlab')!
    vi.useFakeTimers()
  })
  afterEach(() => vi.useRealTimers())

  it('resolveNexStreamUrl resolves against the daemon origin without re-prefixing', () => {
    expect(resolveNexStreamUrl(hostId, '/api/nex/v1/events?execution_id=exc_1'))
      .toBe('http://100.64.0.2:7860/api/nex/v1/events?execution_id=exc_1')
    expect(resolveNexStreamUrl(hostId, 'http://other:1/x')).toBe('http://other:1/x')
  })

  it('sends Bearer, X-Pdx-Client, Accept and Last-Event-ID; delivers frames; reports status', async () => {
    const fetchImpl = vi.fn().mockResolvedValueOnce(sseResponse(['id: 3\nevent: assistant\ndata: {"a":1}\n\n', 'event: stream_event\ndata: {}\n\n'], { hang: true }))
    const frames: NexSseFrame[] = []
    const statuses: string[] = []
    const h = openNexSse({
      hostId, url: '/api/nex/v1/events?execution_id=exc_1',
      getLastEventId: () => 2,
      onFrame: (f) => frames.push(f),
      onStatus: (s) => statuses.push(s),
      fetchImpl,
    })
    await vi.advanceTimersByTimeAsync(0)
    const [url, init] = fetchImpl.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/events?execution_id=exc_1')
    const hd = new Headers(init.headers)
    expect(hd.get('Authorization')).toBe('Bearer tok-1')
    expect(hd.get('X-Pdx-Client')).toMatch(/^[A-Za-z0-9._-]{1,64}$/)
    expect(hd.get('Accept')).toBe('text/event-stream')
    expect(hd.get('Last-Event-ID')).toBe('2')
    expect(url).not.toContain('ticket')
    await vi.advanceTimersByTimeAsync(0)
    expect(frames).toEqual([
      { id: '3', event: 'assistant', data: '{"a":1}' },
      { id: null, event: 'stream_event', data: '{}' },
    ])
    expect(statuses).toEqual(['connecting', 'open'])
    h.close()
    expect(statuses.at(-1)).toBe('closed')
  })

  it('omits Last-Event-ID when no cursor is known', async () => {
    const fetchImpl = vi.fn().mockResolvedValueOnce(sseResponse([], { hang: true }))
    openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus: () => {}, fetchImpl })
    await vi.advanceTimersByTimeAsync(0)
    expect(new Headers(fetchImpl.mock.calls[0][1].headers).has('Last-Event-ID')).toBe(false)
  })

  it('reconnects with the CURRENT cursor and exponential backoff when the stream ends', async () => {
    let cursor = 5
    const fetchImpl = vi.fn()
      .mockResolvedValueOnce(sseResponse(['id: 6\nevent: user\ndata: {}\n\n']))   // ends
      .mockResolvedValueOnce(sseResponse([]))                                          // ends again
      .mockResolvedValueOnce(sseResponse([], { hang: true }))
    const statuses: string[] = []
    openNexSse({
      hostId, url: '/api/nex/v1/events', getLastEventId: () => cursor,
      onFrame: (f) => { if (f.id) cursor = Number(f.id) },
      onStatus: (s) => statuses.push(s),
      fetchImpl, backoff: { initialMs: 1000, maxMs: 30000, jitter: 0, stableMs: 10000 },
    })
    await vi.advanceTimersByTimeAsync(0)
    expect(statuses).toEqual(['connecting', 'open', 'reconnecting'])
    await vi.advanceTimersByTimeAsync(999)
    expect(fetchImpl).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(fetchImpl).toHaveBeenCalledTimes(2)
    expect(new Headers(fetchImpl.mock.calls[1][1].headers).get('Last-Event-ID')).toBe('6')
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(2000)   // second retry after 2 s
    expect(fetchImpl).toHaveBeenCalledTimes(3)
    expect(statuses.at(-1)).toBe('open')
  })

  it('stops on 401/403 with closed + error, retries on 503', async () => {
    const onStatus = vi.fn()
    const fetchImpl = vi.fn().mockResolvedValueOnce(new Response('', { status: 401 }))
    openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus, fetchImpl })
    await vi.advanceTimersByTimeAsync(0)
    expect(onStatus).toHaveBeenLastCalledWith('closed', expect.any(Error))
    await vi.advanceTimersByTimeAsync(60000)
    expect(fetchImpl).toHaveBeenCalledTimes(1)

    const fetch503 = vi.fn().mockResolvedValueOnce(new Response('{"code":"draining"}', { status: 503 })).mockResolvedValueOnce(sseResponse([], { hang: true }))
    const st: string[] = []
    openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus: (s) => st.push(s), fetchImpl: fetch503, backoff: { jitter: 0 } })
    await vi.advanceTimersByTimeAsync(0)
    expect(st.at(-1)).toBe('reconnecting')
    await vi.advanceTimersByTimeAsync(1000)
    expect(fetch503).toHaveBeenCalledTimes(2)
  })

  it('close() aborts the fetch, cancels a pending reconnect and is idempotent', async () => {
    const fetchImpl = vi.fn().mockResolvedValueOnce(sseResponse([]))
    const onStatus = vi.fn()
    const h = openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus, fetchImpl, backoff: { jitter: 0 } })
    await vi.advanceTimersByTimeAsync(0)
    expect(onStatus).toHaveBeenLastCalledWith('reconnecting')
    h.close()
    h.close()
    await vi.advanceTimersByTimeAsync(60000)
    expect(fetchImpl).toHaveBeenCalledTimes(1)
    expect(onStatus.mock.calls.filter((c) => c[0] === 'closed')).toHaveLength(1)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/nex/nex-sse.test.ts`
Expected: FAIL — cannot resolve `./nex-sse`.

- [ ] **Step 3: Write minimal implementation**

```ts
// spa/src/lib/nex/nex-sse.ts — fetch + ReadableStream SSE client for
// /api/nex/v1/events. A native EventSource is ruled out by the P-A contract
// (§4.3): it cannot send Authorization / X-Pdx-Client / Last-Event-ID
// headers and its auto-reconnect would replay a spent ticket URL. This
// client owns reconnection (backoff) but NOT the cursor: the reducer/store
// advances lastSeq from durable frames and this layer reads it back through
// getLastEventId at every (re)connect, so a cursor advanced by history
// paging while the socket was down is honoured.
import { useHostStore } from '../../stores/useHostStore'
import { hostAuthHeaders } from '../host-api'
import { getNexClientId } from './client-id'
import { SseParser, type NexSseFrame } from './sse-parser'

export type NexSseStatus = 'connecting' | 'open' | 'reconnecting' | 'closed'

export interface NexSseBackoff {
  initialMs: number
  maxMs: number
  /** 0..1 fraction of the delay added/subtracted at random. */
  jitter: number
  /** A connection open at least this long resets the backoff. */
  stableMs: number
}

const DEFAULT_BACKOFF: NexSseBackoff = { initialMs: 1000, maxMs: 30000, jitter: 0.2, stableMs: 10000 }

export interface NexSseOptions {
  hostId: string
  /** Origin-relative (as returned by attach(observe).stream_url) or absolute. */
  url: string
  getLastEventId: () => number | null
  onFrame: (frame: NexSseFrame) => void
  onStatus: (status: NexSseStatus, err?: Error) => void
  fetchImpl?: typeof fetch
  backoff?: Partial<NexSseBackoff>
}

export interface NexSseHandle {
  close(): void
}

/**
 * I1: stream_url already carries /api/nex; resolve it against the daemon
 * origin only. An absolute URL is passed through untouched.
 */
export function resolveNexStreamUrl(hostId: string, url: string): string {
  const base = useHostStore.getState().getDaemonBase(hostId)
  return new URL(url, base).toString()
}

export function openNexSse(opts: NexSseOptions): NexSseHandle {
  const fetchImpl = opts.fetchImpl ?? fetch
  const backoff: NexSseBackoff = { ...DEFAULT_BACKOFF, ...opts.backoff }
  const target = resolveNexStreamUrl(opts.hostId, opts.url)

  let closed = false
  let attempt = 0
  let controller: AbortController | null = null
  let timer: ReturnType<typeof setTimeout> | null = null

  const status = (s: NexSseStatus, err?: Error) => { if (!closed || s === 'closed') opts.onStatus(s, err) }

  const scheduleReconnect = () => {
    if (closed) return
    const exp = Math.min(backoff.maxMs, backoff.initialMs * 2 ** attempt)
    const delta = exp * backoff.jitter * (Math.random() * 2 - 1)
    attempt += 1
    status('reconnecting')
    timer = setTimeout(() => { timer = null; void connect() }, Math.max(0, Math.round(exp + delta)))
  }

  const connect = async () => {
    if (closed) return
    controller = new AbortController()
    status(attempt === 0 ? 'connecting' : 'reconnecting')
    const headers = new Headers(hostAuthHeaders(opts.hostId))
    headers.set('Accept', 'text/event-stream')
    headers.set('X-Pdx-Client', getNexClientId())
    const last = opts.getLastEventId()
    if (last != null) headers.set('Last-Event-ID', String(last))

    let res: Response
    try {
      res = await fetchImpl(target, { headers, signal: controller.signal, cache: 'no-store' })
    } catch (e) {
      if (closed) return
      scheduleReconnect()
      return
    }
    if (closed) return
    if (res.status === 401 || res.status === 403) {
      closed = true
      status('closed', new Error(`nex sse: HTTP ${res.status}`))
      return
    }
    if (!res.ok || !res.body) {
      scheduleReconnect()
      return
    }

    status('open')
    const openedAt = Date.now()
    const parser = new SseParser()
    const decoder = new TextDecoder()
    const reader = res.body.getReader()
    try {
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        for (const frame of parser.push(decoder.decode(value, { stream: true }))) {
          if (closed) return
          opts.onFrame(frame)
        }
      }
    } catch {
      // aborted or network error — fall through to reconnect
    }
    if (closed) return
    if (Date.now() - openedAt >= backoff.stableMs) attempt = 0
    scheduleReconnect()
  }

  void connect()

  return {
    close() {
      if (closed) return
      closed = true
      if (timer) { clearTimeout(timer); timer = null }
      controller?.abort()
      opts.onStatus('closed')
    },
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/nex/nex-sse.test.ts`
Expected: PASS (6 tests). If `Date.now()` under fake timers makes `stableMs` reset misbehave in the backoff test, use `vi.setSystemTime` advances (the test above only relies on `advanceTimersByTimeAsync`, which also advances `Date.now()` in vitest ≥ 1).

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/lib/nex/nex-sse.ts spa/src/lib/nex/nex-sse.test.ts && git commit --only spa/src/lib/nex/nex-sse.ts spa/src/lib/nex/nex-sse.test.ts -m "feat(spa): nex SSE transport with header resume and backoff"
```

---

### Task 6: Event reducer (pure)

**Files:**
- Create: `spa/src/lib/nex/event-reducer.ts`
- Test: `spa/src/lib/nex/event-reducer.test.ts`

**Interfaces:**
- Consumes: `NexEvent`, `ExecutionSummary` (Task 2), `StreamMessage` (`../stream-ws`).
- Produces:

```ts
export interface ExecutionState {
  summary: ExecutionSummary | null
  messages: StreamMessage[]
  lastSeq: number
  historyLoaded: boolean
  summaryStale: boolean          // a lifecycle event arrived; hook refetches summary
  sse: 'idle' | 'connecting' | 'open' | 'reconnecting' | 'closed'
  sseError: string | null
  lease: { leaseId: string; expiresAt: number } | null
  leaseError: { code: string; heldBy?: string } | null
  pendingSend: boolean
  pendingLocal: { text: string; delivery: 'delivered' | 'queued' | null } | null
  sendError: { code: string; message: string; turnId?: string } | null
  lastTurn: { turnId: string; delivery: 'delivered' | 'queued' } | null
}
export function defaultExecutionState(): ExecutionState
export function isLifecycleKind(kind: string): boolean          // execution.* | lease.*
export function frameToEvent(frame: NexSseFrame): NexEvent | null   // null for transient or unparsable
export function applyDurableEvent(s: ExecutionState, ev: NexEvent): ExecutionState
```

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/lib/nex/event-reducer.test.ts
import { describe, it, expect } from 'vitest'
import { applyDurableEvent, defaultExecutionState, frameToEvent, isLifecycleKind, type ExecutionState } from './event-reducer'
import type { NexEvent, ExecutionSummary } from './types'

const ev = (seq: number, kind: string, payload: Record<string, unknown> = {}): NexEvent =>
  ({ seq, execution_id: 'exc_1', kind, payload, created_at: 1 })

const summary = (extra: Partial<ExecutionSummary> = {}): ExecutionSummary => ({
  id: 'exc_1', state: 'idle', provider: 'claude', principal_id: 'pdx:mlab', cwd: '/w', mount_kind: 'dev',
  brief: 'hi', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false, ...extra,
})

describe('applyDurableEvent', () => {
  it('appends provider passthrough kinds as messages and advances lastSeq', () => {
    let s = defaultExecutionState()
    s = applyDurableEvent(s, ev(1, 'assistant', { type: 'assistant', message: { role: 'assistant', content: [], stop_reason: null } }))
    s = applyDurableEvent(s, ev(2, 'some_future_provider_kind', { type: 'some_future_provider_kind' }))
    expect(s.messages).toHaveLength(2)
    expect(s.lastSeq).toBe(2)
  })

  it('is idempotent by seq', () => {
    let s = defaultExecutionState()
    s = applyDurableEvent(s, ev(5, 'assistant', { type: 'assistant' }))
    const again = applyDurableEvent(s, ev(5, 'assistant', { type: 'assistant' }))
    expect(again).toBe(s)
    const older = applyDurableEvent(s, ev(3, 'assistant', { type: 'assistant' }))
    expect(older).toBe(s)
  })

  it('turns execution.delegated brief and message_accepted text into user bubbles, clearing pendingLocal', () => {
    let s: ExecutionState = { ...defaultExecutionState(), pendingLocal: { text: 'and more', delivery: 'queued' } }
    s = applyDurableEvent(s, ev(1, 'execution.delegated', { brief: 'do the thing', principal_id: 'p' }))
    s = applyDurableEvent(s, ev(2, 'execution.message_accepted', { text: 'and more', turn_id: 't1', principal_id: 'p' }))
    expect(s.messages).toEqual([
      { type: 'user', message: { role: 'user', content: [{ type: 'text', text: 'do the thing' }], stop_reason: null } },
      { type: 'user', message: { role: 'user', content: [{ type: 'text', text: 'and more' }], stop_reason: null } },
    ])
    expect(s.pendingLocal).toBeNull()
  })

  it('skips message_accepted with no text (site-wide stripped) without adding a bubble', () => {
    const s = applyDurableEvent(defaultExecutionState(), ev(1, 'execution.message_accepted', { turn_id: 't1' }))
    expect(s.messages).toEqual([])
    expect(s.lastSeq).toBe(1)
  })

  it('patches summary fields carried by lifecycle events and marks the summary stale', () => {
    let s: ExecutionState = { ...defaultExecutionState(), summary: summary() }
    s = applyDurableEvent(s, ev(1, 'execution.running', {}))
    expect(s.summary?.state).toBe('running')
    expect(s.summaryStale).toBe(true)
    s = applyDurableEvent(s, ev(2, 'execution.observer_attached', { observers: 3, principal_id: 'x' }))
    expect(s.summary?.observers).toBe(3)
    s = applyDurableEvent(s, ev(3, 'execution.terminal', { turn_id: 't1', reason: 'completed', state: 'idle' }))
    expect(s.summary?.last_turn_reason).toBe('completed')
    expect(s.summary?.state).toBe('idle')
    s = applyDurableEvent(s, ev(4, 'execution.terminal', { turn_id: 't2', reason: 'error', state: 'failed', detail: 'boom' }))
    expect(s.summary?.state).toBe('failed')
    s = applyDurableEvent(s, ev(5, 'execution.terminated', { principal_id: 'p' }))
    expect(s.summary?.state).toBe('terminated')
    s = applyDurableEvent(s, ev(6, 'execution.archived', { principal_id: 'p' }))
    expect(s.summary?.archived).toBe(true)
  })

  it('does not invent summary fields when there is no summary yet', () => {
    const s = applyDurableEvent(defaultExecutionState(), ev(1, 'execution.running', {}))
    expect(s.summary).toBeNull()
    expect(s.summaryStale).toBe(true)
  })

  it('lease.acquired/released patch summary.lease only, never the local lease (I11)', () => {
    let s: ExecutionState = { ...defaultExecutionState(), summary: summary(), lease: { leaseId: 'ls_me', expiresAt: 99 } }
    s = applyDurableEvent(s, ev(1, 'lease.acquired', { lease_id: 'ls_other', principal_id: 'pdx:mlab/t-other', expires_at: 50 }))
    expect(s.summary?.lease).toEqual({ principal_id: 'pdx:mlab/t-other', expires_at: 50 })
    expect(s.lease).toEqual({ leaseId: 'ls_me', expiresAt: 99 })
    s = applyDurableEvent(s, ev(2, 'lease.released', { principal_id: 'pdx:mlab/t-other' }))
    expect(s.summary?.lease).toBeUndefined()
    expect(s.lease).toEqual({ leaseId: 'ls_me', expiresAt: 99 })
  })

  it('result, execution.terminal and execution.error clear pendingSend; sendError is left alone', () => {
    const base: ExecutionState = { ...defaultExecutionState(), pendingSend: true, sendError: { code: 'x', message: 'y' } }
    expect(applyDurableEvent(base, ev(1, 'result', { type: 'result', total_cost_usd: 0.1 })).pendingSend).toBe(false)
    expect(applyDurableEvent(base, ev(1, 'execution.terminal', { turn_id: 't', reason: 'error', state: 'idle' })).pendingSend).toBe(false)
    expect(applyDurableEvent(base, ev(1, 'execution.error', { reason: 'finish_turn_failed' })).pendingSend).toBe(false)
    expect(applyDurableEvent(base, ev(1, 'result', { type: 'result' })).sendError).toEqual({ code: 'x', message: 'y' })
  })

  it('ignores events with a non-finite seq', () => {
    const s = defaultExecutionState()
    expect(applyDurableEvent(s, { ...ev(0, 'assistant'), seq: Number.NaN })).toBe(s)
  })
})

describe('frameToEvent / isLifecycleKind', () => {
  it('returns null for transient frames and unparsable data', () => {
    expect(frameToEvent({ id: null, event: 'stream_event', data: '{}' })).toBeNull()
    expect(frameToEvent({ id: '7', event: 'assistant', data: '{not json' })).toBeNull()
  })
  it('builds a NexEvent from a durable frame', () => {
    expect(frameToEvent({ id: '7', event: 'assistant', data: '{"seq":7,"execution_id":"exc_1","kind":"assistant","payload":{"type":"assistant"},"created_at":9}' }))
      .toEqual({ seq: 7, execution_id: 'exc_1', kind: 'assistant', payload: { type: 'assistant' }, created_at: 9 })
  })
  it('falls back to the frame id/event when the data lacks seq/kind', () => {
    expect(frameToEvent({ id: '8', event: 'user', data: '{"type":"user"}' }))
      .toEqual({ seq: 8, execution_id: '', kind: 'user', payload: { type: 'user' }, created_at: 0 })
  })
  it('classifies kinds', () => {
    expect(isLifecycleKind('execution.running')).toBe(true)
    expect(isLifecycleKind('lease.acquired')).toBe(true)
    expect(isLifecycleKind('assistant')).toBe(false)
    expect(isLifecycleKind('rate_limit_event')).toBe(false)
  })
})
```

> Verified against `~/Workspace/wake/nexen/api/sse.go:295`: a durable frame is `id: <seq>\nevent: <kind>\ndata: <payload JSON>` — the `data` is the **bare payload**, not the `eventView` wrapper. So the primary path of `frameToEvent` is "seq from `id:`, kind from `event:`, payload = data" (the "falls back" test); the full-shape branch is kept only because `GET …/events` history items ARE the wrapper and a future daemon could emit it on the wire too. Keep both tests.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/nex/event-reducer.test.ts`
Expected: FAIL — cannot resolve `./event-reducer`.

- [ ] **Step 3: Write minimal implementation**

```ts
// spa/src/lib/nex/event-reducer.ts — pure reducer from Nexen durable events
// to the per-execution view state (spec §4.2.4). No React, no fetch, no
// store: the hook feeds it history pages and SSE frames alike. Transient
// frames never reach it (P-B2 adds a partial buffer for them).
import type { StreamMessage } from '../stream-ws'
import type { NexSseFrame } from './sse-parser'
import type { ExecutionSummary, NexEvent } from './types'

export interface ExecutionState {
  summary: ExecutionSummary | null
  /** Provider passthrough + synthetic user bubbles, in seq order. */
  messages: StreamMessage[]
  /** Highest durable seq applied (history or SSE). Never moved by transient frames. */
  lastSeq: number
  historyLoaded: boolean
  /** A lifecycle event arrived; the summary is authoritative, so the hook refetches. */
  summaryStale: boolean
  sse: 'idle' | 'connecting' | 'open' | 'reconnecting' | 'closed'
  sseError: string | null
  /** The lease THIS tab holds — written only from attach(control)/renew responses (I11). */
  lease: { leaseId: string; expiresAt: number } | null
  leaseError: { code: string; heldBy?: string } | null
  pendingSend: boolean
  /** Optimistic user bubble; replaced by the durable execution.message_accepted. */
  pendingLocal: { text: string; delivery: 'delivered' | 'queued' | null } | null
  sendError: { code: string; message: string; turnId?: string } | null
  lastTurn: { turnId: string; delivery: 'delivered' | 'queued' } | null
}

export function defaultExecutionState(): ExecutionState {
  return {
    summary: null,
    messages: [],
    lastSeq: 0,
    historyLoaded: false,
    summaryStale: false,
    sse: 'idle',
    sseError: null,
    lease: null,
    leaseError: null,
    pendingSend: false,
    pendingLocal: null,
    sendError: null,
    lastTurn: null,
  }
}

/** Nexen's own (closed-set) kinds; everything else is provider passthrough. */
export function isLifecycleKind(kind: string): boolean {
  return kind.startsWith('execution.') || kind.startsWith('lease.')
}

/**
 * Durable SSE frame → NexEvent. Returns null for transient frames (no id)
 * and for data that is not JSON. Accepts both the full eventView shape and
 * a bare payload (then seq comes from the id line and kind from event:).
 */
export function frameToEvent(frame: NexSseFrame): NexEvent | null {
  if (frame.id == null) return null
  let parsed: unknown
  try {
    parsed = JSON.parse(frame.data)
  } catch {
    return null
  }
  if (!parsed || typeof parsed !== 'object') return null
  const obj = parsed as Record<string, unknown>
  const idSeq = Number(frame.id)
  if (typeof obj.kind === 'string' && obj.payload && typeof obj.payload === 'object') {
    return {
      seq: typeof obj.seq === 'number' ? obj.seq : idSeq,
      execution_id: typeof obj.execution_id === 'string' ? obj.execution_id : '',
      kind: obj.kind,
      payload: obj.payload as Record<string, unknown>,
      created_at: typeof obj.created_at === 'number' ? obj.created_at : 0,
    }
  }
  return { seq: idSeq, execution_id: '', kind: frame.event, payload: obj, created_at: 0 }
}

function userBubble(text: string): StreamMessage {
  return { type: 'user', message: { role: 'user', content: [{ type: 'text', text }], stop_reason: null } } as StreamMessage
}

function str(p: Record<string, unknown>, k: string): string | undefined {
  const v = p[k]
  return typeof v === 'string' ? v : undefined
}

function patchSummary(s: ExecutionState, patch: Partial<ExecutionSummary>): ExecutionState {
  return { ...s, summaryStale: true, summary: s.summary ? { ...s.summary, ...patch } : null }
}

export function applyDurableEvent(s: ExecutionState, ev: NexEvent): ExecutionState {
  if (!Number.isFinite(ev.seq) || ev.seq <= s.lastSeq) return s
  const p = ev.payload ?? {}
  let next: ExecutionState = { ...s, lastSeq: ev.seq }

  if (!isLifecycleKind(ev.kind)) {
    next = { ...next, messages: [...next.messages, p as StreamMessage] }
    if (ev.kind === 'result') next.pendingSend = false
    return next
  }

  switch (ev.kind) {
    case 'execution.delegated': {
      const brief = str(p, 'brief')
      if (brief) next = { ...next, messages: [...next.messages, userBubble(brief)] }
      return patchSummary(next, {})
    }
    case 'execution.message_accepted': {
      const text = str(p, 'text')
      next = { ...next, pendingLocal: null }
      if (text) next = { ...next, messages: [...next.messages, userBubble(text)] }
      return next
    }
    case 'execution.running':
      return patchSummary(next, { state: 'running' })
    case 'execution.terminal': {
      // execution/turn.go:568 — {turn_id, reason, state, detail?}; state is
      // the execution's state after the turn ended (idle, or failed, or a
      // terminal state that outranked it), so take it rather than assume idle.
      next = { ...next, pendingSend: false }
      const reason = str(p, 'reason')
      const state = str(p, 'state') ?? 'idle'
      return patchSummary(next, { state, ...(reason ? { last_turn_reason: reason } : {}) })
    }
    case 'execution.error':
      return patchSummary({ ...next, pendingSend: false }, {})
    case 'execution.rejected':
      return patchSummary(next, { state: 'rejected', ...(str(p, 'reason') ? { reject_reason: str(p, 'reason') } : {}) })
    case 'execution.terminated':
      // execution/service.go:1184 — {principal_id} only; terminal_reason is
      // the summary's business, the refetch brings it.
      return patchSummary({ ...next, pendingSend: false }, { state: 'terminated' })
    case 'execution.archived':
      return patchSummary(next, { archived: true })
    case 'execution.unarchived':
      return patchSummary(next, { archived: false })
    case 'execution.observer_attached':
    case 'execution.observer_detached': {
      const observers = p.observers
      return patchSummary(next, typeof observers === 'number' ? { observers } : {})
    }
    case 'lease.acquired': {
      const principal_id = str(p, 'principal_id')
      const expires_at = typeof p.expires_at === 'number' ? p.expires_at : 0
      return patchSummary(next, principal_id ? { lease: { principal_id, expires_at } } : {})
    }
    case 'lease.released': {
      if (!next.summary) return { ...next, summaryStale: true }
      const { lease: _dropped, ...rest } = next.summary
      return { ...next, summaryStale: true, summary: rest as ExecutionSummary }
    }
    default:
      // interrupt_requested / interrupted / turn_stalled / turn_orphaned …:
      // nothing to render in P-B; the summary refetch carries the state.
      return { ...next, summaryStale: true }
  }
}
```

> Payload keys verified 2026-09-15 against the emitters: `execution.delegated` `{principal_id, provider, cwd, …, brief}` (`service.go:512`), `execution.message_accepted` `{turn_id, principal_id, text}` (`service.go:739`), `execution.terminal` `{turn_id, reason, state, detail?}` (`turn.go:568`), `execution.rejected` `{principal_id, reason}` (`admission.go:120`), `execution.terminated`/`archived`/`unarchived` `{principal_id}` (`service.go:1115/1184`), `lease.acquired` `{lease_id, principal_id, expires_at}` and `lease.released` `{lease_id, principal_id, reason}` (`lease.go:51/116`), observer events `{observers, principal_id}` (`api/observers.go`).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/nex/event-reducer.test.ts`
Expected: PASS (13 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/lib/nex/event-reducer.ts spa/src/lib/nex/event-reducer.test.ts && git commit --only spa/src/lib/nex/event-reducer.ts spa/src/lib/nex/event-reducer.test.ts -m "feat(spa): pure reducer from nex durable events to execution state"
```

---

### Task 7: `useExecutionStore`

**Files:**
- Create: `spa/src/stores/useExecutionStore.ts`
- Test: `spa/src/stores/useExecutionStore.test.ts`

**Interfaces:**
- Consumes: `ExecutionState`, `defaultExecutionState`, `applyDurableEvent` (Task 6), `NexEvent`, `ExecutionSummary` (Task 2), `compositeKey` (`../lib/composite-key`).
- Produces:

```ts
export function executionKey(hostId: string, executionId: string): string   // = compositeKey
export function splitExecutionKey(key: string): { hostId: string; executionId: string }   // lastIndexOf(':')
interface ExecutionStore {
  executions: Record<string, ExecutionState>
  setSummary(hostId, executionId, summary: ExecutionSummary | null): void      // also clears summaryStale
  applyEvents(hostId, executionId, events: NexEvent[]): void
  setHistoryLoaded(hostId, executionId, v: boolean): void
  setSse(hostId, executionId, status: ExecutionState['sse'], err?: string | null): void
  setLease(hostId, executionId, lease: ExecutionState['lease']): void
  setLeaseError(hostId, executionId, err: ExecutionState['leaseError']): void
  setPendingSend(hostId, executionId, v: boolean): void
  setPendingLocal(hostId, executionId, local: ExecutionState['pendingLocal']): void
  setSendError(hostId, executionId, err: ExecutionState['sendError']): void
  setLastTurn(hostId, executionId, turn: ExecutionState['lastTurn']): void
  clearExecution(hostId, executionId): void
  clearHost(hostId): void
}
export const useExecutionStore: UseBoundStore<StoreApi<ExecutionStore>>   // create + subscribeWithSelector
```

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/stores/useExecutionStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useExecutionStore, executionKey, splitExecutionKey } from './useExecutionStore'
import type { NexEvent } from '../lib/nex/types'

const ev = (seq: number, kind: string, payload: Record<string, unknown> = {}): NexEvent =>
  ({ seq, execution_id: 'exc_1', kind, payload, created_at: 0 })

describe('useExecutionStore', () => {
  beforeEach(() => useExecutionStore.setState({ executions: {} }))

  it('keys by (hostId, executionId) and splits on the last colon', () => {
    expect(executionKey('h:1', 'exc_1')).toBe('h:1:exc_1')
    expect(splitExecutionKey('h:1:exc_1')).toEqual({ hostId: 'h:1', executionId: 'exc_1' })
  })

  it('applyEvents creates the entry lazily and reduces in order', () => {
    useExecutionStore.getState().applyEvents('h', 'exc_1', [ev(1, 'assistant', { type: 'assistant' }), ev(2, 'result', { type: 'result' })])
    const st = useExecutionStore.getState().executions['h:exc_1']
    expect(st.messages).toHaveLength(2)
    expect(st.lastSeq).toBe(2)
  })

  it('applyEvents with only already-seen seqs does not create a new object', () => {
    const s = useExecutionStore.getState()
    s.applyEvents('h', 'exc_1', [ev(1, 'assistant', { type: 'assistant' })])
    const before = useExecutionStore.getState().executions['h:exc_1']
    s.applyEvents('h', 'exc_1', [ev(1, 'assistant', { type: 'assistant' })])
    expect(useExecutionStore.getState().executions['h:exc_1']).toBe(before)
  })

  it('setSummary stores the summary and clears summaryStale', () => {
    const s = useExecutionStore.getState()
    s.applyEvents('h', 'exc_1', [ev(1, 'execution.running')])
    expect(useExecutionStore.getState().executions['h:exc_1'].summaryStale).toBe(true)
    s.setSummary('h', 'exc_1', { id: 'exc_1', state: 'running' } as never)
    const st = useExecutionStore.getState().executions['h:exc_1']
    expect(st.summary?.state).toBe('running')
    expect(st.summaryStale).toBe(false)
  })

  it('setters update their field only', () => {
    const s = useExecutionStore.getState()
    s.setSse('h', 'exc_1', 'reconnecting', 'boom')
    s.setLease('h', 'exc_1', { leaseId: 'ls', expiresAt: 5 })
    s.setLeaseError('h', 'exc_1', { code: 'lease_held', heldBy: 'p' })
    s.setPendingSend('h', 'exc_1', true)
    s.setPendingLocal('h', 'exc_1', { text: 'x', delivery: null })
    s.setSendError('h', 'exc_1', { code: 'invalid_text', message: 'too long' })
    s.setLastTurn('h', 'exc_1', { turnId: 't', delivery: 'queued' })
    s.setHistoryLoaded('h', 'exc_1', true)
    const st = useExecutionStore.getState().executions['h:exc_1']
    expect(st).toMatchObject({
      sse: 'reconnecting', sseError: 'boom', lease: { leaseId: 'ls', expiresAt: 5 },
      leaseError: { code: 'lease_held', heldBy: 'p' }, pendingSend: true,
      pendingLocal: { text: 'x', delivery: null }, sendError: { code: 'invalid_text', message: 'too long' },
      lastTurn: { turnId: 't', delivery: 'queued' }, historyLoaded: true, messages: [],
    })
    s.setSse('h', 'exc_1', 'open')
    expect(useExecutionStore.getState().executions['h:exc_1'].sseError).toBeNull()
  })

  it('clearExecution removes one entry; clearHost removes only that host', () => {
    const s = useExecutionStore.getState()
    s.applyEvents('h1', 'exc_1', [ev(1, 'assistant', { type: 'assistant' })])
    s.applyEvents('h1', 'exc_2', [ev(1, 'assistant', { type: 'assistant' })])
    s.applyEvents('h10', 'exc_1', [ev(1, 'assistant', { type: 'assistant' })])
    s.clearExecution('h1', 'exc_2')
    expect(Object.keys(useExecutionStore.getState().executions).sort()).toEqual(['h1:exc_1', 'h10:exc_1'])
    s.clearHost('h1')
    expect(Object.keys(useExecutionStore.getState().executions)).toEqual(['h10:exc_1'])
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/stores/useExecutionStore.test.ts`
Expected: FAIL — cannot resolve `./useExecutionStore`.

- [ ] **Step 3: Write minimal implementation**

```ts
// spa/src/stores/useExecutionStore.ts — per-(host, execution) view state for
// Nexen executions (spec §4.2.4). Holds data only: no sockets, no timers —
// those live in the P-B.2 hooks so HMR / StrictMode double-mount can never
// leak a connection through the store. Successor of useStreamStore, which
// P-D removes.
import { create } from 'zustand'
import { subscribeWithSelector } from 'zustand/middleware'
import { compositeKey } from '../lib/composite-key'
import { applyDurableEvent, defaultExecutionState, type ExecutionState } from '../lib/nex/event-reducer'
import type { ExecutionSummary, NexEvent } from '../lib/nex/types'

export function executionKey(hostId: string, executionId: string): string {
  return compositeKey(hostId, executionId)
}

/** Host ids may contain ':'; execution ids (exc_…) never do — split on the last one. */
export function splitExecutionKey(key: string): { hostId: string; executionId: string } {
  const i = key.lastIndexOf(':')
  return i < 0 ? { hostId: '', executionId: key } : { hostId: key.slice(0, i), executionId: key.slice(i + 1) }
}

interface ExecutionStore {
  executions: Record<string, ExecutionState>
  setSummary: (hostId: string, executionId: string, summary: ExecutionSummary | null) => void
  applyEvents: (hostId: string, executionId: string, events: NexEvent[]) => void
  setHistoryLoaded: (hostId: string, executionId: string, v: boolean) => void
  setSse: (hostId: string, executionId: string, status: ExecutionState['sse'], err?: string | null) => void
  setLease: (hostId: string, executionId: string, lease: ExecutionState['lease']) => void
  setLeaseError: (hostId: string, executionId: string, err: ExecutionState['leaseError']) => void
  setPendingSend: (hostId: string, executionId: string, v: boolean) => void
  setPendingLocal: (hostId: string, executionId: string, local: ExecutionState['pendingLocal']) => void
  setSendError: (hostId: string, executionId: string, err: ExecutionState['sendError']) => void
  setLastTurn: (hostId: string, executionId: string, turn: ExecutionState['lastTurn']) => void
  clearExecution: (hostId: string, executionId: string) => void
  clearHost: (hostId: string) => void
}

export const useExecutionStore = create<ExecutionStore>()(subscribeWithSelector((set) => {
  const patch = (hostId: string, executionId: string, fn: (cur: ExecutionState) => ExecutionState) =>
    set((s) => {
      const key = executionKey(hostId, executionId)
      const cur = s.executions[key] ?? defaultExecutionState()
      const next = fn(cur)
      if (next === cur && key in s.executions) return s
      return { executions: { ...s.executions, [key]: next } }
    })

  return {
    executions: {},

    setSummary: (h, e, summary) => patch(h, e, (c) => ({ ...c, summary, summaryStale: false })),

    applyEvents: (h, e, events) => patch(h, e, (c) => events.reduce(applyDurableEvent, c)),

    setHistoryLoaded: (h, e, v) => patch(h, e, (c) => ({ ...c, historyLoaded: v })),

    setSse: (h, e, status, err = null) => patch(h, e, (c) => ({ ...c, sse: status, sseError: err })),

    setLease: (h, e, lease) => patch(h, e, (c) => ({ ...c, lease })),

    setLeaseError: (h, e, err) => patch(h, e, (c) => ({ ...c, leaseError: err })),

    setPendingSend: (h, e, v) => patch(h, e, (c) => ({ ...c, pendingSend: v })),

    setPendingLocal: (h, e, local) => patch(h, e, (c) => ({ ...c, pendingLocal: local })),

    setSendError: (h, e, err) => patch(h, e, (c) => ({ ...c, sendError: err })),

    setLastTurn: (h, e, turn) => patch(h, e, (c) => ({ ...c, lastTurn: turn })),

    clearExecution: (h, e) => set((s) => {
      const { [executionKey(h, e)]: _dropped, ...rest } = s.executions
      return { executions: rest }
    }),

    clearHost: (hostId) => set((s) => {
      const executions: Record<string, ExecutionState> = {}
      for (const [k, v] of Object.entries(s.executions)) {
        if (splitExecutionKey(k).hostId !== hostId) executions[k] = v
      }
      return { executions }
    }),
  }
}))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/stores/useExecutionStore.test.ts`
Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/stores/useExecutionStore.ts spa/src/stores/useExecutionStore.test.ts && git commit --only spa/src/stores/useExecutionStore.ts spa/src/stores/useExecutionStore.test.ts -m "feat(spa): useExecutionStore keyed by (host, execution)"
```

---

### Task 8: Host-removal cascade clears the execution store

**Files:**
- Modify: `spa/src/lib/host-lifecycle.ts` (import near line 6; cascade near line 127 where `streamStore.clearHost(hostId)` runs)
- Test: `spa/src/lib/host-lifecycle.test.ts` (add one case; read the file's existing seeding helpers first and reuse them)

**Interfaces:**
- Consumes: `useExecutionStore.clearHost` (Task 7).
- Produces: nothing new; `deleteHostCascade(hostId, closeTabs)` now also clears execution entries for that host **after** the tab-close loop (spec §4.3.4 ordering).

- [ ] **Step 1: Write the failing test**

Append to `spa/src/lib/host-lifecycle.test.ts` inside the existing top-level `describe` (adapt the host seeding to whatever helper the file already uses — the existing tests seed two hosts because the cascade vetoes deleting the last one):

```ts
  it('clears useExecutionStore entries for the removed host only', () => {
    // seed two hosts the way the sibling tests do, then:
    useExecutionStore.setState({ executions: {} })
    useExecutionStore.getState().applyEvents(hostA, 'exc_1', [{ seq: 1, execution_id: 'exc_1', kind: 'assistant', payload: { type: 'assistant' }, created_at: 0 }])
    useExecutionStore.getState().applyEvents(hostB, 'exc_1', [{ seq: 1, execution_id: 'exc_1', kind: 'assistant', payload: { type: 'assistant' }, created_at: 0 }])
    deleteHostCascade(hostA, false)
    expect(Object.keys(useExecutionStore.getState().executions)).toEqual([`${hostB}:exc_1`])
  })
```

with `import { useExecutionStore } from '../stores/useExecutionStore'` at the top.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/host-lifecycle.test.ts`
Expected: FAIL — both keys still present.

- [ ] **Step 3: Write minimal implementation**

In `host-lifecycle.ts`: add `import { useExecutionStore } from '../stores/useExecutionStore'` and, right after `streamStore.clearHost(hostId)`:

```ts
  // Nexen execution view state for this host. Runs after the tab-close loop
  // above so no execution pane's hook observes a half-cleared store (spec
  // §4.3.4); undo restores the tabs, whose hooks re-subscribe from scratch,
  // so nothing here needs snapshotting.
  useExecutionStore.getState().clearHost(hostId)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/lib/host-lifecycle.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/lib/host-lifecycle.ts spa/src/lib/host-lifecycle.test.ts && git commit --only spa/src/lib/host-lifecycle.ts spa/src/lib/host-lifecycle.test.ts -m "feat(spa): host removal clears execution store"
```

---

### Task 9: Full verification

**Files:** none new.

- [ ] **Step 1: Run the whole SPA suite, lint and build**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run 2>&1 | tail -6 && pnpm run lint 2>&1 | tail -5 && pnpm run build 2>&1 | tail -3
```

Expected: all test files pass (baseline was 404 files / 5034 tests before this plan; now +7 files), lint clean, build succeeds.

- [ ] **Step 2: Fix anything red, then commit the fixes with a `fix(spa): …` message naming the cause.**

---

## Self-review notes

- Spec coverage: §4.2.1 → T1; §4.2.2 → T2+T3; §4.2.3 → T4+T5; §4.2.4 → T6+T7; §4.3.4 clearHost ordering → T8; I1 → T5 test 1; I2 → T3 test 1 + T5 test 2; I4 → T6 idempotence + T5 transient frame id null; I5 → T5 reconnect test; I11 (reducer half) → T6 lease test; I12 (state half) → T6 pendingSend/sendError tests. I3/I6/I7/I10/I13 are P-B.2 (hooks/components).
- `summaryStale` is an addition over the spec's state list (spec §4.2.4 says "patch from payload where present"); it is the mechanism by which the P-B.2 hook honours "the summary is authoritative" without polling. Recorded here so the P-B.2 plan uses it.
- Types used consistently: `ExecutionState` fields identical in T6 (definition), T7 (setters) and the spec; `NexEvent` shape identical in T2, T6, T7, T8.
