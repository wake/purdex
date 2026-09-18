# Peer Pairing D2 — SPA Peers sub-page: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the per-host `Peers` sub-page (`/hosts/<id>/peers`) that lists a daemon's configured peer entries, joins each to the App host that *is* that peer, verifies both directions live through the D1 route, names the pair's status with one of five words, and lets the operator adopt a drifted alias with one click.

**Architecture:** Three layers, each testable without the one above it. `host-api.ts` gains four typed wrappers over the D1/D0 routes (no logic). `lib/peer-pairing.ts` holds the three pure rules (`matchCounterpart`, `pairStatus`, `aliasDrift`) and `lib/peer-pairing-load.ts` holds the §5.2 orchestration (metadata → join → parallel verifies) as an async function over an injected API object that *emits snapshots* as verifies land. `PeersSection.tsx` only renders a snapshot and wires three buttons (Refresh, Rename outbound, Rename return). Nothing is cached across mounts; nothing is persisted; no token value ever reaches component state.

**Tech Stack:** React 19, Zustand 5 (read-only here: `useHostStore`), Vitest + `@testing-library/react`, Phosphor Icons, flat-key i18n (`useI18nStore.t`).

**Spec:** `docs/specs/2026-09-18-peer-pairing-ui-spec.md` — §2.1 (the live fixture), §2.3 (what the SPA has), §3 D-2/D-3/D-4/D-5/D-8, §5 (this phase), §8.2 (tests + mutation), §9 D2 (real-machine acceptance).

## Global Constraints

- Run every command from the worktree root: `/Users/wake/Workspace/wake/purdex/.claude/worktrees/peer-pairing-d2`. Prefix every Bash call with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/peer-pairing-d2 && `.
- Commit with `git commit --only <files>` naming each file — the repo root has a version-controlled `pdx` binary; never `git add -A`, never `-am`, never expand `$(git diff --name-only)`.
- SPA package manager is **pnpm**. Tests: `cd spa && npx vitest run <path>`; lint: `cd spa && pnpm run lint`; build: `cd spa && pnpm run build`. Full SPA suite before the PR: `cd spa && npx vitest run`.
- Every value that comes from a daemon response (`self_alias`, `daemon_version`, `error`, `host_id`, `alias`, `url`) is rendered as text only — React escapes it; never put it into `dangerouslySetInnerHTML`, a `title` built by string concatenation with markup, or a URL.
- **D-8:** no token value in this phase. `updatePeerHost`'s `token` field exists on the wrapper for D4 but no D2 caller passes it. The UI renders only `has_token` booleans.
- **D-6 / §5.2 step 4:** the page never persists or caches a verify result. No Zustand store, no `localStorage`, no module-level cache. State lives in `PeersSection`'s `useState` and dies with it.
- **D-4 vs `return-unknown`:** `not-app-host` (permanent — no App host *is* this peer) and `counterpart-unavailable` (transient — the App has it but could not ask now) are distinct inputs and distinct outputs (`outbound-only` vs `return-unknown`). Never merge them.
- The three names (§2.1) are always labelled in the UI: peer alias (X's config), App host name (`useHostStore.hosts[id].name`), self alias (the daemon's own `peers.alias`).
- No pairing / unpairing / rotation / `allow_bypass` / `deliver` controls (§5.4, D-10). Do not scaffold them.
- Commit messages: one task = one commit; end each with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.

## Facts the plan relies on (measured in this worktree at `3273d803`)

- `GET /api/peers/hosts` → `{"hosts": hostRow[]}`; `hostRow = {alias, url, host_id, verified, has_token, has_inbound_token, allow_bypass}` (`internal/module/peers/hosts.go:34`).
- `POST /api/peers/hosts/{alias}/verify` → `{alias, host_id, ok, error?, self_alias, daemon_version}`; 404 `{error:"unknown alias"}` (`hosts_verify.go:22–80`). `ok:false` always carries non-empty `error`.
- `PUT /api/peers/hosts/{alias}` body `{token?, allow_bypass?, alias?}` → 200 `hostRow`; 400/404/409 `{error: string}` (`hosts.go:84`, `writeJSONError` at `:90`).
- `GET /api/peers/settings` → `{deliver: bool, alias: string}` (`settings.go:25`).
- `GET /api/info` → `{host_id, tmux_instance, purdex_version, …}` (`internal/core/info_handler.go:67`); SPA type `HostInfo` in `stores/useHostStore.ts:53`.
- Daemon `normalizeHostURL` = `url.Parse` + `TrimRight("/")` (`hosts.go:130–151`); SPA `getDaemonBase(id)` = `` `http://${ip}:${port}` `` (`useHostStore.ts:242`). The existing SPA `normalizeUrl` (`lib/url-utils.ts`) returns `URL.href`, which *adds* a trailing slash — do not reuse it for the join.
- `hostFetch(hostId, path, init)` attaches the admin token (`host-api.ts:140`). `HostApiError(status, statusText)` exists at `host-api.ts:219`.
- Host sub-pages are registered in `spa/src/lib/register-modules/index.tsx:417–428` (ten entries, orders 0–9). Four places pin that count/list: `lib/host-builtin-sections.test.tsx:101` (`toHaveLength(10)`), `:105–111` (ordered list), `:127–150` (wrap map), **`:180–188` (HMR-safe count, `toBe(10)` twice)**; `lib/register-modules.test.ts:77–84,156–162`.
- `lib/host-routes.ts:11` `HOST_SUB_PAGES` is a six-entry legacy constant that is **not** consulted by `isHostSubPage` (it queries the contribution registry) — leave it alone; say so in the PR description so a reviewer does not read it as a missed registration.
- `spa/src/main.tsx:56` wraps the app in `<StrictMode>`: in dev every effect runs mount → cleanup → mount, so the page's load runs twice on first paint. The generation counter in Task 4 makes the first run's emits inert; Task 4's tests render under `StrictMode` once to prove it.
- `HostRuntime.status ∈ 'connected' | 'disconnected' | 'reconnecting' | 'auth-error'` (`useHostStore.ts:39`).
- i18n: flat keys in `spa/src/locales/en.json` + `zh-TW.json`; `t(key, params?)` interpolates `{{name}}`; `locale-completeness.test.ts` fails the build on any key missing from one side. Existing `peer.*` namespace is the tab panel; this page uses `peers.*` (`'peers.x'.startsWith('peer.')` is false, so the tab-panel namespace test is unaffected).
- Colour tokens: `text-status-success`, `text-status-warning`, `text-status-error`, `text-text-muted` (`spa/src/styles/themes.css:23–25`).

## File map

| file | responsibility in this phase |
|---|---|
| `spa/src/lib/host-api.ts` | wire types `PeerHostRow`, `PeerHostVerify`, `PeerSettings`; wrappers `listPeerHosts`, `verifyPeerHost`, `updatePeerHost`, `fetchPeerSettings`, `fetchHostInfo`; `HostApiError.detail` |
| `spa/src/lib/host-api.peers.test.ts` (new) | wrapper tests (fetch mocked) |
| `spa/src/lib/peer-pairing.ts` (new) | pure rules: `normalizePeerUrl`, `matchCounterpart`, `pairStatus`, `aliasDrift`; the row/side types |
| `spa/src/lib/peer-pairing.test.ts` (new) | table-driven tests for every row of the §5.1 table |
| `spa/src/lib/peer-pairing-load.ts` (new) | §5.2 orchestration `loadPairings` over an injected `PairingApi`, emitting snapshots |
| `spa/src/lib/peer-pairing-load.test.ts` (new) | orchestration tests with a fake API (call counts, memoisation, unavailable causes, partial failure) |
| `spa/src/components/hosts/PeersSection.tsx` (new) | the page: banner / rows / Refresh / Rename; reads `useHostStore`, calls `loadPairings` with the real wrappers |
| `spa/src/components/hosts/PeersSection.test.tsx` (new) | §8.2 component tests with `host-api` mocked |
| `spa/src/lib/register-modules/index.tsx` | `{ localId: 'peers', labelKey: 'hosts.peers', order: 10, component: PeersSection }` |
| `spa/src/lib/host-builtin-sections.test.tsx`, `spa/src/lib/register-modules.test.ts` | count 10 → 11, list gains `'peers'`, label test |
| `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json` | `hosts.peers` + `peers.*` keys |
| `docs/plans/2026-09-18-peer-pairing-d2-mutations.md` (new) | mutation record (§8.2 deliverable) |

---

### Task 1: `host-api.ts` — typed wrappers for the peer-host routes

**Files:**
- Modify: `spa/src/lib/host-api.ts` (types after `PeersEnvelope` ~line 128; `HostApiError` at ~221; wrappers after `fetchPeers` ~276)
- Test: `spa/src/lib/host-api.peers.test.ts` (new)

**Interfaces:**
- Produces (used by Tasks 3, 4):

```ts
export interface PeerHostRow {
  alias: string; url: string; host_id: string
  verified: boolean; has_token: boolean; has_inbound_token: boolean; allow_bypass: boolean
}
export interface PeerHostVerify {
  alias: string; host_id: string; ok: boolean; error?: string
  self_alias: string; daemon_version: string
}
export interface PeerSettings { deliver: boolean; alias: string }
export class HostApiError extends Error { status: number; detail: string }   // detail = daemon's {error} or statusText
export function listPeerHosts(hostId: string): Promise<PeerHostRow[]>
export function verifyPeerHost(hostId: string, alias: string): Promise<PeerHostVerify>
export function updatePeerHost(hostId: string, alias: string,
  patch: { alias?: string; token?: string; allow_bypass?: boolean }): Promise<PeerHostRow>
export function fetchPeerSettings(hostId: string): Promise<PeerSettings>
export function fetchHostInfo(hostId: string): Promise<HostInfo>
```

- [ ] **Step 1: Write the failing tests**

Create `spa/src/lib/host-api.peers.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import {
  HostApiError, listPeerHosts, verifyPeerHost, updatePeerHost, fetchPeerSettings, fetchHostInfo,
} from './host-api'

const H = 'hx'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status, statusText: status === 200 ? 'OK' : 'ERR',
    headers: { 'Content-Type': 'application/json' },
  })
}

const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>()

beforeEach(() => {
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0, token: 'adm' } },
    hostOrder: [H],
    runtime: { [H]: { status: 'connected' } },
  })
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => vi.unstubAllGlobals())

const ROW = { alias: 'air', url: 'http://100.64.0.4:7860', host_id: 'wakes-air-2026:oa6drb',
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true }

describe('peer-host wrappers', () => {
  it('listPeerHosts unwraps {hosts} and hits GET /api/peers/hosts with the admin token', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { hosts: [ROW] }))
    const rows = await listPeerHosts(H)
    expect(rows).toEqual([ROW])
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/peers/hosts')
    expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer adm')
    expect(init?.method ?? 'GET').toBe('GET')
  })

  it('listPeerHosts throws HostApiError with the daemon message on a non-2xx', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(403, { error: 'admin required' }))
    await expect(listPeerHosts(H)).rejects.toMatchObject({ name: 'HostApiError', status: 403, detail: 'admin required' })
  })

  it('verifyPeerHost POSTs to /api/peers/hosts/<encoded alias>/verify and returns the body verbatim', async () => {
    const body = { alias: 'air', host_id: 'wakes-air-2026:oa6drb', ok: true, self_alias: 'air26', daemon_version: '1.0.0-alpha.378' }
    fetchMock.mockResolvedValueOnce(jsonResponse(200, body))
    await expect(verifyPeerHost(H, 'a b/c')).resolves.toEqual(body)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/peers/hosts/a%20b%2Fc/verify')
    expect(init?.method).toBe('POST')
  })

  it('verifyPeerHost 404 (unknown alias) is a HostApiError, not an ok:false', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(404, { error: 'unknown alias' }))
    await expect(verifyPeerHost(H, 'ghost')).rejects.toMatchObject({ status: 404, detail: 'unknown alias' })
  })

  it('updatePeerHost PUTs only the fields given and returns the row', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ...ROW, alias: 'air26' }))
    const row = await updatePeerHost(H, 'air', { alias: 'air26' })
    expect(row.alias).toBe('air26')
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/peers/hosts/air')
    expect(init?.method).toBe('PUT')
    expect(JSON.parse(String(init?.body))).toEqual({ alias: 'air26' })
    expect(new Headers(init?.headers).get('Content-Type')).toBe('application/json')
  })

  it('updatePeerHost surfaces a 409 with the daemon text in detail', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(409, { error: 'alias "air26" is already used by another host' }))
    const err = await updatePeerHost(H, 'air', { alias: 'air26' }).catch((e) => e)
    expect(err).toBeInstanceOf(HostApiError)
    expect(err.status).toBe(409)
    expect(err.detail).toBe('alias "air26" is already used by another host')
  })

  it('a non-JSON error body falls back to statusText in detail', async () => {
    fetchMock.mockResolvedValueOnce(new Response('<html>', { status: 502, statusText: 'Bad Gateway' }))
    await expect(fetchPeerSettings(H)).rejects.toMatchObject({ status: 502, detail: 'Bad Gateway' })
  })

  it('fetchPeerSettings and fetchHostInfo return their bodies', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { deliver: true, alias: 'mini-lab' }))
    await expect(fetchPeerSettings(H)).resolves.toEqual({ deliver: true, alias: 'mini-lab' })
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { host_id: 'mini-lab:278cbm', tmux_instance: '1:2', purdex_version: 'x', tmux_version: 'y', os: 'darwin', arch: 'arm64' }))
    await expect(fetchHostInfo(H)).resolves.toMatchObject({ host_id: 'mini-lab:278cbm' })
    expect(fetchMock.mock.calls[0][0]).toBe('http://100.64.0.2:7860/api/peers/settings')
    expect(fetchMock.mock.calls[1][0]).toBe('http://100.64.0.2:7860/api/info')
  })

  it('fetchHostInfo rejects with HostApiError on a non-2xx (the untyped fetchInfo would have resolved)', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(500, { error: 'boom' }))
    await expect(fetchHostInfo(H)).rejects.toMatchObject({ name: 'HostApiError', status: 500, detail: 'boom' })
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd spa && npx vitest run src/lib/host-api.peers.test.ts`
Expected: FAIL — `listPeerHosts` etc. are not exported (`is not a function`).

