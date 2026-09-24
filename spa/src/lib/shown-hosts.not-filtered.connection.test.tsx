// spa/src/lib/shown-hosts.not-filtered.connection.test.tsx — hidden ≠ absent, host-level connections (host ownership
// plan H2d-5 T1, §0.21 table, last row). A host hidden in the workbench keeps every HOST-level connection: its health
// negotiation (`runtime.status`, read through `useHostConnection`), its event WS and the `sessions` reconcile on it (the
// session store, the panes' terminated marks, the attach gate), the session watch and the session refresh
// (`recoverHostSessions`). Each case runs once with air26 HIDDEN (`ids: [<mlab wire>]`) and once SHOWN
// (`ids: [<mlab wire>, <air26 wire>]`) and compares the two observations — they must be identical.
//
// NOT compared: the per-pane sweeps inside the reconcile (revive, cwd / provenance probes) skip a hidden host BY DESIGN
// (H2d-4 T3); the probe modules are stubbed here so neither setting reaches the network through them.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTabStore } from '../stores/useTabStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useHostConnection } from '../hooks/useHostConnection'
import { useSessionWatch, __resetSessionWatch } from '../hooks/useSessionWatch'
import type { FreshSessions } from './host-api'
import { syncIdOfSync } from './profile/host-identity'
import { isRefShownNow } from './shown-hosts'
import { __resetForTests as __resetSessionVersion } from './rebuild/session-version'
import type { PaneLayout, Tab, TmuxSessionContent } from '../types/tab'

vi.mock('./host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))
vi.mock('./rebuild/cwd-probe', () => ({ probeMissingCwds: vi.fn(), probeSessionCwd: vi.fn(), resetCwdProbes: vi.fn() }))
vi.mock('./rebuild/provenance-probe', () => ({ probeSessionProvenance: vi.fn(), resetProvenanceProbes: vi.fn() }))

const { listSessionsFresh } = vi.hoisted(() => ({ listSessionsFresh: vi.fn<(hostId: string) => Promise<FreshSessions>>() }))
vi.mock('./host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./host-api')>()),
  listSessionsFresh: (hostId: string) => listSessionsFresh(hostId),
}))

const { useMultiHostEventWs } = await import('../hooks/useMultiHostEventWs')
const { recoverHostSessions, __resetRefreshForTests } = await import('./rebuild/refresh-sessions')

// mlab and air26, each a LOCAL host with a daemonId (so each has a `d1_…` wire id).
const MLAB = 'h-mlab'
const AIR = 'h-air26'
const MLAB_WIRE = syncIdOfSync('mini-lab:27bbbb')
const AIR_WIRE = syncIdOfSync('air-lab:26aaaa')
const AIR_IP = '100.64.0.4'
const GEN = '222:2000'

type Setting = 'hidden' | 'shown'
const IDS: Record<Setting, string[]> = { hidden: [MLAB_WIRE], shown: [MLAB_WIRE, AIR_WIRE] }

const host = (id: string, ip: string, daemonId: string, order: number): HostConfig =>
  ({ id, name: id, ip, port: 7860, token: 'tok', order, daemonId })

class FakeSocket {
  static OPEN = 1
  readyState = 0
  binaryType = ''
  url: string
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => { this.readyState = 3 })
  constructor(url: string) { this.url = url; sockets.push(this) }
  emit(data: string) { this.onmessage?.({ data }) }
}
let sockets: FakeSocket[] = []

const leaf = (paneId: string, hostId: string, sessionCode: string): PaneLayout => ({
  type: 'leaf',
  pane: { id: paneId, content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: sessionCode, tmuxInstance: GEN } },
})

/** Fixed ids, so the two settings' observations compare verbatim: two air26 tabs and a split [mlab | air26]. */
function seedTabs(): void {
  const tabs: Record<string, Tab> = {
    't-air-live': { id: 't-air-live', pinned: false, locked: false, createdAt: 1, layout: leaf('p-air-live', AIR, 'aaa') },
    't-air-gone': { id: 't-air-gone', pinned: false, locked: false, createdAt: 1, layout: leaf('p-air-gone', AIR, 'bbb') },
    't-split': {
      id: 't-split', pinned: false, locked: false, createdAt: 1,
      layout: { type: 'split', id: 's1', direction: 'h', sizes: [50, 50], children: [leaf('p-split-m', MLAB, 'mmm'), leaf('p-split-a', AIR, 'ccc')] },
    },
  }
  useTabStore.setState({ tabs, tabOrder: Object.keys(tabs), activeTabId: 't-air-live' })
}

/** Every tmux pane's terminated mark, by pane id. */
function terminatedMarks(): Record<string, string | null> {
  const out: Record<string, string | null> = {}
  const walk = (l: PaneLayout) => {
    if (l.type === 'split') { l.children.forEach(walk); return }
    const c = l.pane.content as TmuxSessionContent
    if (c.kind === 'tmux-session') out[l.pane.id] = c.terminated ?? null
  }
  for (const tab of Object.values(useTabStore.getState().tabs)) walk(tab.layout)
  return out
}

const sessionsFrame = (codes: string[]) => JSON.stringify({
  type: 'sessions', session: '',
  value: JSON.stringify(codes.map((code) => ({ code, name: code, tmux_instance: GEN }))),
})

const fetchHost = vi.fn(async (_hostId: string) => {})

