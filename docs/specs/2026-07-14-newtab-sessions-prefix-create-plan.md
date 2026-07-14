# New-Tab Sessions: tab-style indicator + per-host create — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the New-Tab Sessions list the same agent icon/status prefix a tab shows, and let the user create a session per host that attaches straight into the current pane.

**Architecture:** Extract the agent-layer resolution from `useTabDisplay` into a shared `useSessionAgentIndicator` hook; render each session row via a `SessionRow` that reuses `<TabIcon>`. Restructure the host header into a `<div>` carrying a separate collapse button and a `+` button that toggles an inline create form (`createSession()` → guards → `onSelect` into the current pane).

**Tech Stack:** React 19, Zustand 5, Tailwind 4, Vitest + @testing-library/react, Phosphor Icons.

## Global Constraints

- Package manager **pnpm**; tests `cd spa && npx vitest run`; lint `pnpm run lint`; build `pnpm run build` (all from `spa/`).
- No new i18n keys — reuse existing `session.*` / `hosts.*` / `common.*` keys.
- Do not modify the Host page (`components/hosts/SessionsSection.tsx`) or `createSession()` in `lib/host-api.ts`.
- Existing session-**row** offline gate stays `hostRuntime && status !== 'connected'`; only the new `+`/create uses the stricter Host-page rule.
- Spec: `docs/specs/2026-07-14-newtab-sessions-prefix-create-spec.md`.

---

## Phase 1 — G1: tab-style indicator

### Task 1: `useSessionAgentIndicator` hook

**Files:**
- Create: `spa/src/hooks/useSessionAgentIndicator.ts`
- Test: `spa/src/hooks/useSessionAgentIndicator.test.ts`

**Interfaces:**
- Produces:
  - `type TabIconComponent = ComponentType<{ size: number; className?: string }>` (moved here from `useTabDisplay.ts`)
  - `interface SessionAgentIndicator { agentIcon: TabIconComponent | undefined; agentStatus: AgentStatus | undefined; subagentRefs: SubagentRef[]; isUnread: boolean; tabIndicatorStyle: TabIndicatorStyle }`
  - `function useSessionAgentIndicator(hostId: string, sessionCode: string | undefined, opts?: { isTerminated?: boolean }): SessionAgentIndicator`

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/hooks/useSessionAgentIndicator.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useSessionAgentIndicator } from './useSessionAgentIndicator'
import { useAgentStore } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { compositeKey } from '../lib/composite-key'

beforeEach(() => {
  useAgentStore.setState({ statuses: {}, agentTypes: {}, subagents: {}, unread: {} })
  useUISettingsStore.setState({ tabIndicatorStyle: 'iconDot', ccIconVariant: 'default', codexIconVariant: 'default' })
})