- [ ] **Step 3: Implement**

In `spa/src/lib/host-api.ts`:

(a) After the `PeersEnvelope` interface (before `ConfigData`), add:

```ts
/* ─── Peer-host wire types (internal/module/peers/hosts.go hostRow, hosts_verify.go, settings.go) ─── */

/** One `[[peers.hosts]]` entry as `GET /api/peers/hosts` renders it: never a token value. */
export interface PeerHostRow {
  alias: string
  url: string
  host_id: string             // '' when the entry was added without a token and never verified
  verified: boolean
  has_token: boolean          // outbound token present (what we present to them)
  has_inbound_token: boolean  // what they must present to us
  allow_bypass: boolean
}

/**
 * `POST /api/peers/hosts/{alias}/verify` (Phase D spec §4.1). Exactly one
 * `scope=all` fan-out row minus its peer rows, so this and `pdx peers --all`
 * can never disagree. `ok:false` always carries a non-empty `error`.
 * `self_alias` is the peer's own word for itself — display value, unvalidated.
 */
export interface PeerHostVerify {
  alias: string
  host_id: string
  ok: boolean
  error?: string
  self_alias: string
  daemon_version: string
}

/** `GET /api/peers/settings`: this daemon's deliver toggle and its own alias. */
export interface PeerSettings {
  deliver: boolean
  alias: string
}
```

(b) Extend `HostApiError` so callers can show the daemon's own message (the 409 on rename is `alias "x" is already used by another host`, which the page must render verbatim — spec §5.3):

```ts
export class HostApiError extends Error {
  status: number
  /** The daemon's `{error}` text when the body carried one, else `statusText`. */
  detail: string
  constructor(status: number, statusText: string, detail?: string) {
    super(`${status} ${statusText}`)
    this.name = 'HostApiError'
    this.status = status
    this.detail = detail ?? statusText
  }
}
```

(c) After `fetchPeers`, add:

```ts
/* ─── Peer-host API (Phase D) ─── */

/**
 * Reads the daemon's `{error}` body into a HostApiError. Every peer-host route
 * answers errors as `{"error": "<msg>"}` (`writeJSONError`); anything else
 * (a proxy page, an empty body) falls back to the status text.
 */
async function peerHostError(res: Response): Promise<HostApiError> {
  let detail: string | undefined
  try {
    const body = await res.json()
    if (body && typeof body.error === 'string' && body.error) detail = body.error
  } catch { /* not JSON */ }
  return new HostApiError(res.status, res.statusText, detail)
}

async function peerHostJson<T>(res: Response): Promise<T> {
  if (!res.ok) throw await peerHostError(res)
  return res.json() as Promise<T>
}

export async function listPeerHosts(hostId: string): Promise<PeerHostRow[]> {
  const body = await peerHostJson<{ hosts: PeerHostRow[] | null }>(await hostFetch(hostId, '/api/peers/hosts'))
  return body.hosts ?? []
}

/** Live dial, ≤ 3 s on the daemon side; never cached (spec D-6, §5.2 step 4). */
export function verifyPeerHost(hostId: string, alias: string): Promise<PeerHostVerify> {
  return hostFetch(hostId, `/api/peers/hosts/${encodeURIComponent(alias)}/verify`, { method: 'POST' })
    .then(peerHostJson<PeerHostVerify>)
}

/**
 * `PUT /api/peers/hosts/{alias}`: any subset of `{alias, token, allow_bypass}`.
 * D2 passes only `alias` (adopt the peer's self alias = plain rename, spec D-5).
 * `token` is here for D4; a token value must never be held longer than the
 * call that consumes it (spec D-8).
 */
export function updatePeerHost(
  hostId: string, alias: string,
  patch: { alias?: string; token?: string; allow_bypass?: boolean },
): Promise<PeerHostRow> {
  return hostFetch(hostId, `/api/peers/hosts/${encodeURIComponent(alias)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  }).then(peerHostJson<PeerHostRow>)
}

export function fetchPeerSettings(hostId: string): Promise<PeerSettings> {
  return hostFetch(hostId, '/api/peers/settings').then(peerHostJson<PeerSettings>)
}

/** Typed `/api/info` (the untyped `fetchInfo` above stays for its existing callers). */
export function fetchHostInfo(hostId: string): Promise<HostInfo> {
  return fetchInfo(hostId).then(peerHostJson<HostInfo>)
}
```

Add `HostInfo` to the existing import: `import { useHostStore, type HostInfo } from '../stores/useHostStore'`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd spa && npx vitest run src/lib/host-api.peers.test.ts`
Expected: PASS (9 tests). Also run `cd spa && npx vitest run src/lib/host-api` to confirm existing `host-api` tests still pass (the `HostApiError` change is additive).

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/host-api.ts spa/src/lib/host-api.peers.test.ts -m "feat(spa): typed wrappers for the peer-host routes (list/verify/update/settings)

Phase D spec §5.1. HostApiError gains detail so a rename 409 can show the
daemon's own message.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: `peer-pairing.ts` — the three pure rules

**Files:**
- Create: `spa/src/lib/peer-pairing.ts`
- Test: `spa/src/lib/peer-pairing.test.ts`

**Interfaces:**
- Consumes: `PeerHostRow`, `PeerHostVerify` from Task 1.
- Produces (used by Tasks 3, 4):

```ts
export type VerifyOutcome =
  | { ok: true; self_alias: string; daemon_version: string; host_id: string }
  | { ok: false; error: string }
export type Side = VerifyOutcome | 'pending'
export type InboundState = VerifyOutcome | 'pending' | 'no-entry' | 'not-app-host' | 'counterpart-unavailable'
export type PairStatus = 'bidirectional' | 'one-way' | 'outbound-only' | 'return-unknown' | 'unpaired' | 'checking'
export interface CounterpartCandidate { hostId: string; host_id: string; url: string }  // host_id '' = unknown (unavailable host)
export function normalizePeerUrl(raw: string): string
export function matchCounterpart(entry: { host_id: string; url: string }, hosts: CounterpartCandidate[]): CounterpartCandidate | null
export function matchReturnEntry(self: { host_id: string; url: string }, rows: PeerHostRow[]): PeerHostRow | null
export function pairStatus(outbound: Side, inbound: InboundState): PairStatus
export function aliasDrift(alias: string, selfAlias: string): string
export function toOutcome(v: PeerHostVerify): VerifyOutcome
```

**The join rule, stated precisely (D-3 read together with §5.2 step 2).** D-3: identity (`host_id`) first; URL "only for an entry with `host_id: ''`". §5.2 step 2 (folded in from review, §12): an *unavailable* App host — whose `host_id` the page could not learn — "still participates in the join by whatever the App knows locally — its URL", so an entry pointing at it is `counterpart-unavailable`, not `not-app-host`. Put together, exactly two URL fallbacks exist and nothing else:

1. `entry.host_id === ''` → the entry may join **any** candidate by URL (D-3 verbatim).
2. `entry.host_id !== ''` and no candidate carries that host_id → the entry may join by URL **only a candidate whose host_id is unknown** (an unavailable App host). It never joins an *available* candidate whose known host_id differs — that is a different daemon at the same address (a reinstall, a moved port), and joining it would verify the wrong machine.

This does not loosen D-3's identity rule: whenever both sides have a host_id, only the host_id decides. `matchCounterpart` implements the two cases and the tests pin both, plus "same URL, different *known* host_id → null". `matchReturnEntry` is the same rule with the roles swapped (which of *their* rows is *us*), returning the row itself so no caller has to smuggle an alias through a `hostId` field.

- [ ] **Step 1: Write the failing tests**

