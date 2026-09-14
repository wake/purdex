# Explicit Dev Host & Local-Daemon Token Visibility — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Settings → Development takes its update / rebuild / binary source from an explicitly chosen host (no fallback), and the *Local daemon* block shows the local daemon's URL + token and lets the user add that endpoint to the host list.

**Architecture:** `useHostStore` gains a persisted `devHostId` plus two pure helpers (`selectDevHostId`, `findHostByEndpoint`). `DevEnvironmentSection` derives `daemonBase | null` from the selector and enforces a source-change reset + generation guard so a previous host's late responses never paint the page. Electron's `localDaemonStatus` IPC carries the config token and hostname; `LocalDaemonSection` renders them and calls the existing `registerLocalHost`.

**Tech Stack:** React 19, Zustand 5, Vitest + Testing Library (jsdom), Electron main (node:os), Phosphor Icons, flat-JSON i18n (`spa/src/locales/{en,zh-TW}.json`).

**Spec:** `docs/specs/2026-09-14-dev-host-and-local-daemon-token-spec.md` (v2). Read it first; the plan argues from it.

## Global Constraints

- Package manager is **pnpm**. SPA commands run from `spa/`: `npx vitest run <file>`, `pnpm run lint`, `pnpm run build`. Electron tests: from repo root `npx vitest run electron/local-daemon/index.test.ts`.
- Worktree: every shell command starts with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/dev-host-local-token && `; Edit/Write use absolute paths under that directory.
- TDD: write the failing test, run it red, implement, run it green, then commit. One commit per task.
- No fallback dev host (spec D2). `devHostId === null` means no dev requests at all.
- Zustand hook selectors must return primitives or existing references — never a fresh `[]`/`{}` (repo just fixed an infinite-loop bug of that shape, PR #1012).
- i18n: every new key goes to **both** `en.json` and `zh-TW.json`; `spa/src/locales/locale-completeness.test.ts` fails otherwise. Interpolation is `{{name}}`.
- Icons: Phosphor only (`@phosphor-icons/react`).
- Commit trailer (every commit):
  ```
  Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01AcsQgPS13NWN32zfM5JHSz
  ```

---

## File map

| File | Responsibility after this plan |
|---|---|
| `spa/src/stores/useHostStore.ts` | + `devHostId`, `setDevHost`; exports `selectDevHostId`, `findHostByEndpoint`; `registerLocalHost` uses the helper. |
| `spa/src/stores/useHostStore.test.ts` | + `dev host` and `findHostByEndpoint` describes. |
| `spa/src/lib/sync/contributors/hosts.test.ts` | + full-replace drops `devHostId` resolution. |
| `electron/local-daemon/types.ts`, `index.ts`, `index.test.ts` | `config.token`, required `hostname` on status. |
| `spa/src/types/electron.d.ts` | mirrors the status type. |
| `spa/src/components/settings/DevEnvironmentSection.tsx` (+ `.test.tsx`) | host picker, null gating, source-change reset, generation guard, picker lock. |
| `spa/src/components/settings/LocalDaemonSection.tsx` (+ `.test.tsx`) | URL / token / add-to-hosts rows; Install/Update gated on `daemonBase`. |
| `spa/src/locales/en.json`, `zh-TW.json` | new keys (listed per task). |

---

### Task 1: Host store — `devHostId`, `selectDevHostId`, `findHostByEndpoint`

**Files:**
- Modify: `spa/src/stores/useHostStore.ts`
- Test: `spa/src/stores/useHostStore.test.ts`, `spa/src/lib/sync/contributors/hosts.test.ts`

**Interfaces:**
- Produces:
  - state `devHostId: string | null` (persisted via `partialize`)
  - action `setDevHost(hostId: string | null): void` — no-op for an unknown id
  - `export function selectDevHostId(state: Pick<HostState, 'devHostId' | 'hosts'>): string | null`
  - `export function findHostByEndpoint(hosts: Record<string, HostConfig>, ip: string, port: number): HostConfig | undefined`

- [ ] **Step 1: Write the failing tests** — append to `spa/src/stores/useHostStore.test.ts` (imports at top: add `selectDevHostId, findHostByEndpoint` to the existing `useHostStore` import line):

```ts
describe('dev host', () => {
  beforeEach(() => { useHostStore.getState().reset() })

  it('defaults to null and selectDevHostId reads null', () => {
    expect(useHostStore.getState().devHostId).toBeNull()
    expect(selectDevHostId(useHostStore.getState())).toBeNull()
  })

  it('setDevHost accepts a known id', () => {
    const id = useHostStore.getState().hostOrder[0]
    useHostStore.getState().setDevHost(id)
    expect(selectDevHostId(useHostStore.getState())).toBe(id)
  })

  it('setDevHost ignores an unknown id', () => {
    useHostStore.getState().setDevHost('nope')
    expect(useHostStore.getState().devHostId).toBeNull()
  })

  it('setDevHost(null) clears', () => {
    const id = useHostStore.getState().hostOrder[0]
    useHostStore.getState().setDevHost(id)
    useHostStore.getState().setDevHost(null)
    expect(useHostStore.getState().devHostId).toBeNull()
  })

  it('removeHost clears devHostId when it removes the dev host', () => {
    const s = useHostStore.getState()
    const extra = s.addHost({ name: 'b', ip: '10.0.0.2', port: 7860 })
    s.setDevHost(extra)
    s.removeHost(extra)
    expect(useHostStore.getState().devHostId).toBeNull()
  })

  it('removeHost of another host keeps devHostId', () => {
    const s = useHostStore.getState()
    const dev = s.hostOrder[0]
    const extra = s.addHost({ name: 'b', ip: '10.0.0.2', port: 7860 })
    s.setDevHost(dev)
    s.removeHost(extra)
    expect(useHostStore.getState().devHostId).toBe(dev)
  })

  it('selectDevHostId returns null once the id is gone from hosts (stale persisted id)', () => {
    const s = useHostStore.getState()
    const dev = s.hostOrder[0]
    s.setDevHost(dev)
    useHostStore.setState({ hosts: { other: { id: 'other', name: 'o', ip: '10.0.0.9', port: 1, order: 0 } }, hostOrder: ['other'] })
    expect(useHostStore.getState().devHostId).toBe(dev) // raw field untouched
    expect(selectDevHostId(useHostStore.getState())).toBeNull()
  })

  it('devHostId is part of the persisted slice', () => {
    const s = useHostStore.getState()
    s.setDevHost(s.hostOrder[0])
    const partialize = useHostStore.persist.getOptions().partialize!
    expect(partialize(useHostStore.getState())).toMatchObject({ devHostId: s.hostOrder[0] })
  })

  it('selectDevHostId follows the same id after an endpoint change', () => {
    const s = useHostStore.getState()
    const dev = s.hostOrder[0]
    s.setDevHost(dev)
    s.updateHost(dev, { ip: '10.9.9.9', port: 4242 })
    const id = selectDevHostId(useHostStore.getState())
    expect(id).toBe(dev)
    expect(useHostStore.getState().getDaemonBase(id!)).toBe('http://10.9.9.9:4242')
  })
})

