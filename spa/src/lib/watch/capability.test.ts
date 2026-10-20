// W5e: a host is a watch host when its /api/info lists `watch.v1` — not when its watch routes happen to answer. The daemon serves the
// routes whenever the module is registered, and announces `watch.v1` only once the module is ready. So a host whose status route
// answers 200 but whose /api/info lacks the capability gets no events read and no events tab.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const fetchWatchStatus = vi.fn()
const fetchWatchEvents = vi.fn()
const fetchWatchUnhandled = vi.fn()
vi.mock('./api', () => ({ fetchWatchStatus: (...a: unknown[]) => fetchWatchStatus(...a), fetchWatchEvents: (...a: unknown[]) => fetchWatchEvents(...a), fetchWatchUnhandled: (...a: unknown[]) => fetchWatchUnhandled(...a) }))
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { createWatchReconciler } from '../../features/workspace/lib/watch-area'
import { WATCH_EVENTS_TAB_ID } from './ids'
import { startWatchStatus, useWatchStatusStore } from './status-store'
import { startWatchEvents, useWatchEventsStore } from './events-store'

const status = { self: { host_id: 'd1', state: 'none' }, enabled: true, auto_wake: true, mode: '', targets: [] }
const infoOf = (...caps: string[]) => ({ info: null, capabilities: null, phase: 'unavailable', error: null, fetchedAt: 1, generation: 1, fingerprint: 'f', daemonCapabilities: caps })
const flush = () => new Promise((r) => setTimeout(r, 0))

let stops: Array<() => void> = []
beforeEach(() => {
  fetchWatchStatus.mockReset(); fetchWatchEvents.mockReset(); fetchWatchUnhandled.mockReset()
  fetchWatchUnhandled.mockResolvedValue({ kind: 'ok', count: 0 })
  fetchWatchStatus.mockResolvedValue({ kind: 'ok', status })
  fetchWatchEvents.mockResolvedValue({ kind: 'ok', events: [] })
  useWatchStatusStore.getState().reset(); useWatchEventsStore.getState().reset()
  useWorkspaceStore.getState().reset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.getState().addWorkspace('A')
  useWorkspaceStore.getState().ensureWatchWorkspace()
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: ['h1'], runtime: { h1: { status: 'connected' } } } as never)
})
afterEach(() => { for (const s of stops) s(); stops = [] })

const run = async (caps: string[]) => {
  useNexHostStore.setState({ byHost: { h1: infoOf(...caps) }, ensure: async () => {} } as never)
  stops.push(startWatchStatus(), startWatchEvents())
  await flush(); await flush()
  createWatchReconciler().reconcile({ hostIds: [] }, 1)
}

describe('the events tab and the events read follow /api/info', () => {
  it('status answers 200 but /api/info lacks watch.v1: no events read, no events tab', async () => {
    await run(['fleet.v1'])
    expect(fetchWatchStatus).not.toHaveBeenCalled()
    expect(fetchWatchEvents).not.toHaveBeenCalled()
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === 'watch')!.tabs).not.toContain(WATCH_EVENTS_TAB_ID)
  })
  it('/api/info lists watch.v1: the events are read and the tab is made', async () => {
    await run(['watch.v1'])
    expect(fetchWatchEvents).toHaveBeenCalledTimes(1)
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === 'watch')!.tabs).toContain(WATCH_EVENTS_TAB_ID)
  })
})

describe('an /api/info that fails once and then recovers, with no reconnect and no push', () => {
  it('the status becomes ok and the events are read', async () => {
    vi.useFakeTimers()
    try {
      let calls = 0
      useNexHostStore.setState({ byHost: {}, ensure: async () => { calls++; if (calls >= 2) useNexHostStore.setState({ byHost: { h1: infoOf('watch.v1') } } as never) } } as never)
      stops.push(startWatchStatus(), startWatchEvents())
      await vi.advanceTimersByTimeAsync(0)
      expect(fetchWatchEvents).not.toHaveBeenCalled()
      expect(useWatchStatusStore.getState().byHost.h1).toBeUndefined()
      await vi.advanceTimersByTimeAsync(5_000)
      expect(useWatchStatusStore.getState().byHost.h1.kind).toBe('ok')
      expect(fetchWatchEvents).toHaveBeenCalledTimes(1)
      expect(useWatchEventsStore.getState().byHost.h1.kind).toBe('ok')
    } finally {
      vi.useRealTimers()
    }
  })
})