Create `spa/src/lib/peer-pairing.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import {
  aliasDrift, matchCounterpart, matchReturnEntry, normalizePeerUrl, pairStatus, toOutcome,
  type InboundState, type PairStatus, type Side, type VerifyOutcome,
} from './peer-pairing'
import type { PeerHostRow } from './host-api'

const OK: VerifyOutcome = { ok: true, self_alias: 'air26', daemon_version: '1.0.0-alpha.378', host_id: 'wakes-air-2026:oa6drb' }
const FAIL: VerifyOutcome = { ok: false, error: 'dial tcp: host is down' }

describe('normalizePeerUrl', () => {
  it('matches the daemon: scheme+host, trailing slash trimmed, host lower-cased', () => {
    expect(normalizePeerUrl('http://100.64.0.4:7860')).toBe('http://100.64.0.4:7860')
    expect(normalizePeerUrl('http://100.64.0.4:7860/')).toBe('http://100.64.0.4:7860')
    expect(normalizePeerUrl('HTTP://Air.Local:7860/')).toBe('http://air.local:7860')
    expect(normalizePeerUrl('http://h:7860/base/')).toBe('http://h:7860/base')
  })
  it('returns the trimmed input when it does not parse, so garbage never equals garbage by accident', () => {
    expect(normalizePeerUrl('not a url')).toBe('not a url')
    expect(normalizePeerUrl('')).toBe('')
  })
})

describe('matchCounterpart (spec D-3, §5.2 step 2)', () => {
  const air = { hostId: 'hA', host_id: 'wakes-air-2026:oa6drb', url: 'http://100.64.0.4:7860' }
  const other = { hostId: 'hB', host_id: 'other:111111', url: 'http://100.64.0.9:7860' }
  const unknown = { hostId: 'hU', host_id: '', url: 'http://100.64.0.7:7860' }   // unavailable host

  it('host_id wins even when URLs differ', () => {
    const entry = { host_id: 'wakes-air-2026:oa6drb', url: 'http://air.local:7860' }
    expect(matchCounterpart(entry, [other, air])).toBe(air)
  })
  it('URL is the fallback when the entry has no host_id (normalised, trailing slash ignored)', () => {
    const entry = { host_id: '', url: 'http://100.64.0.4:7860/' }
    expect(matchCounterpart(entry, [other, air])).toBe(air)
  })
  it('URL also joins an entry to a host whose host_id is unknown (unavailable), so it can be "could not be asked"', () => {
    const entry = { host_id: 'somebody:abcdef', url: 'http://100.64.0.7:7860' }
    expect(matchCounterpart(entry, [air, unknown])).toBe(unknown)
  })
  it('same URL but a different KNOWN host_id is not a match — that is another daemon at that address', () => {
    const entry = { host_id: 'reinstalled:zzzzzz', url: 'http://100.64.0.4:7860' }
    expect(matchCounterpart(entry, [air])).toBeNull()
  })
  it('an entry with neither a host_id match nor a URL match is null', () => {
    expect(matchCounterpart({ host_id: 'x:1', url: 'http://1.2.3.4:1' }, [air, other, unknown])).toBeNull()
  })
  it('an entry and a candidate that are both host_id-less match only by URL, never by "" === ""', () => {
    const entry = { host_id: '', url: 'http://9.9.9.9:1' }
    expect(matchCounterpart(entry, [unknown])).toBeNull()
  })
})

describe('matchReturnEntry — the same rule with the roles swapped', () => {
  const row = (p: Partial<PeerHostRow>): PeerHostRow => ({
    alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm',
    verified: true, has_token: true, has_inbound_token: true, allow_bypass: true, ...p,
  })
  const self = { host_id: 'mini-lab:278cbm', url: 'http://100.64.0.2:7860' }

  it('returns the row whose host_id is ours, whatever it is named', () => {
    const r = row({ alias: 'mlab' })
    expect(matchReturnEntry(self, [row({ alias: 'x', host_id: 'other:1' }), r])).toBe(r)
  })
  it('falls back to URL only for a row with no host_id', () => {
    const r = row({ alias: 'by-url', host_id: '', url: 'http://100.64.0.2:7860/' })
    expect(matchReturnEntry(self, [r])).toBe(r)
    expect(matchReturnEntry(self, [row({ host_id: 'stranger:1' })])).toBeNull()
  })
  it('returns null on an empty list', () => expect(matchReturnEntry(self, [])).toBeNull())
})

describe('pairStatus — every row of the §5.1 table', () => {
  const cases: Array<[string, Side, InboundState, PairStatus]> = [
    ['ok / ok', OK, OK, 'bidirectional'],
    ['ok / failed', OK, FAIL, 'one-way'],
    ['ok / no-entry', OK, 'no-entry', 'one-way'],
    ['ok / not-app-host', OK, 'not-app-host', 'outbound-only'],
    ['ok / counterpart-unavailable', OK, 'counterpart-unavailable', 'return-unknown'],
    ['failed / ok', FAIL, OK, 'one-way'],
    ['failed / failed', FAIL, FAIL, 'unpaired'],
    ['failed / no-entry', FAIL, 'no-entry', 'unpaired'],
    ['failed / not-app-host', FAIL, 'not-app-host', 'unpaired'],
    ['failed / counterpart-unavailable', FAIL, 'counterpart-unavailable', 'unpaired'],
    ['pending / ok', 'pending', OK, 'checking'],
    ['ok / pending', OK, 'pending', 'checking'],
    ['pending / not-app-host', 'pending', 'not-app-host', 'checking'],
    ['pending / pending', 'pending', 'pending', 'checking'],
  ]
  it.each(cases)('%s → %s', (_name, outbound, inbound, want) => {
    expect(pairStatus(outbound, inbound)).toBe(want)
  })
  it('outbound-only and return-unknown are never the same word (D-4 vs transient)', () => {
    expect(pairStatus(OK, 'not-app-host')).not.toBe(pairStatus(OK, 'counterpart-unavailable'))
  })
})

describe('aliasDrift (same rule as cmd/pdx aliasDriftField)', () => {
  it('returns the self alias when it differs', () => expect(aliasDrift('air', 'air26')).toBe('air26'))
  it('empty self alias is not drift', () => expect(aliasDrift('air', '')).toBe(''))
  it('case-insensitive equal is not drift', () => expect(aliasDrift('Air', 'aIR')).toBe(''))
  it('whitespace is not trimmed away — the daemon bounded it, we compare what it said', () => {
    expect(aliasDrift('air', 'air ')).toBe('air ')
  })
})

describe('toOutcome', () => {
  it('maps ok:true to the success shape and ok:false to {error}', () => {
    expect(toOutcome({ alias: 'air', host_id: 'h:1', ok: true, self_alias: 'air26', daemon_version: 'v' }))
      .toEqual({ ok: true, self_alias: 'air26', daemon_version: 'v', host_id: 'h:1' })
    expect(toOutcome({ alias: 'air', host_id: 'h:1', ok: false, error: 'no outbound token', self_alias: '', daemon_version: '' }))
      .toEqual({ ok: false, error: 'no outbound token' })
  })
  it('an ok:false with no error text still names a cause (belt and braces over the daemon guarantee)', () => {
    expect(toOutcome({ alias: 'a', host_id: '', ok: false, self_alias: '', daemon_version: '' }))
      .toEqual({ ok: false, error: 'peer reported ok=false' })
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd spa && npx vitest run src/lib/peer-pairing.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

Create `spa/src/lib/peer-pairing.ts`:

```ts
// spa/src/lib/peer-pairing.ts — the pure rules of the Peers page (Phase D spec §5.1).
// No React, no store, no fetch: everything here is a function of its arguments.
import type { PeerHostRow, PeerHostVerify } from './host-api'

/** One direction's live verify, or not yet answered. */
export type VerifyOutcome =
  | { ok: true; self_alias: string; daemon_version: string; host_id: string }
  | { ok: false; error: string }
export type Side = VerifyOutcome | 'pending'

/**
 * The return direction has three non-verify states that are deliberately
 * distinct (spec §5.1): `no-entry` = the counterpart was asked and has no
 * entry for us; `not-app-host` = no App host IS this peer (a permanent fact
 * about the App, spec D-4); `counterpart-unavailable` = the App has that
 * host but could not ask it this time (a transient fact).
 */
export type InboundState = VerifyOutcome | 'pending' | 'no-entry' | 'not-app-host' | 'counterpart-unavailable'

export type PairStatus = 'bidirectional' | 'one-way' | 'outbound-only' | 'return-unknown' | 'unpaired' | 'checking'

/** An App host as a join candidate. `host_id` is '' when the page could not learn it. */
export interface CounterpartCandidate {
  hostId: string
  host_id: string
  url: string
}

/**
 * The daemon's `normalizeHostURL` (`internal/module/peers/hosts.go`): parse,
 * keep scheme+host+path, trim trailing slashes. `URL.host` lower-cases the
 * hostname and drops a default port, which the daemon's `url.String()` does
 * not — irrelevant here because both sides of the join go through THIS
 * function, and a default-port peer URL does not occur (the daemon listens
 * on 7860). Unparseable input is returned trimmed so two garbage strings
 * still compare by their own text and never collapse to a shared value.
 */
export function normalizePeerUrl(raw: string): string {
  const s = raw.trim()
  try {
    const u = new URL(s)
    const path = u.pathname.replace(/\/+$/, '')
    return `${u.protocol}//${u.host}${path}`
  } catch {
    return s
  }
}

/**
 * Spec D-3 read with §5.2 step 2. Identity (`host_id`) decides whenever
 * both sides have one. Exactly two URL fallbacks exist:
 *  1. the entry has no host_id (added without a token) → any candidate by URL;
 *  2. the entry has one that no candidate carries → only a candidate whose
 *     host_id is UNKNOWN (an App host the page could not ask), which is what
 *     makes such an entry `counterpart-unavailable` instead of `not-app-host`.
 * A candidate whose KNOWN host_id differs is never joined by URL: that is a
 * different daemon at the same address.
 */
export function matchCounterpart(
  entry: { host_id: string; url: string },
  hosts: CounterpartCandidate[],
): CounterpartCandidate | null {
  if (entry.host_id !== '') {
    const byId = hosts.find((h) => h.host_id !== '' && h.host_id === entry.host_id)
    if (byId) return byId
  }
  const url = normalizePeerUrl(entry.url)
  if (url === '') return null
  return hosts.find((h) =>
    (entry.host_id === '' || h.host_id === '') && normalizePeerUrl(h.url) === url,
  ) ?? null
}

/**
 * Which of THEIR entries is US: `matchCounterpart` with the roles swapped,
 * over the counterpart's `GET /api/peers/hosts` rows. Returns the row so
 * callers never have to carry an alias through a `hostId` field.
 */
export function matchReturnEntry(
  self: { host_id: string; url: string },
  rows: PeerHostRow[],
): PeerHostRow | null {
  const hit = matchCounterpart(self, rows.map((r) => ({ hostId: r.alias, host_id: r.host_id, url: r.url })))
  return hit ? rows.find((r) => r.alias === hit.hostId) ?? null : null
}

/** The §5.1 status table, row for row. */
export function pairStatus(outbound: Side, inbound: InboundState): PairStatus {
  if (outbound === 'pending' || inbound === 'pending') return 'checking'
  if (outbound.ok) {
    if (inbound === 'not-app-host') return 'outbound-only'
    if (inbound === 'counterpart-unavailable') return 'return-unknown'
    if (inbound === 'no-entry') return 'one-way'
    return inbound.ok ? 'bidirectional' : 'one-way'
  }
  if (typeof inbound === 'object' && inbound.ok) return 'one-way'
  return 'unpaired'
}

/**
 * The same rule as `aliasDriftField` in `cmd/pdx/peers.go`: the peer's self
 * alias when it is non-empty and not case-insensitively equal to what we
 * call it, else ''. No trimming — the daemon bounded the value for display
 * and this compares what it said.
 */
export function aliasDrift(alias: string, selfAlias: string): string {
  if (selfAlias === '') return ''
  if (selfAlias.toLowerCase() === alias.toLowerCase()) return ''
  return selfAlias
}