describe('findHostByEndpoint', () => {
  const hosts = {
    a: { id: 'a', name: 'ts', ip: '100.64.0.4', port: 7860, order: 0 },
    b: { id: 'b', name: 'lo', ip: '127.0.0.1', port: 7860, order: 1 },
  }
  it('matches exact ip and port', () => {
    expect(findHostByEndpoint(hosts, '100.64.0.4', 7860)?.id).toBe('a')
    expect(findHostByEndpoint(hosts, '100.64.0.4', 7861)).toBeUndefined()
  })
  it('treats loopback and Tailscale IP as distinct endpoints', () => {
    expect(findHostByEndpoint(hosts, '127.0.0.1', 7860)?.id).toBe('b')
    expect(findHostByEndpoint({ a: hosts.a }, '127.0.0.1', 7860)).toBeUndefined()
  })
})
```

Append to `spa/src/lib/sync/contributors/hosts.test.ts` (inside the top-level describe, after the existing full-replace test; `selectDevHostId` import from `'../../../stores/useHostStore'`):

```ts
  it('full-replace that drops the chosen dev host makes selectDevHostId read null', () => {
    const state = useHostStore.getState()
    const dev = state.hostOrder[0]
    state.setDevHost(dev)
    const incoming: FullPayload = {
      version: 1,
      data: {
        hosts: { 'h-1': { id: 'h-1', name: 'remote-host', ip: '10.0.0.1', port: 8080, order: 0 } },
        hostOrder: ['h-1'],
        activeHostId: 'h-1',
      },
    }
    contributor.deserialize(incoming, { type: 'full-replace' })
    expect(selectDevHostId(useHostStore.getState())).toBeNull()
  })

  it('serialize does not carry devHostId', () => {
    const state = useHostStore.getState()
    state.setDevHost(state.hostOrder[0])
    const payload = contributor.serialize() as FullPayload
    expect('devHostId' in payload.data).toBe(false)
  })
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd spa && npx vitest run src/stores/useHostStore.test.ts src/lib/sync/contributors/hosts.test.ts`
Expected: FAIL — `selectDevHostId is not a function` / `setDevHost is not a function`.

- [ ] **Step 3: Implement** in `spa/src/stores/useHostStore.ts`:

In `interface HostState` add after `activeHostId`:
```ts
  /** Host the Development page targets. Device-local, not synced (spec D3). */
  devHostId: string | null
```
and after `setActiveHost`:
```ts
  setDevHost: (hostId: string | null) => void
```

In `createDefaultState()` return add `devHostId: null as string | null,`.

Add the two exported helpers right above `export const useHostStore`:
```ts
/** Exact-endpoint lookup shared by registerLocalHost and the Local daemon UI
 *  (spec §3.3). Strict ip+port equality — 127.0.0.1 and a Tailscale IP are
 *  two endpoints, never merged. */
export function findHostByEndpoint(hosts: Record<string, HostConfig>, ip: string, port: number): HostConfig | undefined {
  return Object.values(hosts).find((h) => h.ip === ip && h.port === port)
}

/** Dev host as the Development page must see it: null unless the persisted
 *  id still resolves to a host (deleted locally or dropped by a sync
 *  full-replace → unset, user re-picks). Pure so hooks can pass it directly. */
export function selectDevHostId(state: Pick<HostState, 'devHostId' | 'hosts'>): string | null {
  return state.devHostId !== null && state.hosts[state.devHostId] ? state.devHostId : null
}
```

In `registerLocalHost` replace
```ts
        const existing = Object.values(get().hosts).find((h) => h.ip === ip && h.port === port)
```
with
```ts
        const existing = findHostByEndpoint(get().hosts, ip, port)
```

In `removeHost`, inside the returned object add:
```ts
            devHostId: state.devHostId === hostId ? null : state.devHostId,
```

After `setActiveHost` add:
```ts
      setDevHost: (hostId) =>
        set((state) => (hostId === null || state.hosts[hostId] ? { devHostId: hostId } : state)),