function arrange(setting: Setting): void {
  localStorage.clear()
  sockets = []
  __resetRefreshForTests()
  __resetSessionVersion()
  __resetSessionWatch()
  listSessionsFresh.mockReset()
  fetchHost.mockClear()
  useHostStore.setState({
    hosts: { [MLAB]: host(MLAB, '100.64.0.2', 'mini-lab:27bbbb', 0), [AIR]: host(AIR, AIR_IP, 'air-lab:26aaaa', 1) },
    hostOrder: [MLAB, AIR],
    activeHostId: MLAB,
    runtime: {},
  })
  useSessionStore.setState({ sessions: {}, fetchHost } as never)
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useShownHostsStore.setState({ ids: IDS[setting] })
  seedTabs()
  // The premise of every case: the setting really hides / shows air26 (and mlab is shown in both).
  expect(isRefShownNow(AIR)).toBe(setting === 'shown')
  expect(isRefShownNow(MLAB)).toBe(true)
}

beforeEach(() => {
  vi.stubGlobal('WebSocket', FakeSocket)
})

afterEach(() => {
  vi.unstubAllGlobals()
  __resetRefreshForTests()
  __resetSessionWatch()
  useHostStore.getState().reset()
  useShownHostsStore.setState({ ids: [] })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
})

/** Mount the event-WS hook, let health negotiate, optionally feed air26's socket a `sessions` frame; observe. */
async function observeEventWs(setting: Setting, frame?: string[]) {
  arrange(setting)
  const view = renderHook(() => useMultiHostEventWs())
  await waitFor(() => expect(sockets).toHaveLength(2))
  const air = sockets.find((s) => s.url.includes(AIR_IP))
  if (frame && air) act(() => { air.emit(sessionsFrame(frame)) })
  const conn = renderHook(() => useHostConnection(AIR))
  const observed = {
    urls: sockets.map((s) => s.url).sort(),
    airStatus: useHostStore.getState().runtime[AIR]?.status ?? null,
    airConnection: conn.result.current.status,
    airSessions: (useSessionStore.getState().sessions[AIR] ?? []).map((s) => s.code),
    marks: terminatedMarks(),
    airAttachReady: useHostStore.getState().runtime[AIR]?.attachReady ?? null,
  }
  conn.unmount()
  view.unmount()
  return observed
}

describe('hidden ≠ absent — host-level connections (H2d-5 T1)', () => {
  it('health: air26 is negotiated and `useHostConnection(air26)` reads connected — identical hidden and shown', async () => {
    const hidden = await observeEventWs('hidden')
    const shown = await observeEventWs('shown')
    expect(hidden.airStatus).toBe('connected')
    expect(hidden.airConnection).toBe('connected')
    expect(hidden).toEqual(shown)
  })

  it('event WS: air26\'s socket is opened (the WebSocket mock sees its URL) — identical hidden and shown', async () => {
    const hidden = await observeEventWs('hidden')
    const shown = await observeEventWs('shown')
    expect(hidden.urls).toHaveLength(2)
    expect(hidden.urls.some((u) => u.includes(`${AIR_IP}:7860/ws/host-events`))).toBe(true)
    expect(hidden).toEqual(shown)
  })

  it('the `sessions` reconcile on air26: session store, terminated marks and the attach gate — identical hidden and shown', async () => {
    const hidden = await observeEventWs('hidden', ['aaa'])
    const shown = await observeEventWs('shown', ['aaa'])
    expect(hidden.airSessions).toEqual(['aaa'])
    // bbb and ccc vanished → marked; aaa lives; the mlab pane of the split is not air26's frame's business.
    expect(hidden.marks).toEqual({ 'p-air-live': null, 'p-air-gone': 'session-closed', 'p-split-m': null, 'p-split-a': 'session-closed' })
    expect(hidden.airAttachReady).toBe(true)
    expect(hidden).toEqual(shown)
  })

  it('session watch: air26\'s sessions are fetched — identical hidden and shown', () => {
    const observe = (setting: Setting) => {
      arrange(setting)
      useHostStore.setState({ runtime: { [MLAB]: { status: 'connected' }, [AIR]: { status: 'connected' } } as never })
      const view = renderHook(() => useSessionWatch())
      const calls = fetchHost.mock.calls.map(([id]) => id).sort()
      view.unmount()
      return calls
    }
    const hidden = observe('hidden')
    const shown = observe('shown')
    expect(hidden).toEqual([AIR, MLAB].sort())
    expect(hidden).toEqual(shown)
  })

  it('session refresh (`recoverHostSessions`): air26 is asked for a fresh list and reconciled — identical hidden and shown', async () => {
    const observe = async (setting: Setting) => {
      arrange(setting)
      listSessionsFresh.mockResolvedValue({
        kind: 'versioned', epoch: '9f3c1a0b7d2e4c61', seq: 1,
        sessions: [{ code: 'aaa', name: 'aaa', tmux_instance: GEN }] as never,
      })
      await recoverHostSessions(AIR)
      return {
        asked: listSessionsFresh.mock.calls.map(([id]) => id),
        airSessions: (useSessionStore.getState().sessions[AIR] ?? []).map((s) => s.code),
        marks: terminatedMarks(),
      }
    }
    const hidden = await observe('hidden')
    const shown = await observe('shown')
    expect(hidden.asked).toEqual([AIR])
    expect(hidden.airSessions).toEqual(['aaa'])
    expect(hidden.marks['p-air-gone']).toBe('session-closed')
    expect(hidden).toEqual(shown)
  })
})