/** Collapses the wire verify into the two-shape outcome the rules take. */
export function toOutcome(v: PeerHostVerify): VerifyOutcome {
  if (v.ok) return { ok: true, self_alias: v.self_alias, daemon_version: v.daemon_version, host_id: v.host_id }
  return { ok: false, error: v.error || 'peer reported ok=false' }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd spa && npx vitest run src/lib/peer-pairing.test.ts`
Expected: PASS (all table rows).

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/peer-pairing.ts spa/src/lib/peer-pairing.test.ts -m "feat(spa): peer-pairing rules — counterpart join, pair status table, alias drift

Phase D spec §5.1. Pure functions; the status table is tested row for row.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: `peer-pairing-load.ts` — the §5.2 orchestration

**Files:**
- Create: `spa/src/lib/peer-pairing-load.ts`
- Test: `spa/src/lib/peer-pairing-load.test.ts`

**Interfaces:**
- Consumes: Task 1 types; Task 2 `matchCounterpart`, `matchReturnEntry`, `toOutcome`, `Side`, `InboundState`.
- Produces (used by Task 4):

```ts
export interface PairingApi {
  info: (hostId: string) => Promise<{ host_id: string }>
  settings: (hostId: string) => Promise<PeerSettings>
  list: (hostId: string) => Promise<PeerHostRow[]>
  verify: (hostId: string, alias: string) => Promise<PeerHostVerify>
}
export interface PairingAppHost { hostId: string; name: string; url: string; status: HostRuntime['status'] | undefined }
export interface PairingRow {
  entry: PeerHostRow
  counterpart: { hostId: string; name: string } | null   // the App host that IS this peer, when joined
  counterpartCause: string                                // non-empty only when joined but unavailable
  returnEntry: PeerHostRow | null                         // E' on the counterpart, when asked and present
  outbound: Side
  inbound: InboundState
}
export interface PairingSnapshot {
  self: { host_id: string; self_alias: string } | null
  error: { call: string; message: string } | null          // page-level banner (§5.2 step 0)
  rows: PairingRow[]
}
export function loadPairings(x: PairingAppHost, others: PairingAppHost[], api: PairingApi,
  emit: (s: PairingSnapshot) => void): Promise<PairingSnapshot>
```

Behaviour (spec §5.2, numbered as there):

0. `info(X)`, `settings(X)`, `list(X)` run in parallel. If any rejects → emit + return `{ self: null, error: { call: 'info'|'settings'|'list', message }, rows: [] }`. No rows, no other calls.
2. For every other host with `status === 'connected'`: `info(H)` + `settings(H)` in parallel; a rejection makes H unavailable with cause `` `${call}: ${message}` ``. A host not connected is unavailable with cause = its status (`'disconnected'`, `'reconnecting'`, `'auth-error'`) or `'unknown'` when undefined.
3. Join each entry with `matchCounterpart(entry, candidates)` where a candidate's `host_id` is `''` for unavailable hosts. For each *distinct* available counterpart Y that at least one entry joined to: `list(Y)` **once** (memoised in a `Map<hostId, Promise>`); a rejection makes every entry joined to Y `counterpart-unavailable` with cause `` `list: ${message}` ``. The return entry E' is `matchReturnEntry({host_id: self.host_id, url: x.url}, rowsOfY)` (Task 2) — the same join rule with the roles swapped.
4. Emit the snapshot with every verifiable side `'pending'`, then start all verifies in parallel: `verify(X, entry.alias)` for every entry; `verify(Y, E'.alias)` for every entry with a return entry. As each settles, patch that row's side (`toOutcome` on success; on rejection `{ ok: false, error: message }` — a thrown 404 means the entry vanished between list and verify, which is a failure of that direction, not a page error) and emit the whole snapshot again. Resolve with the final snapshot after all settle.

The emit-after-each-settle contract is what lets the page paint `checking` rows immediately and fill them in as the ≤ 3 s dials return, without the loader knowing anything about React.

- [ ] **Step 1: Write the failing tests**

Create `spa/src/lib/peer-pairing-load.test.ts`:

```ts
import { describe, it, expect, vi } from 'vitest'
import { loadPairings, type PairingApi, type PairingAppHost, type PairingSnapshot } from './peer-pairing-load'
import type { PeerHostRow, PeerHostVerify } from './host-api'
import { pairStatus } from './peer-pairing'

const X: PairingAppHost = { hostId: 'hM', name: 'mlab', url: 'http://100.64.0.2:7860', status: 'connected' }
const AIR: PairingAppHost = { hostId: 'hA', name: 'Air 2026', url: 'http://100.64.0.4:7860', status: 'connected' }
const OTHER: PairingAppHost = { hostId: 'hO', name: 'Other', url: 'http://100.64.0.9:7860', status: 'connected' }

const row = (p: Partial<PeerHostRow>): PeerHostRow => ({
  alias: 'air', url: 'http://100.64.0.4:7860', host_id: 'wakes-air-2026:oa6drb',
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true, ...p,
})
const ok = (alias: string, self_alias: string, host_id: string): PeerHostVerify =>
  ({ alias, host_id, ok: true, self_alias, daemon_version: '1.0.0-alpha.378' })

/** A fake API keyed by hostId; every method is a vi.fn so call counts are assertable. */
function fakeApi(spec: {
  info?: Record<string, string | Error>
  settings?: Record<string, string | Error>
  list?: Record<string, PeerHostRow[] | Error>
  verify?: Record<string, PeerHostVerify | Error>     // key `${hostId}/${alias}`
}): PairingApi {
  const pick = <T,>(m: Record<string, T | Error> | undefined, k: string): Promise<T> => {
    const v = m?.[k]
    if (v === undefined) return Promise.reject(new Error(`unexpected ${k}`))
    return v instanceof Error ? Promise.reject(v) : Promise.resolve(v)
  }
  return {
    info: vi.fn((h) => pick(spec.info, h).then((host_id) => ({ host_id }))),
    settings: vi.fn((h) => pick(spec.settings, h).then((alias) => ({ deliver: true, alias }))),
    list: vi.fn((h) => pick(spec.list, h)),
    verify: vi.fn((h, a) => pick(spec.verify, `${h}/${a}`)),
  }
}

const collect = () => { const snaps: PairingSnapshot[] = []; return { snaps, emit: (s: PairingSnapshot) => snaps.push(structuredClone(s)) } }

describe('loadPairings — the §2.1 fixture (mlab ↔ air, drift)', () => {
  const api = () => fakeApi({
    info: { hM: 'mini-lab:278cbm', hA: 'wakes-air-2026:oa6drb' },
    settings: { hM: 'mini-lab', hA: 'air26' },
    list: {
      hM: [row({})],
      hA: [row({ alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm' })],
    },
    verify: {
      'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb'),
      'hA/mini-lab': ok('mini-lab', 'mini-lab', 'mini-lab:278cbm'),
    },
  })

  it('joins by host_id, finds the return entry, verifies both directions, ends bidirectional', async () => {
    const { snaps, emit } = collect()
    const final = await loadPairings(X, [AIR], api(), emit)
    expect(final.error).toBeNull()
    expect(final.self).toEqual({ host_id: 'mini-lab:278cbm', self_alias: 'mini-lab' })
    expect(final.rows).toHaveLength(1)
    const r = final.rows[0]
    expect(r.counterpart).toEqual({ hostId: 'hA', name: 'Air 2026' })
    expect(r.counterpartCause).toBe('')
    expect(r.returnEntry?.alias).toBe('mini-lab')
    expect(r.outbound).toEqual({ ok: true, self_alias: 'air26', daemon_version: '1.0.0-alpha.378', host_id: 'wakes-air-2026:oa6drb' })
    expect(r.inbound).toEqual({ ok: true, self_alias: 'mini-lab', daemon_version: '1.0.0-alpha.378', host_id: 'mini-lab:278cbm' })
    expect(pairStatus(r.outbound, r.inbound)).toBe('bidirectional')
    // first emit: rows present, both sides pending → 'checking'
    expect(snaps[0].rows[0].outbound).toBe('pending')
    expect(snaps[0].rows[0].inbound).toBe('pending')
    expect(pairStatus(snaps[0].rows[0].outbound, snaps[0].rows[0].inbound)).toBe('checking')
    // one emit per settled verify after the first
    expect(snaps).toHaveLength(3)
  })

  it('calls verify exactly once per direction and list exactly once per host', async () => {
    const a = api()
    await loadPairings(X, [AIR], a, () => {})
    expect(a.verify).toHaveBeenCalledTimes(2)
    expect(a.verify).toHaveBeenCalledWith('hM', 'air')
    expect(a.verify).toHaveBeenCalledWith('hA', 'mini-lab')
    expect(a.list).toHaveBeenCalledTimes(2)
    expect(a.info).toHaveBeenCalledTimes(2)
    expect(a.settings).toHaveBeenCalledTimes(2)
  })
})

describe('loadPairings — page-level preconditions (§5.2 step 0)', () => {
  it('a failing settings(X) yields the banner, no rows, and no calls to any other host', async () => {
    const a = fakeApi({ info: { hM: 'mini-lab:278cbm' }, settings: { hM: new Error('HTTP 500') }, list: { hM: [row({})] } })
    const { snaps, emit } = collect()
    const final = await loadPairings(X, [AIR], a, emit)
    expect(final).toEqual({ self: null, error: { call: 'settings', message: 'HTTP 500' }, rows: [] })
    expect(snaps).toEqual([final])
    expect(a.info).not.toHaveBeenCalledWith('hA')
    expect(a.verify).not.toHaveBeenCalled()
  })
  it('a failing list(X) names "list"', async () => {
    const a = fakeApi({ info: { hM: 'm:1' }, settings: { hM: 'm' }, list: { hM: new Error('403 admin required') } })
    const final = await loadPairings(X, [], a, () => {})
    expect(final.error).toEqual({ call: 'list', message: '403 admin required' })
  })
  it('an empty entry list is not an error', async () => {
    const a = fakeApi({ info: { hM: 'm:1' }, settings: { hM: 'm' }, list: { hM: [] } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.error).toBeNull()
    expect(final.rows).toEqual([])
    expect(a.verify).not.toHaveBeenCalled()
  })
})

describe('loadPairings — the return side (§5.1 inbound states)', () => {
  const base = { info: { hM: 'mini-lab:278cbm', hA: 'wakes-air-2026:oa6drb' }, settings: { hM: 'mini-lab', hA: 'air26' } }

  it('no App host is the peer → not-app-host, and only the outbound verify runs', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({ host_id: 'stranger:aaaaaa', url: 'http://10.0.0.1:7860' })] },
      verify: { 'hM/air': ok('air', 'stranger', 'stranger:aaaaaa') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.rows[0].counterpart).toBeNull()
    expect(final.rows[0].inbound).toBe('not-app-host')
    expect(a.verify).toHaveBeenCalledTimes(1)
    expect(pairStatus(final.rows[0].outbound, final.rows[0].inbound)).toBe('outbound-only')
  })

  it('the counterpart has no entry for X → no-entry', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})], hA: [] }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.rows[0].counterpart?.hostId).toBe('hA')
    expect(final.rows[0].returnEntry).toBeNull()
    expect(final.rows[0].inbound).toBe('no-entry')
    expect(a.verify).toHaveBeenCalledTimes(1)
  })

  it('a disconnected App host still joins by URL and is counterpart-unavailable with its status as cause', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})] }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [{ ...AIR, status: 'disconnected' }], a, () => {})
    const r = final.rows[0]
    expect(r.counterpart).toEqual({ hostId: 'hA', name: 'Air 2026' })
    expect(r.counterpartCause).toBe('disconnected')
    expect(r.inbound).toBe('counterpart-unavailable')
    expect(a.info).not.toHaveBeenCalledWith('hA')
    expect(pairStatus(r.outbound, r.inbound)).toBe('return-unknown')
  })

  it('an auth-error host is unavailable with cause auth-error, never not-app-host', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})] }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [{ ...AIR, status: 'auth-error' }], a, () => {})
    expect(final.rows[0].counterpartCause).toBe('auth-error')
    expect(final.rows[0].inbound).toBe('counterpart-unavailable')
  })

  it('a connected host whose info() fails is unavailable with "info: <message>"', async () => {
    const a = fakeApi({ info: { hM: 'mini-lab:278cbm', hA: new Error('HTTP 502') }, settings: { hM: 'mini-lab', hA: 'air26' },
      list: { hM: [row({})] }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.rows[0].counterpartCause).toBe('info: HTTP 502')
    expect(final.rows[0].inbound).toBe('counterpart-unavailable')
  })

  it('a failing list(Y) makes every entry joined to Y unavailable with "list: <message>"', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})], hA: new Error('HTTP 500') }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.rows[0].counterpart?.hostId).toBe('hA')
    expect(final.rows[0].counterpartCause).toBe('list: HTTP 500')
    expect(final.rows[0].inbound).toBe('counterpart-unavailable')
  })

  it('two entries pointing at the same daemon share ONE list(Y) call', async () => {
    const a = fakeApi({ ...base,
      list: { hM: [row({}), row({ alias: 'air-again', url: 'http://air.local:7860' })],
              hA: [row({ alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm' })] },
      verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb'), 'hM/air-again': ok('air-again', 'air26', 'wakes-air-2026:oa6drb'),
                'hA/mini-lab': ok('mini-lab', 'mini-lab', 'mini-lab:278cbm') } })
    const final = await loadPairings(X, [AIR, OTHER], a, () => {})
    expect(a.list).toHaveBeenCalledTimes(2)              // hM once, hA once; OTHER is nobody's counterpart → never listed
    expect(a.list).not.toHaveBeenCalledWith('hO')
    expect(final.rows.map((r) => r.returnEntry?.alias)).toEqual(['mini-lab', 'mini-lab'])
  })

  it('a rejected verify (e.g. 404 because the entry vanished) is that direction failing, not a page error', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})], hA: [row({ alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm' })] },
      verify: { 'hM/air': new Error('404 unknown alias'), 'hA/mini-lab': ok('mini-lab', 'mini-lab', 'mini-lab:278cbm') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.error).toBeNull()
    expect(final.rows[0].outbound).toEqual({ ok: false, error: '404 unknown alias' })
    expect(pairStatus(final.rows[0].outbound, final.rows[0].inbound)).toBe('one-way')
  })

  it('the return entry is found by URL when the counterpart row has no host_id', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})], hA: [row({ alias: 'mlab-by-url', url: 'http://100.64.0.2:7860/', host_id: '' })] },
      verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb'), 'hA/mlab-by-url': ok('mlab-by-url', 'mini-lab', 'mini-lab:278cbm') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.rows[0].returnEntry?.alias).toBe('mlab-by-url')
    expect(a.verify).toHaveBeenCalledWith('hA', 'mlab-by-url')
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd spa && npx vitest run src/lib/peer-pairing-load.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

Create `spa/src/lib/peer-pairing-load.ts`:

```ts
// spa/src/lib/peer-pairing-load.ts — what the Peers page does on mount and on
// Refresh (Phase D spec §5.2), as one async function over an injected API so
// it is testable without React and the component only renders snapshots.
//
// Cost per run with n entries on X and m connected other hosts: 3 calls on X,
// ≤ 2 per other connected host, 1 list per host that is somebody's
// counterpart, and ≤ 2n verifies — all in parallel; nothing cached across
// runs (spec D-6: a stale green is the thing this page exists to remove).
import type { PeerHostRow, PeerHostVerify, PeerSettings } from './host-api'
import type { HostRuntime } from '../stores/useHostStore'
import { matchCounterpart, matchReturnEntry, toOutcome, type CounterpartCandidate, type InboundState, type Side } from './peer-pairing'

export interface PairingApi {
  info: (hostId: string) => Promise<{ host_id: string }>
  settings: (hostId: string) => Promise<PeerSettings>
  list: (hostId: string) => Promise<PeerHostRow[]>
  verify: (hostId: string, alias: string) => Promise<PeerHostVerify>
}

export interface PairingAppHost {
  hostId: string
  name: string
  url: string
  status: HostRuntime['status'] | undefined
}

export interface PairingRow {
  entry: PeerHostRow
  /** The App host that IS this peer, when the join found one (spec D-3). */
  counterpart: { hostId: string; name: string } | null
  /** Why the counterpart could not be asked this run; '' when it could (or there is none). */
  counterpartCause: string
  /** The counterpart's entry for X, when it was asked and has one. */
  returnEntry: PeerHostRow | null
  outbound: Side
  inbound: InboundState
}

export interface PairingSnapshot {
  self: { host_id: string; self_alias: string } | null
  /** Page-level failure of one of X's three precondition calls (spec §5.2 step 0). */
  error: { call: 'info' | 'settings' | 'list'; message: string } | null
  rows: PairingRow[]
}

const msg = (e: unknown) => (e instanceof Error ? e.message : String(e))

type Meta =
  | { hostId: string; name: string; url: string; available: true; host_id: string; self_alias: string }
  | { hostId: string; name: string; url: string; available: false; cause: string }

async function loadMeta(h: PairingAppHost, api: PairingApi): Promise<Meta> {
  const base = { hostId: h.hostId, name: h.name, url: h.url }
  if (h.status !== 'connected') return { ...base, available: false, cause: h.status ?? 'unknown' }
  const [info, settings] = await Promise.allSettled([api.info(h.hostId), api.settings(h.hostId)])
  if (info.status === 'rejected') return { ...base, available: false, cause: `info: ${msg(info.reason)}` }
  if (settings.status === 'rejected') return { ...base, available: false, cause: `settings: ${msg(settings.reason)}` }
  return { ...base, available: true, host_id: info.value.host_id, self_alias: settings.value.alias }
}

export async function loadPairings(
  x: PairingAppHost,
  others: PairingAppHost[],
  api: PairingApi,
  emit: (s: PairingSnapshot) => void,
): Promise<PairingSnapshot> {
  // Step 0–1: X's own metadata and entries are the page's preconditions.
  const [info, settings, list] = await Promise.allSettled([api.info(x.hostId), api.settings(x.hostId), api.list(x.hostId)])
  const failed: Array<['info' | 'settings' | 'list', PromiseSettledResult<unknown>]> =
    [['info', info], ['settings', settings], ['list', list]]
  for (const [call, r] of failed) {
    if (r.status === 'rejected') {
      const snap: PairingSnapshot = { self: null, error: { call, message: msg(r.reason) }, rows: [] }
      emit(snap)
      return snap
    }
  }
  const self = {
    host_id: (info as PromiseFulfilledResult<{ host_id: string }>).value.host_id,
    self_alias: (settings as PromiseFulfilledResult<PeerSettings>).value.alias,
  }
  const entries = (list as PromiseFulfilledResult<PeerHostRow[]>).value

  // Step 2: every other App host's metadata, or the reason it could not be asked.
  const metas = await Promise.all(others.filter((h) => h.hostId !== x.hostId).map((h) => loadMeta(h, api)))
  const candidates: CounterpartCandidate[] = metas.map((m) => ({
    hostId: m.hostId, url: m.url, host_id: m.available ? m.host_id : '',
  }))
  const metaById = new Map(metas.map((m) => [m.hostId, m]))

  // Step 3: join, then list each distinct available counterpart once.
  const listOnce = new Map<string, Promise<PeerHostRow[] | Error>>()
  const listOf = (hostId: string) => {
    let p = listOnce.get(hostId)
    if (!p) { p = api.list(hostId).catch((e: unknown) => (e instanceof Error ? e : new Error(String(e)))); listOnce.set(hostId, p) }
    return p
  }

  const rows: PairingRow[] = await Promise.all(entries.map(async (entry): Promise<PairingRow> => {
    const y = matchCounterpart(entry, candidates)
    if (!y) return { entry, counterpart: null, counterpartCause: '', returnEntry: null, outbound: 'pending', inbound: 'not-app-host' }
    const meta = metaById.get(y.hostId)!
    const counterpart = { hostId: meta.hostId, name: meta.name }
    if (!meta.available) {
      return { entry, counterpart, counterpartCause: meta.cause, returnEntry: null, outbound: 'pending', inbound: 'counterpart-unavailable' }
    }
    const theirs = await listOf(y.hostId)
    if (theirs instanceof Error) {
      return { entry, counterpart, counterpartCause: `list: ${theirs.message}`, returnEntry: null, outbound: 'pending', inbound: 'counterpart-unavailable' }
    }
    // The same join rule with the roles swapped: which of THEIR entries is us?
    const returnEntry = matchReturnEntry({ host_id: self.host_id, url: x.url }, theirs)
    return { entry, counterpart, counterpartCause: '', returnEntry, outbound: 'pending', inbound: returnEntry ? 'pending' : 'no-entry' }
  }))

  const snap: PairingSnapshot = { self, error: null, rows }
  emit(snap)

  // Step 4: every direction that has an entry, verified in parallel; one emit per settle.
  const settle = (i: number, side: 'outbound' | 'inbound', p: Promise<PeerHostVerify>) =>
    p.then(toOutcome, (e: unknown) => ({ ok: false as const, error: msg(e) }))
      .then((outcome) => {
        snap.rows = snap.rows.map((r, j) => (j === i ? { ...r, [side]: outcome } : r))
        emit({ ...snap })
      })

  const dials: Promise<void>[] = []
  rows.forEach((r, i) => {
    dials.push(settle(i, 'outbound', api.verify(x.hostId, r.entry.alias)))
    if (r.counterpart && r.returnEntry) dials.push(settle(i, 'inbound', api.verify(r.counterpart.hostId, r.returnEntry.alias)))
  })
  await Promise.all(dials)
  return { ...snap }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd spa && npx vitest run src/lib/peer-pairing-load.test.ts`
Expected: PASS. If the "one emit per settled verify" count is off by one, check that the initial emit happens exactly once before any dial and that each `settle` emits exactly once — do not "fix" it by emitting only at the end; the page depends on the incremental paints.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/peer-pairing-load.ts spa/src/lib/peer-pairing-load.test.ts -m "feat(spa): loadPairings — metadata, join, memoised counterpart list, parallel verifies

Phase D spec §5.2 as one function over an injected API, emitting a snapshot
after the join and after every verify settles.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: `PeersSection.tsx` — the page, with i18n

**Files:**
- Create: `spa/src/components/hosts/PeersSection.tsx`
- Modify: `spa/src/locales/en.json` (after the `hosts.snapshots.*` block ~line 1157), `spa/src/locales/zh-TW.json` (same place)
- Test: `spa/src/components/hosts/PeersSection.test.tsx`

**Interfaces:**
- Consumes: Task 1 wrappers (`listPeerHosts`, `verifyPeerHost`, `updatePeerHost`, `fetchPeerSettings`, `fetchHostInfo`, `HostApiError`); Task 2 `pairStatus`, `aliasDrift`; Task 3 `loadPairings`, `PairingSnapshot`, `PairingRow`.
- Produces: `export function PeersSection({ hostId }: { hostId: string })` for Task 5.

**i18n keys** (en / zh-TW; add all of them to both files, in this order, right after `hosts.snapshots.client_no_dev`):

| key | en | zh-TW |
|---|---|---|
| `hosts.peers` | `Peers` | `對等主機` |
| `peers.desc` | `Daemon-to-daemon pairings configured on this host. Both directions are verified live on every refresh; nothing is cached.` | `此主機設定的 daemon 對 daemon 配對。每次重新整理都會即時驗證兩個方向，不做快取。` |
| `peers.refresh` | `Refresh` | `重新整理` |
| `peers.checking` | `Checking…` | `檢查中…` |
| `peers.empty` | `No peers are configured on this host.` | `此主機尚未設定任何對等主機。` |
| `peers.banner` | `Could not read this host's peer configuration — {{call}} failed: {{message}}` | `無法讀取此主機的對等設定 — {{call}} 失敗：{{message}}` |
| `peers.retry` | `Retry` | `重試` |
| `peers.app_host` | `App host “{{name}}”` | `App 主機「{{name}}」` |
| `peers.self_alias_label` | `self alias` | `自稱` |
| `peers.direction` | `{{from}} → {{to}}` | `{{from}} → {{to}}` |
| `peers.reachable` | `reachable, daemon {{version}}` | `可連線，daemon {{version}}` |
| `peers.failed` | `failed: {{error}}` | `失敗：{{error}}` |
| `peers.calls_itself` | `calls itself “{{alias}}”` | `自稱「{{alias}}」` |
| `peers.drift` | `drift` | `名稱不一致` |
| `peers.rename_to` | `Rename to {{alias}}` | `改名為 {{alias}}` |
| `peers.renaming` | `Renaming…` | `改名中…` |
| `peers.their_entry` | `({{name}}'s entry: {{alias}})` | `（{{name}} 的項目：{{alias}}）` |
| `peers.no_entry` | `no entry for “{{alias}}” on {{name}}` | `{{name}} 上沒有「{{alias}}」的項目` |
| `peers.not_verifiable` | `not verifiable — not a host in this App` | `無法驗證 — 不是此 App 的主機` |
| `peers.could_not_ask` | `{{name}} could not be asked: {{cause}}` | `無法詢問 {{name}}：{{cause}}` |
| `peers.status` | `status` | `狀態` |
| `peers.status.bidirectional` | `bidirectional` | `雙向` |
| `peers.status.one-way` | `one-way` | `單向` |
| `peers.status.outbound-only` | `outbound-only` | `僅出站` |
| `peers.status.return-unknown` | `return-unknown` | `回程未知` |
| `peers.status.unpaired` | `unpaired` | `未配對` |
| `peers.status.checking` | `checking` | `檢查中` |

- [ ] **Step 1: Write the failing tests**

Create `spa/src/components/hosts/PeersSection.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { StrictMode } from 'react'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { PeersSection } from './PeersSection'
import { useHostStore } from '../../stores/useHostStore'
import * as api from '../../lib/host-api'
import { HostApiError, type PeerHostRow, type PeerHostVerify } from '../../lib/host-api'

vi.mock('../../lib/host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/host-api')>()),
  fetchHostInfo: vi.fn(),
  fetchPeerSettings: vi.fn(),
  listPeerHosts: vi.fn(),
  verifyPeerHost: vi.fn(),
  updatePeerHost: vi.fn(),
}))

const M = 'hM'
const A = 'hA'

const AIR_ROW: PeerHostRow = { alias: 'air', url: 'http://100.64.0.4:7860', host_id: 'wakes-air-2026:oa6drb',
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true }
const MLAB_ROW: PeerHostRow = { alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm',
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true }
const ok = (alias: string, self_alias: string, host_id: string): PeerHostVerify =>
  ({ alias, host_id, ok: true, self_alias, daemon_version: '1.0.0-alpha.378' })

// High-entropy so a leak into the DOM cannot be mistaken for ordinary text (spec D-8).
const SECRET_M = 'pdx_admin_secret_M_9f3k2q8w'
const SECRET_A = 'pdx_admin_secret_A_7t5r1z0x'

function seedHosts(airStatus: 'connected' | 'disconnected' = 'connected') {
  useHostStore.setState({
    hosts: {
      [M]: { id: M, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0, token: SECRET_M },
      [A]: { id: A, name: 'Air 2026', ip: '100.64.0.4', port: 7860, order: 1, token: SECRET_A },
    },
    hostOrder: [M, A],
    runtime: { [M]: { status: 'connected' }, [A]: { status: airStatus } },
  })
}

/** The §2.1 fixture: mlab's entry `air`, the peer calls itself `air26`, both directions green. */
function seedFixture() {
  vi.mocked(api.fetchHostInfo).mockImplementation(async (h) => ({
    host_id: h === M ? 'mini-lab:278cbm' : 'wakes-air-2026:oa6drb',
    tmux_instance: '', purdex_version: '', tmux_version: '', os: '', arch: '',
  }))
  vi.mocked(api.fetchPeerSettings).mockImplementation(async (h) => ({ deliver: true, alias: h === M ? 'mini-lab' : 'air26' }))
  vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [AIR_ROW] : [MLAB_ROW]))
  vi.mocked(api.verifyPeerHost).mockImplementation(async (h, alias) =>
    h === M ? ok(alias, 'air26', 'wakes-air-2026:oa6drb') : ok(alias, 'mini-lab', 'mini-lab:278cbm'))
  vi.mocked(api.updatePeerHost).mockResolvedValue({ ...AIR_ROW, alias: 'air26' })
}

beforeEach(() => {
  vi.mocked(api.fetchHostInfo).mockReset()
  vi.mocked(api.fetchPeerSettings).mockReset()
  vi.mocked(api.listPeerHosts).mockReset()
  vi.mocked(api.verifyPeerHost).mockReset()
  vi.mocked(api.updatePeerHost).mockReset()
  seedHosts()
  seedFixture()
})

describe('PeersSection — the §2.1 fixture', () => {
  it('renders the §5.3 row: three names labelled, both lines green, status bidirectional, drift + Rename', async () => {
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    // The selected host's own self alias, labelled (the third of the §2.1 names).
    const self = screen.getByTestId('peers-self')
    expect(self).toHaveTextContent('self alias')
    expect(self).toHaveTextContent('mini-lab')
    expect(self).toHaveTextContent('mini-lab:278cbm')
    expect(within(row).getByTestId('peer-alias')).toHaveTextContent('air')
    expect(within(row).getByTestId('peer-app-host')).toHaveTextContent('Air 2026')
    expect(within(row).getByTestId('peer-url')).toHaveTextContent('http://100.64.0.4:7860')
    expect(within(row).getByTestId('peer-host-id')).toHaveTextContent('wakes-air-2026:oa6drb')
    expect(within(row).getByTestId('peer-outbound')).toHaveAttribute('data-ok', 'true')
    expect(within(row).getByTestId('peer-outbound')).toHaveTextContent('1.0.0-alpha.378')
    expect(within(row).getByTestId('peer-inbound')).toHaveAttribute('data-ok', 'true')
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent('mini-lab')     // "(Air 2026's entry: mini-lab)"
    expect(within(row).getByTestId('peer-outbound-drift')).toHaveTextContent('air26')
    expect(within(row).getByTestId('peer-outbound-rename')).toHaveTextContent('air26')
    expect(within(row).queryByTestId('peer-inbound-drift')).toBeNull()               // mlab's self alias equals air's entry name
    expect(within(row).queryByTestId('peer-inbound-rename')).toBeNull()
  })

  it('paints checking while the dials are out, then fills in', async () => {
    let release!: (v: PeerHostVerify) => void
    vi.mocked(api.verifyPeerHost).mockImplementation((h, alias) =>
      h === M ? new Promise<PeerHostVerify>((r) => { release = r }) : Promise.resolve(ok(alias, 'mini-lab', 'mini-lab:278cbm')))
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-inbound')).toHaveAttribute('data-ok', 'true'))
    expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'checking')
    release(ok('air', 'air26', 'wakes-air-2026:oa6drb'))
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
  })

  it('Rename calls updatePeerHost(X, "air", {alias: "air26"}) and refreshes (verifies run again)', async () => {
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    const before = vi.mocked(api.verifyPeerHost).mock.calls.length
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [{ ...AIR_ROW, alias: 'air26' }] : [MLAB_ROW]))
    fireEvent.click(within(row).getByTestId('peer-outbound-rename'))
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledWith(M, 'air', { alias: 'air26' }))
    await screen.findByTestId('peer-row-air26')
    await waitFor(() => expect(vi.mocked(api.verifyPeerHost).mock.calls.length).toBeGreaterThan(before))
    expect(screen.queryByTestId('peer-outbound-drift')).toBeNull()
  })

  it('a 409 on Rename shows the daemon message inline and keeps the row', async () => {
    vi.mocked(api.updatePeerHost).mockRejectedValue(new HostApiError(409, 'Conflict', 'alias "air26" is already used by another host'))
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    fireEvent.click(within(row).getByTestId('peer-outbound-rename'))
    expect(await within(row).findByTestId('peer-outbound-rename-error')).toHaveTextContent('alias "air26" is already used by another host')
    expect(screen.getByTestId('peer-row-air')).toBeInTheDocument()
    expect(within(row).getByTestId('peer-outbound-rename')).toBeEnabled()
  })

  it('Rename on the return line acts on the counterpart host', async () => {
    // air's entry for mlab is named "mlab", mlab calls itself "mini-lab" → drift on the return line.
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [AIR_ROW] : [{ ...MLAB_ROW, alias: 'mlab' }]))
    vi.mocked(api.updatePeerHost).mockResolvedValue({ ...MLAB_ROW, alias: 'mini-lab' })
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    const btn = await within(row).findByTestId('peer-inbound-rename')
    expect(btn).toHaveTextContent('mini-lab')
    fireEvent.click(btn)
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledWith(A, 'mlab', { alias: 'mini-lab' }))
  })
})

describe('PeersSection — the return side', () => {
  it('a peer that is not an App host renders outbound-only, the D-4 sentence, and no return Rename', async () => {
    vi.mocked(api.listPeerHosts).mockImplementation(async () => [{ ...AIR_ROW, alias: 'stranger', host_id: 'stranger:aaaaaa', url: 'http://10.0.0.1:7860' }])
    vi.mocked(api.verifyPeerHost).mockResolvedValue(ok('stranger', 'strange-self', 'stranger:aaaaaa'))
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-stranger')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'outbound-only'))
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent('not verifiable')
    expect(within(row).queryByTestId('peer-inbound-rename')).toBeNull()
    expect(within(row).queryByTestId('peer-app-host')).toBeNull()
    // outbound drift still offered
    expect(within(row).getByTestId('peer-outbound-rename')).toHaveTextContent('strange-self')
  })

  it('a disconnected App host renders return-unknown with the cause and Refresh as the only action', async () => {
    seedHosts('disconnected')
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'return-unknown'))
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent('Air 2026 could not be asked: disconnected')
    expect(within(row).queryByTestId('peer-inbound-rename')).toBeNull()
    expect(api.fetchHostInfo).not.toHaveBeenCalledWith(A)
    expect(screen.getByTestId('peers-refresh')).toBeEnabled()
  })

  it('outbound failure with no return entry renders unpaired and shows the daemon error', async () => {
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [{ ...AIR_ROW, has_token: false }] : []))
    vi.mocked(api.verifyPeerHost).mockResolvedValue({ alias: 'air', host_id: 'wakes-air-2026:oa6drb', ok: false, error: 'no outbound token', self_alias: '', daemon_version: '' })
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'unpaired'))
    expect(within(row).getByTestId('peer-outbound')).toHaveAttribute('data-ok', 'false')
    expect(within(row).getByTestId('peer-outbound')).toHaveTextContent('no outbound token')
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent('no entry for “mini-lab” on Air 2026')
  })
})

describe('PeersSection — page states', () => {
  it('a failing precondition renders one banner naming the call and a Retry that re-runs', async () => {
    vi.mocked(api.listPeerHosts).mockRejectedValueOnce(new HostApiError(403, 'Forbidden', 'admin required'))
    render(<PeersSection hostId={M} />)
    const banner = await screen.findByTestId('peers-banner')
    expect(banner).toHaveTextContent('list')
    expect(banner).toHaveTextContent('admin required')
    expect(screen.queryByTestId(/^peer-row-/)).toBeNull()
    fireEvent.click(screen.getByTestId('peers-retry'))
    await screen.findByTestId('peer-row-air')
  })

  it('an empty list renders the empty sentence', async () => {
    vi.mocked(api.listPeerHosts).mockResolvedValue([])
    render(<PeersSection hostId={M} />)
    expect(await screen.findByTestId('peers-empty')).toBeInTheDocument()
  })

  it('Refresh re-runs everything and disables itself while running', async () => {
    render(<PeersSection hostId={M} />)
    await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(screen.getByTestId('peers-refresh')).toBeEnabled())
    const n = vi.mocked(api.verifyPeerHost).mock.calls.length
    fireEvent.click(screen.getByTestId('peers-refresh'))
    await waitFor(() => expect(vi.mocked(api.verifyPeerHost).mock.calls.length).toBe(n + 2))
  })

  it('a result that lands after hostId changed is dropped', async () => {
    let release!: (v: PeerHostVerify) => void
    vi.mocked(api.verifyPeerHost).mockImplementation((h, alias) =>
      h === M ? new Promise<PeerHostVerify>((r) => { release = r }) : Promise.resolve(ok(alias, 'mini-lab', 'mini-lab:278cbm')))
    const { rerender } = render(<PeersSection hostId={M} />)
    await screen.findByTestId('peer-row-air')
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === A ? [MLAB_ROW] : [AIR_ROW]))
    rerender(<PeersSection hostId={A} />)
    await screen.findByTestId('peer-row-mini-lab')
    release(ok('air', 'air26', 'wakes-air-2026:oa6drb'))
    await new Promise((r) => setTimeout(r, 0))
    expect(screen.queryByTestId('peer-row-air')).toBeNull()
  })

  it('never renders a token value (spec D-8): neither host admin token reaches the DOM, even in attributes', async () => {
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    expect(api.updatePeerHost).not.toHaveBeenCalled()
    expect(document.body.innerHTML).not.toContain(SECRET_M)
    expect(document.body.innerHTML).not.toContain(SECRET_A)
  })

  it('under StrictMode (dev double-mount) the page still ends bidirectional and does not paint the discarded first run', async () => {
    render(<StrictMode><PeersSection hostId={M} /></StrictMode>)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    expect(screen.getAllByTestId('peer-row-air')).toHaveLength(1)
    await waitFor(() => expect(screen.getByTestId('peers-refresh')).toBeEnabled())
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd spa && npx vitest run src/components/hosts/PeersSection.test.tsx`
Expected: FAIL — module not found (and, once the file exists, missing i18n keys render as the key).

- [ ] **Step 3: Add the i18n keys**

Insert the 27 keys from the table above into `spa/src/locales/en.json` and `spa/src/locales/zh-TW.json` immediately after the line `"hosts.snapshots.client_no_dev": …,`. Keep the JSON valid (trailing commas!). Run `cd spa && npx vitest run src/locales` — `locale-completeness` must pass.

- [ ] **Step 4: Implement the component**

Create `spa/src/components/hosts/PeersSection.tsx`:

```tsx
// spa/src/components/hosts/PeersSection.tsx — Hosts › Peers (Phase D spec §5).
// Renders a PairingSnapshot from loadPairings and wires three buttons:
// Refresh, and one Rename per direction when the peer's self alias drifts
// from the entry name (spec D-5: adoption is a plain PUT {alias}). Nothing
// here is cached or persisted; the snapshot lives in useState and dies with
// the component (spec D-6, D-8).
import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowsClockwise, CheckCircle, WarningCircle, Circle } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import {
  HostApiError, fetchHostInfo, fetchPeerSettings, listPeerHosts, updatePeerHost, verifyPeerHost,
} from '../../lib/host-api'
import { aliasDrift, pairStatus, type PairStatus, type Side, type InboundState } from '../../lib/peer-pairing'
import { loadPairings, type PairingApi, type PairingAppHost, type PairingRow, type PairingSnapshot } from '../../lib/peer-pairing-load'

const api: PairingApi = { info: fetchHostInfo, settings: fetchPeerSettings, list: listPeerHosts, verify: verifyPeerHost }

const STATUS_CLASS: Record<PairStatus, string> = {
  bidirectional: 'text-status-success',
  'one-way': 'text-status-warning',
  unpaired: 'text-status-error',
  'outbound-only': 'text-text-muted',     // half-verifiable, never green (spec D-4)
  'return-unknown': 'text-text-muted',    // transient, never green
  checking: 'text-text-muted',
}

interface Props { hostId: string }

export function PeersSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const host = hosts[hostId]

  const [snap, setSnap] = useState<PairingSnapshot | null>(null)
  const [busy, setBusy] = useState(false)
  // Generation counter: a snapshot from a run started for a previous hostId
  // (or a previous Refresh) must never paint over the current one.
  const gen = useRef(0)

  const run = useCallback(async () => {
    const my = ++gen.current
    setBusy(true)
    const { hosts: hs, hostOrder: order, runtime: rt, getDaemonBase } = useHostStore.getState()
    const toApp = (id: string): PairingAppHost => ({ hostId: id, name: hs[id]?.name ?? id, url: getDaemonBase(id), status: rt[id]?.status })
    const others = order.filter((id) => id !== hostId).map(toApp)
    const emit = (s: PairingSnapshot) => { if (gen.current === my) setSnap(s) }
    try {
      await loadPairings(toApp(hostId), others, api, emit)
    } finally {
      if (gen.current === my) setBusy(false)
    }
  }, [hostId])

  useEffect(() => {
    setSnap(null)
    void run()
    return () => { gen.current++ }
  }, [run])

  if (!host) return null

  return (
    <div className="max-w-3xl">
      <div className="flex items-center justify-between mb-1">
        <h2 className="text-lg font-semibold">{t('hosts.peers')}</h2>
        <button type="button" data-testid="peers-refresh" disabled={busy} onClick={() => void run()}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-50">
          <ArrowsClockwise size={14} className={busy ? 'animate-spin' : ''} />{busy ? t('peers.checking') : t('peers.refresh')}
        </button>
      </div>
      <p className="text-xs text-text-muted mb-1">{t('peers.desc')}</p>
      {/* The selected host's own identity, labelled: the third of the three names (spec §2.1).
          It is what the return direction's entry on the counterpart must point at. */}
      {snap?.self && (
        <p data-testid="peers-self" className="text-xs text-text-muted mb-4 font-mono">
          {host.name} · {t('peers.self_alias_label')}: <span className="text-text-secondary">{snap.self.self_alias}</span> · {snap.self.host_id}
        </p>
      )}

      {snap?.error && (
        <div data-testid="peers-banner" className="flex items-start justify-between gap-3 px-3 py-2.5 rounded-md mb-4 bg-red-500/10 border border-red-500/20">
          <p className="text-sm text-red-400">{t('peers.banner', { call: snap.error.call, message: snap.error.message })}</p>
          <button type="button" data-testid="peers-retry" disabled={busy} onClick={() => void run()}
            className="text-xs px-2 py-1 rounded bg-surface-tertiary cursor-pointer disabled:opacity-50">{t('peers.retry')}</button>
        </div>
      )}

      {snap && !snap.error && snap.rows.length === 0 && (
        <p data-testid="peers-empty" className="text-sm text-text-muted">{t('peers.empty')}</p>
      )}

      {snap && !snap.error && snap.rows.length > 0 && (
        <div className="space-y-3">
          {snap.rows.map((row) => (
            <PeerRow key={row.entry.alias} hostId={hostId} hostName={host.name} self={snap.self!} row={row}
              busy={busy} onRenamed={() => void run()} />
          ))}
        </div>
      )}
    </div>
  )
}

/* ─── One entry ─── */

interface RowProps {
  hostId: string
  hostName: string
  self: { host_id: string; self_alias: string }
  row: PairingRow
  busy: boolean
  onRenamed: () => void
}

function PeerRow({ hostId, hostName, self, row, busy, onRenamed }: RowProps) {
  const t = useI18nStore((s) => s.t)
  const status = pairStatus(row.outbound, row.inbound)
  const { entry, counterpart, returnEntry } = row

  const outDrift = row.outbound !== 'pending' && row.outbound.ok ? aliasDrift(entry.alias, row.outbound.self_alias) : ''
  const inDrift = returnEntry && typeof row.inbound === 'object' && row.inbound.ok ? aliasDrift(returnEntry.alias, row.inbound.self_alias) : ''

  return (
    <div data-testid={`peer-row-${entry.alias}`} className="border border-border-subtle rounded-lg px-4 py-3 text-sm">
      <div className="flex items-baseline gap-2 flex-wrap">
        <span data-testid="peer-alias" className="font-semibold text-text-primary">{entry.alias}</span>
        {counterpart && (
          <span data-testid="peer-app-host" className="text-xs text-text-secondary">{t('peers.app_host', { name: counterpart.name })}</span>
        )}
        <span data-testid="peer-status" data-status={status} className={`ml-auto text-xs font-medium ${STATUS_CLASS[status]}`}>
          {t('peers.status')}: {t(`peers.status.${status}`)}
        </span>
      </div>
      <div className="font-mono text-xs text-text-muted mt-0.5">
        <span data-testid="peer-url">{entry.url}</span>
        {entry.host_id && <> · <span data-testid="peer-host-id">{entry.host_id}</span></>}
      </div>

      <div className="mt-2 space-y-1.5">
        {/* Outbound: X → peer, verify(X, E.alias) */}
        <DirectionLine testId="peer-outbound" from={hostName} to={entry.alias} side={row.outbound}
          drift={outDrift} renameTarget={{ hostId, alias: entry.alias }} busy={busy} onRenamed={onRenamed} />

        {/* Return: peer → X, verify(Y, E'.alias) or one of the three sentences */}
        {counterpart && returnEntry ? (
          <DirectionLine testId="peer-inbound" from={entry.alias} to={hostName} side={row.inbound as Side}
            note={t('peers.their_entry', { name: counterpart.name, alias: returnEntry.alias })}
            drift={inDrift} renameTarget={{ hostId: counterpart.hostId, alias: returnEntry.alias }} busy={busy} onRenamed={onRenamed} />
        ) : (
          <div data-testid="peer-inbound" data-ok="none" className="flex items-center gap-2 text-text-muted">
            <Circle size={14} />
            <span className="font-mono text-xs">{t('peers.direction', { from: entry.alias, to: hostName })}</span>
            <span className="text-xs">{returnSentence(t, row, self, counterpart)}</span>
          </div>
        )}
      </div>
    </div>
  )
}

function returnSentence(
  t: (k: string, p?: Record<string, string | number>) => string,
  row: PairingRow, self: { self_alias: string }, counterpart: { name: string } | null,
): string {
  if (row.inbound === 'not-app-host' || !counterpart) return t('peers.not_verifiable')
  if (row.inbound === 'counterpart-unavailable') return t('peers.could_not_ask', { name: counterpart.name, cause: row.counterpartCause })
  return t('peers.no_entry', { alias: self.self_alias, name: counterpart.name })
}

/* ─── One direction ─── */

interface LineProps {
  testId: string
  from: string
  to: string
  side: Side
  note?: string
  drift: string
  renameTarget: { hostId: string; alias: string }
  busy: boolean
  onRenamed: () => void
}

function DirectionLine({ testId, from, to, side, note, drift, renameTarget, busy, onRenamed }: LineProps) {
  const t = useI18nStore((s) => s.t)
  const [renaming, setRenaming] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const rename = async () => {
    setRenaming(true)
    setError(null)
    try {
      await updatePeerHost(renameTarget.hostId, renameTarget.alias, { alias: drift })
      onRenamed()
    } catch (e) {
      // 400/409 carry the daemon's own sentence; nothing is retried or auto-suffixed (v4 §7.2).
      setError(e instanceof HostApiError ? e.detail : e instanceof Error ? e.message : String(e))
    } finally {
      setRenaming(false)
    }
  }

  const ok = side === 'pending' ? 'pending' : String(side.ok)
  const Icon = side === 'pending' ? Circle : side.ok ? CheckCircle : WarningCircle
  const colour = side === 'pending' ? 'text-text-muted' : side.ok ? 'text-status-success' : 'text-status-error'

  return (
    <div data-testid={testId} data-ok={ok} className="flex items-center gap-2 flex-wrap">
      <Icon size={14} weight={side === 'pending' ? 'regular' : 'fill'} className={colour} />
      <span className="font-mono text-xs text-text-secondary">{t('peers.direction', { from, to })}</span>
      <span className="text-xs text-text-secondary">
        {side === 'pending' ? t('peers.checking')
          : side.ok ? t('peers.reachable', { version: side.daemon_version })
          : t('peers.failed', { error: side.error })}
      </span>
      {note && <span className="text-xs text-text-muted">{note}</span>}
      {drift && (
        <>
          <span data-testid={`${testId}-drift`} className="text-xs text-status-warning">
            {t('peers.calls_itself', { alias: drift })} ({t('peers.drift')})
          </span>
          <button type="button" data-testid={`${testId}-rename`} disabled={busy || renaming} onClick={() => void rename()}
            className="text-xs px-2 py-0.5 rounded bg-accent text-white cursor-pointer disabled:opacity-50">
            {renaming ? t('peers.renaming') : t('peers.rename_to', { alias: drift })}
          </button>
        </>
      )}
      {error && <span data-testid={`${testId}-rename-error`} className="text-xs text-status-error whitespace-pre-wrap">{error}</span>}
    </div>
  )
}
```

Notes for the implementer:
- `row.inbound as Side` in the return `DirectionLine` is safe because that branch is entered only when `returnEntry` exists, and the loader sets `inbound` to `'pending'` or an outcome exactly then. If TypeScript still complains, narrow with a local `const inboundSide: Side = typeof row.inbound === 'object' || row.inbound === 'pending' ? row.inbound : 'pending'`.
- The drift `button` text must contain the self alias verbatim (the test asserts `toHaveTextContent('air26')`).
- Keep `data-testid`s exactly as the tests name them: `peer-row-<alias>`, `peer-alias`, `peer-app-host`, `peer-url`, `peer-host-id`, `peer-status[data-status]`, `peer-outbound[data-ok]`, `peer-inbound[data-ok]`, `peer-outbound-drift`, `peer-outbound-rename`, `peer-outbound-rename-error`, `peer-inbound-drift`, `peer-inbound-rename`, `peers-refresh`, `peers-banner`, `peers-retry`, `peers-empty`.
- Check the icon names exist in `@phosphor-icons/react` (`CheckCircle`, `WarningCircle`, `Circle`, `ArrowsClockwise` all do at the installed version; `ls spa/node_modules/@phosphor-icons/react/dist/csr/` to confirm).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd spa && npx vitest run src/components/hosts/PeersSection.test.tsx src/locales`
Expected: PASS. Then `cd spa && pnpm run lint` — fix anything in the new files only.

- [ ] **Step 6: Commit**

```bash
git commit --only spa/src/components/hosts/PeersSection.tsx spa/src/components/hosts/PeersSection.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): Hosts › Peers page — both directions verified live, alias drift adoptable

Phase D spec §5.3. Renders loadPairings snapshots; Rename = PUT {alias:
self_alias} on whichever side drifted; 409/400 shown verbatim inline.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Register the sub-page at order 10

**Files:**
- Modify: `spa/src/lib/register-modules/index.tsx:58` (import), `:417–428` (the `setHostBuiltinSections` list)
- Modify: `spa/src/lib/host-builtin-sections.test.tsx:101–111,127–150`
- Modify: `spa/src/lib/register-modules.test.ts:77–84,156–170`

**Interfaces:**
- Consumes: `PeersSection` from Task 4.

- [ ] **Step 1: Update the three pinned tests first (they go red)**

`spa/src/lib/host-builtin-sections.test.tsx`:
- line 101: `expect(builtinContribs).toHaveLength(11)`
- the `orders built-in contributions as: …` test: append `'peers'` to the title and to the expected array: `'projects', 'commands', 'snapshots', 'peers',`
- the `wraps all ten sections` test: rename to `wraps all eleven sections to their original components`, add `import { PeersSection } from '../components/hosts/PeersSection'` next to the `SnapshotsSection` import (line 74), and add `{ localId: 'peers', component: PeersSection },` after the snapshots line.
- the `is HMR-safe: re-running registerBuiltinModules after clearAll keeps built-in count stable` test (lines 180–188): `expect(first).toBe(11)` and `expect(second).toBe(11)`.
- header comment line 6: `ten` → `eleven`.

`spa/src/lib/register-modules.test.ts`:
- line 83: append `'peers'` to the expected localId list.
- the `registers host sub-pages projects / commands / snapshots after nex (7/8/9)` test: rename to `… / snapshots / peers after nex (7/8/9/10)`, add `'peers'` to the filter list and `['peers', 10]` to the expected tuples.
- after the `both locales carry the hosts.snapshots label` test add:

```ts
  it('both locales carry the hosts.peers label', () => {
    const en = enLocale as Record<string, string>
    const zh = zhLocale as Record<string, string>
    expect(en['hosts.peers']).toBe('Peers')
    expect(zh['hosts.peers']).toBe('對等主機')
  })
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd spa && npx vitest run src/lib/host-builtin-sections.test.tsx src/lib/register-modules.test.ts`
Expected: FAIL on the length/list assertions (label test passes already because Task 4 added the keys).

- [ ] **Step 3: Register**

In `spa/src/lib/register-modules/index.tsx`, next to the `SnapshotsSection` import add `import { PeersSection } from '../../components/hosts/PeersSection'`, and append to the list:

```ts
    { localId: 'peers',     labelKey: 'hosts.peers',     order: 10, component: PeersSection },
```

- [ ] **Step 4: Run to verify they pass, then the whole suite + lint + build**

Run: `cd spa && npx vitest run src/lib/host-builtin-sections.test.tsx src/lib/register-modules.test.ts`
Expected: PASS.
Run: `cd spa && npx vitest run` — Expected: all green (note any pre-existing flake by name; do not touch unrelated tests).
Run: `cd spa && pnpm run lint && pnpm run build` — Expected: clean.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/register-modules/index.tsx spa/src/lib/host-builtin-sections.test.tsx spa/src/lib/register-modules.test.ts -m "feat(spa): register Hosts › Peers sub-page at /hosts/<id>/peers (order 10)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Mutation record (§8.2 deliverable) — M1–M23

**Files:**
- Create: `docs/plans/2026-09-18-peer-pairing-d2-mutations.md`

Each row: apply the one-edit mutation, run the named test with `npx vitest run <file> -t '<name>'`, record FAIL with the assertion text, revert with `git checkout -- <file>`. After all rows `git status --short` must be empty except the record itself.

| # | mutation (exact edit) | file | must go red |
|---|---|---|---|
| M1 | in `pairStatus`, `if (inbound === 'not-app-host') return 'outbound-only'` → `return 'bidirectional'` | `peer-pairing.ts` | `peer-pairing.test.ts` "ok / not-app-host → outbound-only" **and** `PeersSection.test.tsx` "a peer that is not an App host renders outbound-only…" (spec §8.2's named mutation) |
| M2 | `if (inbound === 'counterpart-unavailable') return 'return-unknown'` → `return 'outbound-only'` | `peer-pairing.ts` | "ok / counterpart-unavailable → return-unknown" and "outbound-only and return-unknown are never the same word" |
| M3 | in `pairStatus`, delete the leading `if (outbound === 'pending' \|\| inbound === 'pending') return 'checking'` (and make the rest type-check with `as`) | `peer-pairing.ts` | every `pending` row |
| M4 | in `matchCounterpart`, replace the guarded by-id search with an unconditional `hosts.find((h) => h.host_id === entry.host_id)` (both `!== ''` guards gone, so `''` matches `''`) | `peer-pairing.ts` | "both host_id-less match only by URL, never by "" === """ |
| M5 | in `matchCounterpart`, change the URL fallback condition to `true` (ignore known host_id) | `peer-pairing.ts` | "same URL but a different KNOWN host_id is not a match" |
| M6 | in `matchCounterpart`, change the URL fallback condition to `entry.host_id === ''` only | `peer-pairing.ts` | "URL also joins an entry to a host whose host_id is unknown" **and** `peer-pairing-load.test.ts` "a disconnected App host still joins by URL…" |
| M7 | in `normalizePeerUrl`, remove `.replace(/\/+$/, '')` | `peer-pairing.ts` | "trailing slash trimmed" and "URL is the fallback when the entry has no host_id (…trailing slash ignored)" |
| M8 | in `aliasDrift`, drop `.toLowerCase()` on both sides | `peer-pairing.ts` | "case-insensitive equal is not drift" |
| M9 | in `loadMeta`, treat every status as connected (`if (false) return …`) | `peer-pairing-load.ts` | "a disconnected App host still joins by URL and is counterpart-unavailable with its status as cause" (`info` gets called with `hA`) |
| M10 | in `loadPairings`, replace `listOf(y.hostId)` with `api.list(y.hostId).catch(…)` (no memo) | `peer-pairing-load.ts` | "two entries pointing at the same daemon share ONE list(Y) call" |
| M11 | in `loadPairings`, on a rejected verify set the row to `'pending'` instead of `{ok:false}` | `peer-pairing-load.ts` | "a rejected verify … is that direction failing, not a page error" |
| M12 | in `loadPairings`, remove the early `return snap` on a failed precondition (fall through with `self` undefined) | `peer-pairing-load.ts` | "a failing settings(X) yields the banner, no rows, and no calls to any other host" |
| M13 | in `loadPairings`, delete the `emit(snap)` before the dials | `peer-pairing-load.ts` | "joins by host_id … ends bidirectional" (emit count / first-emit pending assertions) and `PeersSection.test.tsx` "paints checking while the dials are out" |
| M14 | in `PeersSection.run`, drop the `gen.current === my` check in `emit` | `PeersSection.tsx` | "a result that lands after hostId changed is dropped" |
| M15 | in `DirectionLine.rename`, replace `{ alias: drift }` with `{ alias: renameTarget.alias }` | `PeersSection.tsx` | "Rename calls updatePeerHost(X, "air", {alias: "air26"})" |
| M16 | in `DirectionLine.rename`, catch → `setError(null)` | `PeersSection.tsx` | "a 409 on Rename shows the daemon message inline" |
| M17 | in `PeerRow`, pass `renameTarget={{ hostId, alias: returnEntry.alias }}` on the return line (wrong host) | `PeersSection.tsx` | "Rename on the return line acts on the counterpart host" |
| M18 | in `peerHostError`, never read the body (`detail` always `undefined`) | `host-api.ts` | "updatePeerHost surfaces a 409 with the daemon text in detail" |
| M19 | in `HostApiError`'s constructor, `this.detail = detail ?? ''` (drop the statusText fallback) | `host-api.ts` | "a non-JSON error body falls back to statusText in detail" |
| M20 | in `fetchHostInfo`, return `fetchInfo(hostId).then((r) => r.json())` (skip `peerHostJson`) | `host-api.ts` | "fetchHostInfo rejects with HostApiError on a non-2xx" |
| M21 | in `PeersSection`, render `<span>{host.token}</span>` inside the `peers-self` line (a deliberate D-8 leak) | `PeersSection.tsx` | "never renders a token value (spec D-8)" |
| M22 | in `PeersSection`, delete the `peers-self` paragraph | `PeersSection.tsx` | "renders the §5.3 row: three names labelled…" (`peers-self` missing) |
| M23 | in `matchReturnEntry`, return `rows[0] ?? null` | `peer-pairing.ts` | "returns the row whose host_id is ours" (first row is the stranger) and "falls back to URL only for a row with no host_id" (the `stranger:1` row would be returned) |

- [ ] **Step 1: Run every row, record, revert**
- [ ] **Step 2: `git status --short` shows only the record; commit**

```bash
git commit --only docs/plans/2026-09-18-peer-pairing-d2-mutations.md -m "docs(plan): D2 mutation-test record (M1–M23 all red)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## After the tasks (main session, not a subagent)

1. `git push -u origin worktree-peer-pairing-d2`; PR titled `feat(spa): Peer Pairing D2 — Hosts › Peers page (verify both directions, adopt drifted alias)` with the spec/plan links, the mutation record, and a note that `HOST_SUB_PAGES` (`lib/host-routes.ts`) is a legacy constant not consulted for routing and is deliberately untouched.
2. Two review rounds (codex `--model gpt-5.5`): R1 standard; R2 attacker / defender (spec-drift: D-3 + §5.2 join rule, D-4 vs `return-unknown`, D-8 no token in state) / file-health (`PeersSection.tsx` size — if it passes ~250 lines, split `DirectionLine` into its own file).
3. Real-machine acceptance (spec §9 D2) on the mlab dev server (`100.64.0.2:5174`): open `/hosts/<mlab>/peers` → one row `air`, joined to the App's air host, both lines green, `bidirectional`, drift `air26`, Rename → header reads `air26`; `bin/pdx peers --all` prints `air26/…` and no drift line; `pdx msg send air26/<name> "…"` from a Claude Code session on mlab delivers. Record the result in the PR.
4. Merge, bump (`git fetch` and read `VERSION` first — 379 at plan time), update the memory file.

## Self-review against the spec

- §5.1 placement/data → Task 5 (contribution), Task 1 (wrappers), Task 2 (`matchCounterpart`, `pairStatus`, `aliasDrift`). ✔
- §5.2 steps 0–4, memoised `list(Y)`, per-cause unavailability, parallel verifies, nothing cached → Task 3. ✔ (The D-3/§5.2 join rule reading is stated up front in Task 2 for the reviewer.)
- §5.3 row content: three names labelled, URL + host_id, outbound line with drift + Rename, return line with its own drift + Rename on Y / three sentences, status word, `outbound-only` neutral → Task 4. ✔ `has_token:false` shows the daemon's own "no outbound token" (we still call verify; the daemon answers without dialling, §4.1). ✔
- §5.4 nothing else → no pairing/rotation/toggles anywhere. ✔
- §8.2 tests: table-driven `pairStatus`, `matchCounterpart` (host_id wins, URL only when host_id empty, trailing slash), `aliasDrift` (empty, case) → Task 2; component: §2.1 fixture, drift + Rename `air26`, click → `updatePeerHost('X','air',{alias:'air26'})` + refresh, 409 message, non-App → `outbound-only` no Rename, disconnected → not joined-as-available → Task 4; named mutation → M1. ✔
- Types consistent: `PairingRow.inbound: InboundState` includes `'pending'`; `Side` excludes the three sentences; `DirectionLine` takes `Side` only. `PairingSnapshot.error.call` union matches the loader's tuple. `CounterpartCandidate.hostId` is reused to carry an *alias* in the swapped join (documented inline) — acceptable, but if a reviewer objects, add a `matchReturnEntry(self, x.url, theirs)` wrapper in `peer-pairing.ts` rather than duplicating the rule.