```

In `partialize` add `devHostId: state.devHostId,`.

- [ ] **Step 4: Run to verify they pass**

Run: `cd spa && npx vitest run src/stores/useHostStore.test.ts src/stores/useHostStore.registerLocalHost.test.ts src/lib/sync/contributors/hosts.test.ts`
Expected: all PASS (registerLocalHost tests prove the helper swap is behaviour-preserving).

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useHostStore.ts spa/src/stores/useHostStore.test.ts spa/src/lib/sync/contributors/hosts.test.ts
git commit -m "feat(spa): persisted devHostId + selectDevHostId / findHostByEndpoint helpers"
```

---

### Task 2: Electron status carries the config token and hostname

**Files:**
- Modify: `electron/local-daemon/types.ts:13`, `electron/local-daemon/index.ts:170-183`, `spa/src/types/electron.d.ts:54-64`
- Test: `electron/local-daemon/index.test.ts:153`

**Interfaces:**
- Produces: `LocalDaemonStatus.config: { bind: string; port: number; token: string | null } | null`, `LocalDaemonStatus.hostname: string` (required). Same shape on `ElectronLocalDaemonStatus` in the renderer.

- [ ] **Step 1: Write the failing test** — in `electron/local-daemon/index.test.ts` change line 153 to:

```ts
    expect(st.config).toEqual({ bind: '100.64.0.9', port: 7860, token: 'purdex_x' })
    expect(st.hostname).toBe('air-2026')
```
and in the `none` status test (the first `status()` test, the one asserting `st.managed === 'none'`) add:
```ts
    expect(st.hostname).toBe('air-2026')
    expect(st.config).toBeNull()
```
and in `external with custom data_dir` (line ~212 — an early-return branch) add:
```ts
    expect(st.hostname).toBe('air-2026')
```

- [ ] **Step 2: Run to verify it fails**

Run: `npx vitest run electron/local-daemon/index.test.ts`
Expected: FAIL — `hasToken: true` ≠ `token: 'purdex_x'`, `hostname` undefined.

- [ ] **Step 3: Implement**

`electron/local-daemon/types.ts` — replace the `config` line and add `hostname`:
```ts
  config: { bind: string; port: number; token: string | null } | null
  /** os.hostname(); registerLocalHost needs it, so it is required. */
  hostname: string
```

`electron/local-daemon/index.ts` `statusUnlocked()` — the `base` object becomes:
```ts
    const base = {
      binPath, installed, running, target: tgt, tools: { tmux }, hostname: deps.hostname(),
      config: cfgFile ? { bind: cfgFile.bind, port: cfgFile.port, token: cfgFile.token } : null,
    }
```

`spa/src/types/electron.d.ts` — in `ElectronLocalDaemonStatus`:
```ts
  config: { bind: string; port: number; token: string | null } | null
  hostname: string
```

- [ ] **Step 4: Run to verify it passes**

Run: `npx vitest run electron/local-daemon/index.test.ts && cd spa && npx tsc --noEmit -p tsconfig.app.json`
Expected: electron tests PASS. `tsc` will complain about `LocalDaemonSection.test.tsx` — fix both spots in that file now so the tree type-checks (the UI change itself is Task 5):
- the `status()` fixture: add `hostname: 'air-2026',` after `config: null,`
- line ~97 (`external without running info…`): `config: { bind: '100.64.0.9', port: 7860, hasToken: true }` → `config: { bind: '100.64.0.9', port: 7860, token: 'purdex_t' }`
Re-run `npx tsc --noEmit -p tsconfig.app.json` (no pipe — the exit code must be 0).

- [ ] **Step 5: Commit**

```bash
git add electron/local-daemon/types.ts electron/local-daemon/index.ts electron/local-daemon/index.test.ts spa/src/types/electron.d.ts spa/src/components/settings/LocalDaemonSection.test.tsx
git commit -m "feat(electron): local-daemon status exposes config token and hostname"
```

---

### Task 3: Development page — host picker and no-fallback gating

**Files:**
- Modify: `spa/src/components/settings/DevEnvironmentSection.tsx`, `spa/src/components/settings/LocalDaemonSection.tsx` (prop + Install/Update gating only)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`
- Test: `spa/src/components/settings/DevEnvironmentSection.test.tsx`, `spa/src/components/settings/LocalDaemonSection.test.tsx`

**Interfaces:**
- Consumes: `selectDevHostId`, `setDevHost`, `devHostId` (Task 1).
- Produces: `daemonBase: string | null` passed to `<LocalDaemonSection>`, whose prop type changes to `string | null` in this task (with its Install/Update gating), so no-fallback holds from this commit on.

i18n keys (add to both locale files):

| key | en | zh-TW |
|---|---|---|
| `settings.dev.host.label` | Development host | 開發主機 |
| `settings.dev.host.none` | — not set — | — 未指定 — |
| `settings.dev.host.required` | Pick a development host first. Update checks, daemon rebuild and local-daemon install all come from it. | 請先選擇開發主機。更新檢查、daemon 重建與本機 daemon 安裝來源都取自這台。 |

- [ ] **Step 1: Write the failing tests**

In `DevEnvironmentSection.test.tsx`, the existing tests assume the first host is the source. Add to the top-level `beforeEach` (after the `window.electronAPI = …` block):
```ts
  useHostStore.getState().reset()
  useHostStore.getState().setDevHost(useHostStore.getState().hostOrder[0])
```
(Do the same inside the `DevEnvironmentSection - Daemon block` describe's `beforeEach` — or rely on the outer one; the outer `beforeEach` already runs for that describe, so nothing extra is needed there.)

Change the existing test `renders section title` to target the heading only (the new picker label also matches `/Development/`):
```ts
    expect(screen.getByRole('heading', { name: /Development Environment|開發環境/ })).toBeTruthy()