describe('useSessionAgentIndicator', () => {
  it('returns empty indicator when sessionCode is undefined', () => {
    const { result } = renderHook(() => useSessionAgentIndicator('h1', undefined))
    expect(result.current.agentIcon).toBeUndefined()
    expect(result.current.agentStatus).toBeUndefined()
    expect(result.current.subagentRefs).toEqual([])
    expect(result.current.isUnread).toBe(false)
    expect(result.current.tabIndicatorStyle).toBe('iconDot')
  })

  it('resolves agent icon + status + refs + unread for a live session', () => {
    const ck = compositeKey('h1', 's1')
    useAgentStore.setState({
      statuses: { [ck]: 'running' },
      agentTypes: { [ck]: 'claude' },
      subagents: { [ck]: [{ id: 'a', status: 'running' } as never] },
      unread: { [ck]: true },
    })
    const { result } = renderHook(() => useSessionAgentIndicator('h1', 's1'))
    expect(result.current.agentStatus).toBe('running')
    expect(result.current.agentIcon).toBeTypeOf('function') // getAgentIcon('claude', ...) resolved a component
    expect(result.current.subagentRefs).toHaveLength(1)
    expect(result.current.isUnread).toBe(true)
  })

  it('suppresses the agent icon when terminated', () => {
    const ck = compositeKey('h1', 's1')
    useAgentStore.setState({ agentTypes: { [ck]: 'claude' } })
    const { result } = renderHook(() => useSessionAgentIndicator('h1', 's1', { isTerminated: true }))
    expect(result.current.agentIcon).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/hooks/useSessionAgentIndicator.test.ts`
Expected: FAIL — module `./useSessionAgentIndicator` not found.

- [ ] **Step 3: Write the hook (extract from `useTabDisplay`)**

```ts
// spa/src/hooks/useSessionAgentIndicator.ts
import type { ComponentType } from 'react'
import type { AgentStatus, SubagentRef } from '../stores/useAgentStore'
import type { TabIndicatorStyle } from '../stores/useUISettingsStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { compositeKey } from '../lib/composite-key'
import { getAgentIcon } from '../lib/agent-icons'

export type TabIconComponent = ComponentType<{ size: number; className?: string }>

const EMPTY_SUBAGENT_REFS: SubagentRef[] = []

export interface SessionAgentIndicator {
  agentIcon: TabIconComponent | undefined
  agentStatus: AgentStatus | undefined
  subagentRefs: SubagentRef[]
  isUnread: boolean
  tabIndicatorStyle: TabIndicatorStyle
}

/**
 * Resolves the agent-layer indicator (icon/status/subagents/unread + the user's
 * tabIndicatorStyle) for a single (hostId, sessionCode). Shared by useTabDisplay
 * (tab bar) and the New-Tab Sessions list so both render an identical prefix.
 */
export function useSessionAgentIndicator(
  hostId: string,
  sessionCode: string | undefined,
  opts?: { isTerminated?: boolean },
): SessionAgentIndicator {
  const isTerminated = opts?.isTerminated ?? false
  const ck = sessionCode && hostId ? compositeKey(hostId, sessionCode) : undefined

  const agentStatus = useAgentStore((s) => (ck ? s.statuses[ck] : undefined))
  const isUnread = useAgentStore((s) => (ck ? !!s.unread[ck] : false))
  const subagentRefs = useAgentStore((s) => (ck ? (s.subagents[ck] ?? EMPTY_SUBAGENT_REFS) : EMPTY_SUBAGENT_REFS))
  const agentType = useAgentStore((s) => (ck ? s.agentTypes[ck] : undefined))
  const tabIndicatorStyle = useUISettingsStore((s) => s.tabIndicatorStyle)
  const ccIconVariant = useUISettingsStore((s) => s.ccIconVariant)
  const codexIconVariant = useUISettingsStore((s) => s.codexIconVariant)

  const agentIcon = !isTerminated && agentType
    ? (getAgentIcon(agentType, { ccVariant: ccIconVariant, codexVariant: codexIconVariant }) as TabIconComponent | undefined)
    : undefined

  return { agentIcon, agentStatus, subagentRefs, isUnread, tabIndicatorStyle }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/hooks/useSessionAgentIndicator.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add spa/src/hooks/useSessionAgentIndicator.ts spa/src/hooks/useSessionAgentIndicator.test.ts
git commit -m "feat(new-tab): add useSessionAgentIndicator hook (shared tab/session prefix resolver)"
```

---

### Task 2: Refactor `useTabDisplay` to consume the hook

**Files:**
- Modify: `spa/src/hooks/useTabDisplay.ts`
- Test: `spa/src/hooks/useTabDisplay.test.ts` (existing 14 stay green; add 1 variant test)

**Interfaces:**
- Consumes: `useSessionAgentIndicator`, `TabIconComponent` (Task 1).

- [ ] **Step 1: Add a variant-update regression test**

Append to `spa/src/hooks/useTabDisplay.test.ts` (adapt store-seed helpers to the file's existing pattern — seed a `tmux-session` tab with `agentTypes[ck]='claude'`):

```ts
it('re-resolves the agent icon when the cc icon variant changes', () => {
  // seed a tmux-session tab whose ck has agentType 'claude' (reuse this file's helpers)
  const first = renderTabDisplay() // -> returns result.current.IconComponent
  useUISettingsStore.setState({ ccIconVariant: 'mono' })
  const second = renderTabDisplay()
  expect(second.IconComponent).not.toBe(first.IconComponent) // variant flows through the extracted hook
})
```

(If the existing file already covers variant flow, keep that instead and skip this addition — do not duplicate.)

- [ ] **Step 2: Run the existing suite to confirm baseline green**

Run: `cd spa && npx vitest run src/hooks/useTabDisplay.test.ts`
Expected: PASS (14 existing; new one FAILS only if variant flow regresses).

- [ ] **Step 3: Refactor `useTabDisplay`**

In `spa/src/hooks/useTabDisplay.ts`:
- Import `useSessionAgentIndicator` and `TabIconComponent` from `./useSessionAgentIndicator`; remove the local `TabIconComponent` definition and the now-unused imports (`getAgentIcon`, and the agent-store selector lines it replaces — keep the `agentType` read used by `paneTitle`).
- Replace the inline `agentStatus` / `isUnread` / `subagentRefs` / `tabIndicatorStyle` / `ccIconVariant` / `codexIconVariant` / `agentIcon` computation with:

```ts
const { agentIcon, agentStatus, subagentRefs, isUnread, tabIndicatorStyle } =
  useSessionAgentIndicator(hostId, sessionCode, { isTerminated })
const subagentCount = subagentRefs.length
```

- Keep: `const agentType = useAgentStore((s) => (ck ? s.agentTypes[ck] : undefined))` (used by the `paneTitle` gate), `dynamicTabName`, `ck`, and `const IconComponent = (agentIcon ?? paneIcon) as TabIconComponent | undefined`.
- The exported `TabDisplayData` interface and return shape are unchanged.

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/hooks/useTabDisplay.test.ts`
Expected: PASS (all, incl. the variant test).

- [ ] **Step 5: Commit**

```bash
git add spa/src/hooks/useTabDisplay.ts spa/src/hooks/useTabDisplay.test.ts
git commit -m "refactor(tabs): useTabDisplay consumes useSessionAgentIndicator (single source of truth)"
```

---

### Task 3: `SessionRow` with `<TabIcon>` prefix

**Files:**
- Modify: `spa/src/components/SessionSection.tsx`
- Test: `spa/src/components/SessionSection.test.tsx`

**Interfaces:**
- Consumes: `useSessionAgentIndicator` (Task 1), `TabIcon` (`components/TabIcon.tsx`).

- [ ] **Step 1: Write the failing test (prefix DOM under a dot style)**

Add to `SessionSection.test.tsx`. Seed `tabIndicatorStyle: 'dot'` + a running agent, assert the status indicator renders (via `TabStatusIndicator`'s output). Use the agent store + composite key:

```ts
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from '../lib/composite-key'

it('renders the tab-style status indicator for a running-agent session', () => {
  useUISettingsStore.setState({ tabIndicatorStyle: 'dot' })
  const ck = compositeKey(HOST_ID, 'abc001')
  useAgentStore.setState({ statuses: { [ck]: 'running' }, agentTypes: { [ck]: 'claude' }, subagents: {}, unread: {} })
  useSessionStore.setState({
    sessions: { [HOST_ID]: [{ code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false }] },
  })
  render(<SessionSection onSelect={mockOnSelect} />)
  // TabStatusIndicator renders a data-testid — assert the running indicator exists.
  expect(screen.getByTestId('tab-status-indicator')).toBeInTheDocument()
})
```

> Before writing, open `components/TabStatusIndicator.tsx` to use its real `data-testid` (or role). If it has none, assert on the rendered agent icon instead: seed no agent and assert the terminal icon is present, then seed an agent and assert an added element. Pick a stable selector that actually exists.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/SessionSection.test.tsx -t "tab-style status indicator"`
Expected: FAIL (no status indicator; row still uses `<TerminalWindow>` only).

- [ ] **Step 3: Extract `SessionRow` and render `<TabIcon>`**

In `SessionSection.tsx`, add a `SessionRow` component (hooks can't run in `.map`) and use it inside the sessions `.map`:

```tsx
import { TabIcon } from './TabIcon'
import { useSessionAgentIndicator } from '../hooks/useSessionAgentIndicator'

function SessionRow({ hostId, session, disabled, onSelect }: {
  hostId: string
  session: Session
  disabled: boolean
  onSelect: NewTabProviderProps['onSelect']
}) {
  const { agentIcon, agentStatus, subagentRefs, isUnread, tabIndicatorStyle } =
    useSessionAgentIndicator(hostId, session.code)
  const IconComponent = agentIcon ?? TerminalWindow
  return (
    <button
      data-session-btn
      className="flex items-center gap-3 px-3 py-2 rounded-md hover:bg-white/10 text-left text-sm text-text-primary cursor-pointer transition-colors disabled:opacity-50 disabled:cursor-not-allowed focus:outline-none focus:ring-1 focus:ring-accent-muted"
      disabled={disabled}
      tabIndex={0}
      onClick={() => onSelect({ kind: 'tmux-session', hostId, sessionCode: session.code, mode: 'terminal', cachedName: session.name, tmuxInstance: '' })}
      onKeyDown={(e) => { /* keep the existing Arrow/j/k handler verbatim */ }}
    >
      <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
        <TabIcon IconComponent={IconComponent} agentStatus={agentStatus} tabIndicatorStyle={tabIndicatorStyle} isActive={false} iconSize={14} subagentRefs={subagentRefs} isUnread={isUnread} />
      </span>
      <span className="truncate">{session.name}</span>
      <span className="text-xs text-text-secondary ml-auto">{session.code}</span>
    </button>
  )
}
```

- Replace the inline `sessions.map((session) => (<button …><TerminalWindow …/>…</button>))` with `sessions.map((session) => <SessionRow key={`${hostId}:${session.code}`} hostId={hostId} session={session} disabled={!!isOffline} onSelect={onSelect} />)`.
- Import `Session` type; keep `TerminalWindow` import (now the fallback icon). Preserve the exact `onKeyDown` handler by moving it into `SessionRow`.

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/SessionSection.test.tsx`
Expected: PASS (new prefix test + all existing).

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/SessionSection.tsx spa/src/components/SessionSection.test.tsx
git commit -m "feat(new-tab): render tab-style agent indicator on Sessions list rows"
```

---

## Phase 2 — G2: per-host create & attach

### Task 4: Always-render host header (div) with separate collapse + `+` buttons

**Files:**
- Modify: `spa/src/components/SessionSection.tsx`
- Test: `spa/src/components/SessionSection.test.tsx`

**Interfaces:**
- Produces: header container per host in `hostOrder` (single- and multi-host); a `+` button `data-testid={`new-session-${hostId}`}`; the collapse button keeps `data-testid={`host-header-${hostId}`}` + `aria-expanded` (multi-host only).
- Consumes: Host-page offline rule `!runtime || runtime.status !== 'connected' || runtime.tmuxState === 'unavailable'`.

- [ ] **Step 1: Update existing tests + add new ones**

In `SessionSection.test.tsx`:
- Replace `shows no sessions message when empty`: with a connected host + zero sessions, the **global** "No sessions available" no longer shows; instead the host's `+` (`new-session-${HOST_ID}`) is present. Keep a separate assertion that with **no hosts at all** (`hostOrder: []`) the global empty message shows.
- Replace `does not show host header for single host`: now assert the single host shows a create affordance — `screen.getByTestId(`new-session-${HOST_ID}`)` is present — and that NO collapse toggle (`host-header-${HOST_ID}`) exists for a single host.
- Add:

```ts
it('renders header + create button for a connected host with zero sessions', () => {
  useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
  useHostStore.setState((s) => ({ runtime: { [HOST_ID]: { status: 'connected', tmuxState: 'available' } as never } }))
  render(<SessionSection onSelect={mockOnSelect} />)
  expect(screen.queryByText('No sessions available')).toBeNull()
  expect(screen.getByTestId(`new-session-${HOST_ID}`)).toBeInTheDocument()
})

it('shows the global empty message only when there are no hosts', () => {
  useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: null })
  render(<SessionSection onSelect={mockOnSelect} />)
  expect(screen.getByText('No sessions available')).toBeInTheDocument()
})

it('disables the create button when the host tmux is unavailable', () => {
  useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
  useHostStore.setState((s) => ({ runtime: { [HOST_ID]: { status: 'connected', tmuxState: 'unavailable' } as never } }))
  render(<SessionSection onSelect={mockOnSelect} />)
  expect(screen.getByTestId(`new-session-${HOST_ID}`)).toBeDisabled()
})

it('clicking the create button does not toggle collapse', () => {
  // multi-host so a collapse toggle exists
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1', port: 7860, order: 0 }, [HOST_B]: { id: HOST_B, name: 'air', ip: '2', port: 7860, order: 1 } },
    hostOrder: [HOST_ID, HOST_B], activeHostId: HOST_ID,
    runtime: { [HOST_ID]: { status: 'connected', tmuxState: 'available' } as never },
  })
  useSessionStore.setState({ sessions: { [HOST_ID]: [{ code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false }] } })
  render(<SessionSection onSelect={mockOnSelect} />)
  fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
  expect(screen.getByTestId(`host-header-${HOST_ID}`)).toHaveAttribute('aria-expanded', 'true') // unchanged
  expect(screen.getByText('dev')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run to verify failures**

Run: `cd spa && npx vitest run src/components/SessionSection.test.tsx`
Expected: FAIL on the new/updated cases.

- [ ] **Step 3: Restructure the header + iteration**

In `SessionSection.tsx`:
- Remove the `hasAnySessions` early-return. Instead: if `hostOrder.length === 0` (no hosts) render the global `t('session.no_sessions')`.
- Iterate `hostOrder` for **every** existing host (drop the "only when sessions exist" implicit gating). For a host with zero sessions, render a subtle empty line `<p className="text-xs text-text-muted px-3 py-1">{t('session.no_sessions')}</p>` under its header (only when expanded).
- Replace the single-`<button>` header with a `<div className="flex items-center gap-1.5 px-3 py-1 mt-1 w-full">`:
  - **Collapse control (multi-host only)**: a `<button data-testid={`host-header-${hostId}`} aria-expanded={isExpanded} onClick={toggle} className="flex items-center gap-1.5 flex-1 min-w-0 cursor-pointer">` wrapping caret + status dot + name (move the existing caret/dot/name markup here verbatim). Single-host: render the same caret-less dot + name as a non-interactive `<span>` (no `host-header-` testid, no collapse).
  - **Create button (always)**: a sibling `<button data-testid={`new-session-${hostId}`} disabled={createDisabled} onClick={() => setCreatingHost((h) => h === hostId ? null : hostId)} className="ml-auto p-1 rounded hover:bg-white/10 text-text-muted hover:text-text-primary cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed" title={t('hosts.new_session')}><Plus size={14} /></button>` where `createDisabled = !hostRuntime || hostRuntime.status !== 'connected' || hostRuntime.tmuxState === 'unavailable'`.
- Add `const [creatingHost, setCreatingHost] = useState<string | null>(null)` (form wiring lands in Task 5; here the button only toggles this state).
- Import `Plus` from `@phosphor-icons/react`.

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/SessionSection.test.tsx`
Expected: PASS (all).

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/SessionSection.tsx spa/src/components/SessionSection.test.tsx
git commit -m "feat(new-tab): per-host header with create button; render hosts with zero sessions"
```

---

### Task 5: `NewTabSessionForm` — create & attach with guards

**Files:**
- Modify: `spa/src/components/SessionSection.tsx`
- Test: `spa/src/components/SessionSection.test.tsx`

**Interfaces:**
- Consumes: `createSession(hostId, name, cwd, mode)` (`lib/host-api`), `useHostStore` (host-live re-check), `onSelect`.

- [ ] **Step 1: Write the failing tests (mock `createSession`)**

Extend the `vi.mock('../lib/host-api', …)` to also expose `createSession: vi.fn()`. Add:

```ts
import * as hostApi from '../lib/host-api'

it('creates a session and attaches it into the current pane', async () => {
  useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
  useHostStore.setState((s) => ({ runtime: { [HOST_ID]: { status: 'connected', tmuxState: 'available' } as never } }))
  vi.mocked(hostApi.createSession).mockResolvedValue({ code: 'new001', name: 'built', cwd: '~', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false })
  render(<SessionSection onSelect={mockOnSelect} />)
  fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
  fireEvent.change(screen.getByPlaceholderText('Session name'), { target: { value: 'built' } })
  fireEvent.click(screen.getByText('Create'))
  await screen.findByTestId(`new-session-${HOST_ID}`) // let the async create settle
  expect(hostApi.createSession).toHaveBeenCalledWith(HOST_ID, 'built', '~', 'terminal')
  expect(mockOnSelect).toHaveBeenCalledWith({ kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'new001', mode: 'terminal', cachedName: 'built', tmuxInstance: '' })
})

it('does not attach when the created session has a blank code', async () => {
  useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
  useHostStore.setState((s) => ({ runtime: { [HOST_ID]: { status: 'connected', tmuxState: 'available' } as never } }))
  vi.mocked(hostApi.createSession).mockResolvedValue({ code: '', name: 'built', cwd: '~', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false })
  render(<SessionSection onSelect={mockOnSelect} />)
  fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
  fireEvent.change(screen.getByPlaceholderText('Session name'), { target: { value: 'built' } })
  fireEvent.click(screen.getByText('Create'))
  await screen.findByText(/failed|error/i)
  expect(mockOnSelect).not.toHaveBeenCalled()
})

it('does not attach when the host is removed while creating', async () => {
  useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
  useHostStore.setState((s) => ({ runtime: { [HOST_ID]: { status: 'connected', tmuxState: 'available' } as never } }))
  vi.mocked(hostApi.createSession).mockImplementation(async () => {
    useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: null }) // host vanishes mid-flight
    return { code: 'new001', name: 'built', cwd: '~', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false }
  })
  render(<SessionSection onSelect={mockOnSelect} />)
  fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
  fireEvent.change(screen.getByPlaceholderText('Session name'), { target: { value: 'built' } })
  fireEvent.click(screen.getByText('Create'))
  await screen.findByText(/failed|error/i)
  expect(mockOnSelect).not.toHaveBeenCalled()
})
```

> Use the real resolved i18n strings for `Session name` placeholder (`hosts.session_name`) and `Create` (`hosts.create`). Confirm the exact English values in `locales/en.json` before writing; adjust the selectors to match.

- [ ] **Step 2: Run to verify failures**

Run: `cd spa && npx vitest run src/components/SessionSection.test.tsx -t "creates a session"`
Expected: FAIL — no form / `createSession` not wired.

- [ ] **Step 3: Add `NewTabSessionForm` and wire it under the header**

In `SessionSection.tsx`:

```tsx
import { createSession } from '../lib/host-api'

function NewTabSessionForm({ hostId, onCreated, onCancel }: {
  hostId: string
  onCreated: (content: { code: string; name: string; mode: string }) => void
  onCancel: () => void
}) {
  const t = useI18nStore((s) => s.t)
  const [name, setName] = useState('')
  const [cwd, setCwd] = useState('~')
  const [mode, setMode] = useState('terminal')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')

  const disabledSubmit = creating || !name.trim()

  const handleCreate = async () => {
    if (!name.trim()) return
    setCreating(true); setError('')
    try {
      const created = await createSession(hostId, name.trim(), cwd, mode)
      // Guard 1 — blank code = failed create.
      if (!created.code) { setError(t('hosts.create') + ' failed'); return }
      // Guard 2 — host still live (removed/disconnected during the await must not attach).
      const rt = useHostStore.getState().runtime[hostId]
      const live = !!useHostStore.getState().hosts[hostId] && !!rt && rt.status === 'connected' && rt.tmuxState !== 'unavailable'
      if (!live) { setError(t('hosts.create') + ' failed'); return }
      onCreated({ code: created.code, name: created.name, mode })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed')
    } finally {
      setCreating(false)
    }
  }

  return (
    <div className="mx-3 my-1 p-2 bg-surface-secondary border border-border-default rounded-md">
      <input placeholder={t('hosts.session_name')} value={name} autoFocus
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => { if (e.key === 'Enter') handleCreate() }}
        className="w-full bg-surface-primary border border-border-default rounded px-2 py-1 text-sm text-text-primary mb-1" />
      <input placeholder={t('hosts.session_cwd')} value={cwd}
        onChange={(e) => setCwd(e.target.value)}
        className="w-full bg-surface-primary border border-border-default rounded px-2 py-1 text-sm text-text-muted mb-1" />
      <select value={mode} onChange={(e) => setMode(e.target.value)}
        className="bg-surface-primary border border-border-default rounded px-2 py-1 text-sm text-text-primary">
        <option value="terminal">terminal</option>
        <option value="stream">stream</option>
      </select>
      {error && <p className="text-xs text-red-400 mt-1">{error}</p>}
      <div className="flex gap-2 mt-2">
        <button onClick={handleCreate} disabled={disabledSubmit}
          className="px-2 py-1 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">{t('hosts.create')}</button>
        <button onClick={onCancel}
          className="px-2 py-1 rounded text-xs bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
      </div>
    </div>
  )
}
```

- Under each host header, when `creatingHost === hostId`, render:

```tsx
{creatingHost === hostId && (
  <NewTabSessionForm
    hostId={hostId}
    onCancel={() => setCreatingHost(null)}
    onCreated={({ code, name, mode }) => {
      setCreatingHost(null)
      onSelect({ kind: 'tmux-session', hostId, sessionCode: code, mode: mode as 'terminal' | 'stream', cachedName: name, tmuxInstance: '' })
    }}
  />
)}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/SessionSection.test.tsx`
Expected: PASS (all, incl. blank-code + host-removed guards).

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/SessionSection.tsx spa/src/components/SessionSection.test.tsx
git commit -m "feat(new-tab): create a session per host and attach it into the current pane"
```

---

## Final verification (after all tasks)

- [ ] `cd spa && npx vitest run` — full suite green.
- [ ] `cd spa && pnpm run lint` — clean.
- [ ] `cd spa && pnpm run build` — succeeds.
- [ ] Manual smoke (HMR): New-Tab Sessions rows show agent icon/status like tabs; per-host `+` opens the form; creating attaches into the current pane (and into a split new-tab pane).

## PR split

Two reviewable PRs off this worktree (recommended): **PR-A = Phase 1 (Tasks 1-3)**, **PR-B = Phase 2 (Tasks 4-5)**. If review size is small, a single PR with the five commits is acceptable. Decide at PR time.