```

Change the existing test `restarts the stream when daemonBase changes` to use the dev host id: replace `const hostId = useHostStore.getState().hostOrder[0]` with `const hostId = selectDevHostId(useHostStore.getState())!` (import `selectDevHostId`).

Add a new describe:
```ts
describe('DevEnvironmentSection - dev host picker', () => {
  it('renders the picker with every host and the current selection', async () => {
    const extra = useHostStore.getState().addHost({ name: 'air', ip: '100.64.0.4', port: 7860 })
    await act(async () => { render(<DevEnvironmentSection />) })
    const select = screen.getByLabelText('Development host') as HTMLSelectElement
    expect(select.value).toBe(useHostStore.getState().hostOrder[0])
    expect(screen.getByRole('option', { name: 'air (100.64.0.4:7860)' })).toBeTruthy()
    expect(screen.getByRole('option', { name: '— not set —' })).toBeTruthy()
    fireEvent.change(select, { target: { value: extra } })
    expect(useHostStore.getState().devHostId).toBe(extra)
  })

  it('with no dev host: shows the notice, makes no requests, disables the buttons', async () => {
    useHostStore.getState().setDevHost(null)
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(mockGetAppInfo).toHaveBeenCalled())
    expect(screen.getByText(/Pick a development host first/)).toBeTruthy()
    expect(mockStreamCheck).not.toHaveBeenCalled()
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Check Update' })).toBeDisabled()          // daemon block
    expect(screen.getByRole('button', { name: 'Rebuild & Restart' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Check for Updates' })).toBeDisabled()      // app block
    fetchSpy.mockRestore()
  })

  it('picking a host starts the check against that host', async () => {
    useHostStore.getState().setDevHost(null)
    const extra = useHostStore.getState().addHost({ name: 'air', ip: '100.64.0.4', port: 7860, token: 'tok-air' })
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(mockGetAppInfo).toHaveBeenCalled())
    expect(mockStreamCheck).not.toHaveBeenCalled()
    await act(async () => { fireEvent.change(screen.getByLabelText('Development host'), { target: { value: extra } }) })
    await waitFor(() => expect(mockStreamCheck).toHaveBeenCalledWith('http://100.64.0.4:7860', 'tok-air', expect.any(Function)))
  })
})
```
Button names are the exact `en.json` values: app block `Check for Updates`, daemon block `Check Update` / `Rebuild & Restart`.

In `LocalDaemonSection.test.tsx`: change `renderIt` to accept a base and add the gating test:
```ts
const renderIt = (latestHash: string | null = 'bbb', refreshKey: unknown = { latest_hash: latestHash }, daemonBase: string | null = 'http://100.64.0.2:7860') =>
  act(async () => { render(<LocalDaemonSection daemonBase={daemonBase} token="tok" latestHash={latestHash} refreshKey={refreshKey} />) })
```
```ts
describe('LocalDaemonSection - no dev host', () => {
  it('Install disabled (none) and Update disabled (managed, stale); Start still enabled', async () => {
    mockStatus.mockResolvedValue(status())
    await renderIt('bbb', { latest_hash: 'bbb' }, null)
    expect(screen.getByRole('button', { name: 'Install' })).toBeDisabled()
    cleanup()
    mockStatus.mockResolvedValue(status({ managed: 'managed', installed: { version: '9', hash: 'aaa', goos: 'darwin', goarch: 'arm64' } }))
    await renderIt('bbb', { latest_hash: 'bbb' }, null)
    expect(screen.getByRole('button', { name: 'Update' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Start' })).not.toBeDisabled()
    expect(mockInstall).not.toHaveBeenCalled()
  })
})
```
(`cleanup` comes from `@testing-library/react`; add it to that import.)

- [ ] **Step 2: Run to verify they fail**

Run: `cd spa && npx vitest run src/components/settings/DevEnvironmentSection.test.tsx src/components/settings/LocalDaemonSection.test.tsx`
Expected: new tests FAIL (`Unable to find a label with the text of: Development host`; Install not disabled); existing ones still pass.

- [ ] **Step 3: Implement** in `DevEnvironmentSection.tsx`

Imports: add `selectDevHostId` to the `useHostStore` import.

Replace lines 34–36:
```ts
  const devHostId = useHostStore(selectDevHostId)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const setDevHost = useHostStore((s) => s.setDevHost)
  // null = no dev host chosen: no dev requests at all, no fallback (spec D2).
  const daemonBase: string | null = useHostStore((s) => {
    const id = selectDevHostId(s)
    return id ? s.getDaemonBase(id) : null
  })
  const token = useHostStore((s) => {
    const id = selectDevHostId(s)
    return id ? (s.hosts[id]?.token ?? undefined) : undefined
  })
```

Guard the three entry points that pass `daemonBase` to Electron:
- `checkUpdate`: first line inside the callback `if (!daemonBase) return` (before `closeStream()`).
- `handleUpdate`: first line `if (!daemonBase) return`.
- `checkDaemon` / `rebuildDaemon` already return on `!daemonBase`.

Mount/check effect becomes:
```ts
  useEffect(() => {
    if (!appInfo || !daemonBase) return
    checkUpdateRef.current()
  }, [appInfo, daemonBase, token])
```

Render — insert as the first child of the `space-y-3` block (above the SPA-source row):
```tsx
        <div className="flex items-center justify-between">
          <label htmlFor="dev-host-picker" className="text-sm text-text-primary">{t('settings.dev.host.label')}</label>
          <select
            id="dev-host-picker"
            value={devHostId ?? ''}
            onChange={(e) => setDevHost(e.target.value || null)}
            className="text-xs rounded bg-surface-input border border-border-default text-text-primary px-2 py-1 disabled:opacity-50"
          >
            <option value="">{t('settings.dev.host.none')}</option>
            {hostOrder.map((id) => {
              const h = hosts[id]
              return h ? <option key={id} value={id}>{`${h.name} (${h.ip}:${h.port})`}</option> : null
            })}
          </select>
        </div>
        {devHostId === null && (
          <div className="text-xs text-status-warning border border-status-warning/40 bg-status-warning/10 rounded p-2">
            {t('settings.dev.host.required')}
          </div>
        )}
```

Button `disabled` props — add `!daemonBase ||` to: app *Check* (`disabled={!appInfo || !daemonBase || status === 'checking' || status === 'building'}`), app *Update* (`disabled={updating || !daemonBase}`), daemon *Check* and daemon *Rebuild* (`disabled={!daemonBase || daemonPhase === …}`).

`<LocalDaemonSection daemonBase={daemonBase} …>` — and in `LocalDaemonSection.tsx`: prop `daemonBase: string | null`; the Install and Update buttons both become
```tsx
onClick={() => void run('install', () => daemonBase ? api.localDaemonInstall?.(daemonBase, token) : undefined)}
disabled={disabled || daemonBase === null}
title={daemonBase === null ? t('settings.dev.host.required') : undefined}
```
Start / Restart / Refresh are untouched.

- [ ] **Step 4: Run to verify they pass**

Run: `cd spa && npx vitest run src/components/settings src/locales && pnpm run lint`
Expected: PASS, lint clean.

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/DevEnvironmentSection.tsx spa/src/components/settings/DevEnvironmentSection.test.tsx spa/src/components/settings/LocalDaemonSection.tsx spa/src/components/settings/LocalDaemonSection.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "feat(spa): Development page targets an explicitly chosen dev host, no fallback"
```

---

### Task 4: Development page — source-change reset, generation guard, picker lock

**Files:**
- Modify: `spa/src/components/settings/DevEnvironmentSection.tsx`
- Test: `spa/src/components/settings/DevEnvironmentSection.test.tsx`

**Interfaces:**
- Consumes: Task 3's `daemonBase | null`, picker `#dev-host-picker`.
- Produces: nothing new externally; `latestHash` passed to `LocalDaemonSection` becomes `null` on every source change because `daemonCheck` is reset.

- [ ] **Step 1: Write the failing tests** — add a describe to `DevEnvironmentSection.test.tsx`:

```ts
describe('DevEnvironmentSection - source change discipline', () => {
  const originalFetch = globalThis.fetch
  afterEach(() => { globalThis.fetch = originalFetch; vi.useRealTimers() })

  function deferred<T>() {
    let resolve!: (v: T) => void
    const promise = new Promise<T>((r) => { resolve = r })
    return { promise, resolve }
  }

  const checkJson = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })

  it('A → unset: A\'s painted daemon check is cleared', async () => {
    globalThis.fetch = vi.fn(async (url: string | URL) =>
      String(url).endsWith('/api/dev/daemon/check') ? checkJson({ current_hash: 'aaa', latest_hash: 'bbb', available: true }) : new Response('{}', { status: 200 })) as typeof globalThis.fetch
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(screen.getByText('aaa')).toBeTruthy()) // A's result is on screen first
    await act(async () => { useHostStore.getState().setDevHost(null) })
    expect(screen.queryByText('aaa')).toBeNull()
    expect(screen.queryByText('Current hash')).toBeNull()
  })

  it('A → unset: A\'s late daemon-check response is discarded', async () => {
    const d = deferred<Response>()
    globalThis.fetch = vi.fn(async (url: string | URL) =>
      String(url).endsWith('/api/dev/daemon/check') ? d.promise : new Response('{}', { status: 200 })) as typeof globalThis.fetch
    await act(async () => { render(<DevEnvironmentSection />) })
    await act(async () => { useHostStore.getState().setDevHost(null) })
    await act(async () => { d.resolve(checkJson({ current_hash: 'aaa', latest_hash: 'bbb', available: true })) })
    expect(screen.queryByText('aaa')).toBeNull()
    expect(screen.queryByText('Current hash')).toBeNull()
  })

  it('A → unset: A\'s late rebuild 409 does not paint an error', async () => {
    const d = deferred<Response>()
    globalThis.fetch = vi.fn(async (url: string | URL, init?: RequestInit) => {
      const href = String(url)
      if (href.endsWith('/api/dev/daemon/rebuild') && init?.method === 'POST') return d.promise
      if (href.endsWith('/api/dev/daemon/check')) return checkJson({ current_hash: 'a', latest_hash: 'a', available: false })
      return new Response('{}', { status: 200 })
    }) as typeof globalThis.fetch
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Rebuild & Restart' })).not.toBeDisabled())
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Rebuild & Restart' })) })
    await act(async () => { useHostStore.getState().setDevHost(null) })
    await act(async () => { d.resolve(new Response('', { status: 409 })) })
    expect(screen.queryByText('Rebuild already in progress')).toBeNull()
  })

  it('picker is disabled while a daemon rebuild is in flight', async () => {
    const d = deferred<Response>()
    globalThis.fetch = vi.fn(async (url: string | URL, init?: RequestInit) => {
      const href = String(url)
      if (href.endsWith('/api/dev/daemon/rebuild') && init?.method === 'POST') return d.promise
      if (href.endsWith('/api/dev/daemon/check')) return checkJson({ current_hash: 'a', latest_hash: 'a', available: false })
      return new Response('{}', { status: 200 })
    }) as typeof globalThis.fetch
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Rebuild & Restart' })).not.toBeDisabled())
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Rebuild & Restart' })) })
    expect(screen.getByLabelText('Development host')).toBeDisabled()
  })

  it('A → B: a late "done" from A\'s stream does not paint B\'s view', async () => {
    arrangeStream() // hold A's stream open
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(mockStreamCheck).toHaveBeenCalledTimes(1))
    const cbA = lastStreamCallback!
    const closeA = lastStreamClose
    const b = useHostStore.getState().addHost({ name: 'b', ip: '10.0.0.2', port: 7860 })
    arrangeStream() // B's stream, also held open
    await act(async () => { useHostStore.getState().setDevHost(b) })
    await waitFor(() => expect(mockStreamCheck).toHaveBeenCalledTimes(2))
    expect(closeA).toHaveBeenCalled()
    await act(async () => { cbA({ type: 'done', check: baseCheck({ spaHash: 'zzz9999' }) }) })
    expect(screen.queryByText(/zzz9999/)).toBeNull()
    expect(screen.queryByText(/Update available/)).toBeNull()
  })

  it('post-rebuild 3 s re-check is cancelled by a source change', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const fetchMock = vi.fn(async (url: string | URL, init?: RequestInit) => {
      const href = String(url)
      if (href.endsWith('/api/dev/daemon/rebuild') && init?.method === 'POST') {
        return new Response('data: {"type":"success","new_hash":"n1"}\n\n', { status: 200 })
      }
      if (href.endsWith('/api/dev/daemon/check')) {
        return new Response(JSON.stringify({ current_hash: 'a', latest_hash: 'a', available: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      return new Response('{}', { status: 200 })
    })
    globalThis.fetch = fetchMock as typeof globalThis.fetch
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(fetchMock.mock.calls.some(([u]) => String(u).endsWith('/api/dev/daemon/check'))).toBe(true))
    const checksBefore = () => fetchMock.mock.calls.filter(([u]) => String(u).endsWith('/api/dev/daemon/check')).length
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Rebuild & Restart' })) })
    await waitFor(() => expect(screen.getByText(/Build complete/)).toBeTruthy())
    const n = checksBefore()
    await act(async () => { useHostStore.getState().setDevHost(null) })
    await act(async () => { await vi.advanceTimersByTimeAsync(3500) })
    expect(checksBefore()).toBe(n)
  })

  it('picker is disabled while an app update is running', async () => {
    arrangeStream((cb) => {
      cb({ type: 'check', check: baseCheck({ electronHash: 'newhash' }) })
      cb({ type: 'done', check: baseCheck({ electronHash: 'newhash' }) })
    })
    mockApplyUpdate.mockReturnValue(new Promise(() => {}))
    await act(async () => { render(<DevEnvironmentSection />) })
    const update = await screen.findByRole('button', { name: 'Update App' })
    await act(async () => { fireEvent.click(update) })
    expect(screen.getByLabelText('Development host')).toBeDisabled()
  })
})
```
The last test's button label is the exact `en.json` value of `settings.dev.btn.update_app` (`Update App`); after the click it flips to `Updating...`, so query it before clicking.

- [ ] **Step 2: Run to verify they fail**

Run: `cd spa && npx vitest run src/components/settings/DevEnvironmentSection.test.tsx -t "source change"`
Expected: first three FAIL (stale text present / extra check call); the picker test FAILS (not disabled).

- [ ] **Step 3: Implement** in `DevEnvironmentSection.tsx`

Add refs next to `streamCloseRef`:
```ts
  // Spec §2.2: every dev request belongs to a "source generation". A source
  // change (host / token) bumps it; anything that started under an older
  // generation drops its result instead of painting the new host's view.
  const sourceGenRef = useRef(0)
  const daemonTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
```

`checkDaemon` — capture and check:
```ts
  const checkDaemon = useCallback(async () => {
    if (!daemonBase) return
    const gen = sourceGenRef.current
    setDaemonPhase('checking')
    setDaemonError(null)
    try {
      const res = await fetch(`${daemonBase}/api/dev/daemon/check`, { headers: daemonAuthHeaders() })
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = (await res.json()) as DaemonCheck
      if (gen !== sourceGenRef.current) return
      setDaemonCheck(data)
      setDaemonPhase('idle')
    } catch (err) {
      if (gen !== sourceGenRef.current) return
      setDaemonError(err instanceof Error ? err.message : String(err))
      setDaemonPhase('error')
    }
  }, [daemonBase, daemonAuthHeaders])
```

`rebuildDaemon` — `const gen = sourceGenRef.current` right after the `if (!daemonBase) return`; **immediately after `const res = await fetch(…)` and before the `409` / `!res.ok` branches** add `if (gen !== sourceGenRef.current) return` (a late 409/500 from the old host must not paint an error); in the reader loop, first statement after `const { done, value } = await reader.read()`:
```ts
        if (gen !== sourceGenRef.current) { void reader.cancel().catch(() => {}); return }
```
and replace the post-stream timer with:
```ts
      if (!encounteredError) {
        daemonTimerRef.current = setTimeout(() => {
          daemonTimerRef.current = null
          if (gen === sourceGenRef.current) void checkDaemon()
        }, 3000)
      }
```
Also wrap the two `setDaemonError`/`setDaemonPhase('error')` calls in the outer `catch` with `if (gen !== sourceGenRef.current) return`.

`checkUpdate` — after the `if (!daemonBase) return` add `const gen = sourceGenRef.current`, and make the first line of the `streamCheck` callback `if (gen !== sourceGenRef.current) return`.

Add the reset effect **immediately before** the `useEffect(() => { void checkDaemon() }, [checkDaemon])` effect (effects run in declaration order, so the reset lands before any check):
```ts
  // Source change: wipe everything the previous host produced before the
  // check effects below fire (spec §2.2 steps 1–3).
  useEffect(() => {
    sourceGenRef.current += 1
    if (daemonTimerRef.current) { clearTimeout(daemonTimerRef.current); daemonTimerRef.current = null }
    closeStream()
    setRemoteInfo(null)
    setStatus('idle')
    setUpdateError(null)
    setBuildEvents([])
    setDaemonCheck(null)
    setDaemonLog([])
    setDaemonError(null)
    setDaemonPhase('idle')
  }, [daemonBase, token, closeStream])
```
Today `useEffect(() => { void checkDaemon() }, [checkDaemon])` is at line ~154 and `const closeStream = useCallback(…, [])` at ~158. Move `closeStream` above the `checkDaemon` effect, put the reset effect between them, so the order is: `closeStream` → reset effect → `checkDaemon` effect → (later) the `[appInfo, daemonBase, token]` check effect. `closeStream` has no deps, so hoisting it is safe. Also add an unmount cleanup for the timer next to the existing `useEffect(() => () => closeStream(), [closeStream])`:
```ts
  useEffect(() => () => { if (daemonTimerRef.current) clearTimeout(daemonTimerRef.current) }, [])
```

Picker lock — compute above the return:
```ts
  const sourceLocked = updating || daemonPhase === 'rebuilding' || daemonPhase === 'restarting'
```
and add `disabled={sourceLocked}` to the `<select id="dev-host-picker">`.

- [ ] **Step 4: Run to verify they pass**

Run: `cd spa && npx vitest run src/components/settings/DevEnvironmentSection.test.tsx && pnpm run lint`
Expected: all PASS, including the pre-existing `restarts the stream when daemonBase changes`.

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/DevEnvironmentSection.tsx spa/src/components/settings/DevEnvironmentSection.test.tsx
git commit -m "fix(spa): dev page resets and fences stale responses on dev-host change"
```

---

### Task 5: Local daemon block — URL, token, add-to-hosts, gated Install/Update

**Files:**
- Modify: `spa/src/components/settings/LocalDaemonSection.tsx`, `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`
- Test: `spa/src/components/settings/LocalDaemonSection.test.tsx`

**Interfaces:**
- Consumes: `findHostByEndpoint`, `registerLocalHost` (Task 1); `status.config.token`, `status.hostname` (Task 2).
- Consumes: prop `daemonBase: string | null` and the Install/Update gating (already done in Task 3).

i18n keys (both files):

| key | en | zh-TW |
|---|---|---|
| `settings.dev.local.url` | URL | 網址 |
| `settings.dev.local.token` | Token | Token |
| `settings.dev.local.token_missing` | No token in config.toml | config.toml 沒有 token |
| `settings.dev.local.host_list` | Host list | Host 清單 |
| `settings.dev.local.in_hosts` | Registered as {{name}} | 已登記為 {{name}} |
| `settings.dev.local.copied` | Copied | 已複製 |
| `settings.dev.local.btn.add_host` | Add to hosts | 加入 Host |
| `settings.dev.local.btn.reveal` | Show token | 顯示 token |
| `settings.dev.local.btn.hide` | Hide token | 隱藏 token |
| `settings.dev.local.btn.copy` | Copy token | 複製 token |

- [ ] **Step 1: Write the failing tests** — in `LocalDaemonSection.test.tsx`:

Add to `beforeEach`:
```ts
  Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn().mockResolvedValue(undefined) }, configurable: true })
```
Add a describe:
```ts
describe('LocalDaemonSection - config rows', () => {
  const cfg = { bind: '100.64.0.9', port: 7860, token: 'purdex_secret' }

  it('shows URL and a masked token; reveal and copy work', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'x', config: cfg }))
    await renderIt()
    expect(screen.getByText('http://100.64.0.9:7860')).toBeTruthy()
    expect(screen.queryByText('purdex_secret')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Show token' }))
    expect(screen.getByText('purdex_secret')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Hide token' }))
    expect(screen.queryByText('purdex_secret')).toBeNull()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Copy token' })) })
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('purdex_secret')
    expect(screen.getByText('Copied')).toBeTruthy()
  })

  it('token missing → notice, Add to hosts disabled', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', config: { ...cfg, token: null } }))
    await renderIt()
    expect(screen.getByText('No token in config.toml')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Add to hosts' })).toBeDisabled()
  })

  it('endpoint not in host list → Add to hosts registers it with the config token and hostname', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'x', config: cfg }))
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Add to hosts' })) })
    const added = Object.values(useHostStore.getState().hosts).find((h) => h.ip === '100.64.0.9' && h.port === 7860)
    expect(added).toMatchObject({ name: 'air-2026', token: 'purdex_secret' })
    expect(screen.getByText('Registered as air-2026')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Add to hosts' })).toBeNull()
  })

  it('endpoint already in host list → shows the host name, no button', async () => {
    useHostStore.getState().addHost({ name: 'my-air', ip: '100.64.0.9', port: 7860, token: 't' })
    mockStatus.mockResolvedValue(status({ managed: 'managed', config: cfg }))
    await renderIt()
    expect(screen.getByText('Registered as my-air')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Add to hosts' })).toBeNull()
  })

  it('loopback bind is a different endpoint from the Tailscale host entry', async () => {
    useHostStore.getState().addHost({ name: 'my-air', ip: '100.64.0.9', port: 7860, token: 't' })
    mockStatus.mockResolvedValue(status({ managed: 'managed', config: { ...cfg, bind: '127.0.0.1' } }))
    await renderIt()
    expect(screen.getByRole('button', { name: 'Add to hosts' })).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd spa && npx vitest run src/components/settings/LocalDaemonSection.test.tsx`
Expected: new tests FAIL (`Unable to find … 'Show token'`), old tests pass.

- [ ] **Step 3: Implement** in `LocalDaemonSection.tsx`

Imports:
```ts
import { Copy, Eye, EyeSlash } from '@phosphor-icons/react'
import { findHostByEndpoint, useHostStore } from '../../stores/useHostStore'
```
State / derived, after the existing `useState` lines:
```ts
  const hosts = useHostStore((s) => s.hosts)
  const [revealed, setRevealed] = useState(false)
  const [copied, setCopied] = useState(false)
  const cfg = status?.config ?? null
  const cfgUrl = cfg ? `http://${cfg.bind}:${cfg.port}` : null
  // Spec §3.3: exact-endpoint membership only, via the same helper registerLocalHost uses.
  const registeredAs = cfg ? findHostByEndpoint(hosts, cfg.bind, cfg.port) : undefined

  const copyToken = useCallback(async () => {
    if (!cfg?.token) return
    await navigator.clipboard.writeText(cfg.token)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }, [cfg])

  const addToHosts = useCallback(() => {
    if (!status || !cfg?.token || !cfgUrl) return
    registerLocalHost({ url: cfgUrl, token: cfg.token, hostname: status.hostname })
    setNotice(t('settings.dev.local.registered', { name: status.hostname }))
  }, [status, cfg, cfgUrl, registerLocalHost, t])
```
Also `setRevealed(false)` inside `refresh()` after `setStatus(...)` so a re-query re-masks.

Render — inside the `{status && (<div className="space-y-1 …">…)}` block, right **before** the `{status.tools.tmux === null && …}` line:
```tsx
          {cfg && (
            <>
              <div className="flex items-center justify-between">
                <span>{t('settings.dev.local.url')}</span>
                <span className="font-mono text-text-primary">{cfgUrl}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('settings.dev.local.token')}</span>
                {cfg.token === null ? (
                  <span className="text-status-warning">{t('settings.dev.local.token_missing')}</span>
                ) : (
                  <span className="flex items-center gap-1">
                    <span className="font-mono text-text-primary">{revealed ? cfg.token : '••••••••••••'}</span>
                    <button type="button" onClick={() => setRevealed((v) => !v)} aria-label={revealed ? t('settings.dev.local.btn.hide') : t('settings.dev.local.btn.reveal')} className="p-0.5 rounded hover:bg-surface-hover cursor-pointer">
                      {revealed ? <EyeSlash size={14} /> : <Eye size={14} />}
                    </button>
                    <button type="button" onClick={() => void copyToken()} aria-label={t('settings.dev.local.btn.copy')} className="p-0.5 rounded hover:bg-surface-hover cursor-pointer">
                      <Copy size={14} />
                    </button>
                    {copied && <span>{t('settings.dev.local.copied')}</span>}
                  </span>
                )}
              </div>
              <div className="flex items-center justify-between">
                <span>{t('settings.dev.local.host_list')}</span>
                {registeredAs ? (
                  <span className="text-text-primary">{t('settings.dev.local.in_hosts', { name: registeredAs.name })}</span>
                ) : (
                  <button type="button" onClick={addToHosts} disabled={disabled || cfg.token === null} className={btnSecondary}>
                    {t('settings.dev.local.btn.add_host')}
                  </button>
                )}
              </div>
            </>
          )}
```
Note `disabled` is declared below the `if (!api?.localDaemonStatus) return null` line today; keep hook calls above that early return and only *use* `disabled` in JSX (as the file already does).

- [ ] **Step 4: Run to verify they pass**

Run: `cd spa && npx vitest run src/components/settings src/locales src/stores && pnpm run lint && pnpm run build`
Expected: all PASS, lint clean, build OK.

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/LocalDaemonSection.tsx spa/src/components/settings/LocalDaemonSection.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "feat(spa): local daemon block shows URL/token and can add its endpoint to hosts"
```

---

### Task 6: Full verification

- [ ] `cd spa && npx vitest run` — whole SPA suite green.
- [ ] `npx vitest run electron/local-daemon` from repo root — green.
- [ ] `cd spa && pnpm run lint && pnpm run build` — clean.
- [ ] `git log --oneline origin/main..HEAD` shows the spec commits + 5 task commits.

No commit for this task.

---

## Self-review against the spec

| Spec item | Task |
|---|---|
| §2.1 store field, action, pure selector, removeHost, partialize, no migration | 1 |
| D3 not synced / dangling id → unset; sync full-replace test | 1 |
| §2.2 picker, null notice, no requests, buttons disabled | 3 |
| §2.2 source-change discipline steps 1–4 | 4 |
| §2.3 i18n | 3 |
| §3.1 electron token + required hostname | 2 |
| §3.2 renderer type | 2 |
| §3.3 URL/token/reveal/copy, endpoint membership via shared helper, Add to hosts | 5 |
| §3.3 Install/Update gating on `daemonBase === null` (prop nullable) | 3 |
| §3.4 i18n | 5 |
| §4 tests | 1–5 |

## Codex plan review — dispositions (`task-mu0xfbsm-ogzb93`)

| # | Sev | Finding | Disposition |
|---|---|---|---|
| 1 | Important | Rebuild's 409 / `!res.ok` branches bypass the generation check. | Adopted → Task 4 Step 3 (check right after `await fetch`), + deferred-409 test. |
| 2 | Important | Task 2 leaves `hasToken: true` at `LocalDaemonSection.test.tsx:97`; `\| head` hides tsc's exit code. | Adopted → Task 2 Step 4. |
| 3 | Important | New "Development host" label breaks `getByText(/Development/)` in the title test. | Adopted → Task 3 Step 1 (`getByRole('heading')`). |
| 4 | Minor | `daemonBase ?? ''` in Task 3 hands an empty source to Install. | Adopted → nullable prop + Install/Update gating + test moved from Task 5 to Task 3. |
| 5 | Minor | Missing tests: persistence slice, external-branch hostname, rebuilding picker lock; A→unset must show A first. | Adopted → Tasks 1 / 2 / 4. |
