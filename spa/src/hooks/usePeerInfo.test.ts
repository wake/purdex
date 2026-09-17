import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { PEER_STALE_AFTER_MS, usePeerInfo } from './usePeerInfo'
import { useHostStore, type HostRuntime } from '../stores/useHostStore'
import { emptyPeerHostEntry, usePeerStore, type PeerHostEntry, type PeerRow } from '../stores/usePeerStore'
import { useSessionCwdStore } from '../stores/useSessionCwdStore'
import { useTabStore } from '../stores/useTabStore'
import { useAgentStore, type NormalizedEvent } from '../stores/useAgentStore'
import { createTab } from '../types/tab'

const H = 'h1'
const CODE = 'z141yl'
/** The tmux server generation both the pane and the peers answer describe. */
const GEN = '6901:1789205013'
const NOW = 1_700_000_000_000

const peerRefresh = vi.fn(async (_hostId: string) => {})
const cwdRefresh = vi.fn(async (_hostId: string, _code: string) => {})

const ROW: PeerRow = {
  address: 'mini-lab/ai-chat-story-3a',
  ref: '_3k9f2m',
  title: 'ai-chat4',
  titleSource: 'user',
  deliverable: true,
  reason: '',
  tmuxInstance: GEN,
  agent: { type: 'cc', peerName: 'ai-chat-story-3a', status: 'idle' },
}

function setRuntime(runtime: HostRuntime | undefined) {
  useHostStore.setState({
    hosts: {
      [H]: { id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 },
      h2: { id: 'h2', name: 'air', ip: '100.64.0.4', port: 7860, order: 1 },
    },
    hostOrder: [H, 'h2'],
    runtime: runtime ? { [H]: runtime } : {},
  })
}

/** Seed a host as already fetched, so the policy has nothing left to do. */
function seedFetched(entry: Partial<PeerHostEntry> = {}) {
  usePeerStore.setState({
    byHost: { [H]: { ...emptyPeerHostEntry(), fetchedAt: NOW, rows: { [CODE]: ROW }, ...entry } },
  })
}

/**
 * Seed one terminal pane bound to (host, code) at GEN, optionally already
 * carrying the Claude Code session id its last owner `SessionStart` recorded.
 */
function seedTerminalPane(code = CODE, sessionId?: string, tabId = `tab-${code}`) {
  const tab = createTab({
    kind: 'tmux-session', hostId: H, sessionCode: code, mode: 'terminal', cachedName: 'dev', tmuxInstance: GEN,
  })
  const l = tab.layout
  if (l.type === 'leaf' && l.pane.content.kind === 'tmux-session' && sessionId !== undefined) {
    l.pane.content.rebuild = {
      sessionName: 'dev', tmuxInstance: GEN, capturedAt: NOW - 1,
      agent: { type: 'cc', sessionId, updatedAt: NOW - 1 },
    }
  }
  useTabStore.setState((s) => ({ tabs: { ...s.tabs, [tabId]: { ...tab, id: tabId } }, tabOrder: [...s.tabOrder, tabId], activeTabId: tabId }))
}

/**
 * A qualifying owner `SessionStart` for (host, code): the event the SPA's
 * provenance path turns into a rebuild-record write — the only signal that
 * says which Claude Code session is behind the pane now.
 */
function sessionStart(sessionId: string, code = CODE) {
  const event: NormalizedEvent = {
    agent_type: 'cc', status: 'idle', raw_event_name: 'PdxSessionStart', broadcast_ts: 1, subagents: [],
    detail: { pdx_provenance: { owner_session_start: true, agent_type: 'cc', session_id: sessionId, cwd: '/w', tmux_pane_id: '%1', tmux_instance: GEN } },
  }
  useAgentStore.getState().handleNormalizedEvent(H, code, event)
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(NOW)
  // `mockReset`, not `mockClear`: a once-implementation a failing test never
  // consumed must not leak into the next one.
  peerRefresh.mockReset()
  cwdRefresh.mockReset()
  usePeerStore.setState({ byHost: {}, refresh: peerRefresh })
  useSessionCwdStore.setState({ byHost: {}, refresh: cwdRefresh })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, unread: {} })
  setRuntime({ status: 'connected' })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('the fetch policy', () => {
  // The drift guard for ROW: a fixture in v3's shape — an eight-digit ref,
  // an address ending in `:<suffix>` — passes every test in this file while
  // proving nothing about v4.
  it('ROW is a v4 row: a six-digit ref and a colon-free address', () => {
    expect(ROW.ref).toMatch(/^_[0-9a-z]{6}$/)
    expect(ROW.address).toMatch(/^[a-z0-9][a-z0-9.-]*\/(tmux:.+|_[0-9a-z]{6}|[a-z0-9][a-z0-9-]*)$/)
  })

  it('fetches once on the first render that needs a connected, unfetched host', () => {
    renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(peerRefresh).toHaveBeenCalledTimes(1)
    expect(peerRefresh).toHaveBeenCalledWith(H)
  })

  it('does not fetch peers again when the tab switches between sessions of one host', () => {
    seedFetched()
    const { rerender } = renderHook(({ code }) => usePeerInfo(H, code, GEN), { initialProps: { code: CODE } })
    peerRefresh.mockClear()
    for (let i = 0; i < 10; i++) rerender({ code: `code-${i}` })
    expect(peerRefresh).toHaveBeenCalledTimes(0)
  })

  it('does not fetch a host it has already fetched, even after an error', () => {
    seedFetched({ error: 'boom' })
    renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(peerRefresh).not.toHaveBeenCalled()
  })

  it.each(['disconnected', 'reconnecting', 'auth-error'] as const)(
    'never fetches a host that is %s',
    (status) => {
      setRuntime({ status })
      renderHook(() => usePeerInfo(H, CODE, GEN))
      expect(peerRefresh).not.toHaveBeenCalled()
      expect(cwdRefresh).not.toHaveBeenCalled()
    },
  )

  it('never fetches a host with no runtime entry at all', () => {
    setRuntime(undefined)
    renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(peerRefresh).not.toHaveBeenCalled()
  })

  it('fetches a connected host whose tmux is unavailable — "no rows" is a real answer', () => {
    setRuntime({ status: 'connected', tmuxState: 'unavailable' })
    renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(peerRefresh).toHaveBeenCalledTimes(1)
  })

  it('fetches a connected host whose tmuxState is still undefined — the normal opening moment', () => {
    // useMultiHostEventWs sets status:'connected' on WS open and only sets
    // tmuxState when a tmux event arrives, so every host passes through this
    // state. Treating undefined as "not ok" would mean the first render after
    // connecting never fetches.
    setRuntime({ status: 'connected', tmuxState: undefined })
    renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(peerRefresh).toHaveBeenCalledTimes(1)
  })

  it('fetches once the host becomes connected, not before', () => {
    setRuntime({ status: 'disconnected' })
    renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(peerRefresh).not.toHaveBeenCalled()
    act(() => { useHostStore.getState().setRuntime(H, { status: 'connected' }) })
    expect(peerRefresh).toHaveBeenCalledTimes(1)
  })

  it('does nothing at all without a host or a session code', () => {
    renderHook(() => usePeerInfo(null, null, GEN))
    renderHook(() => usePeerInfo(null, CODE, GEN))
    expect(peerRefresh).not.toHaveBeenCalled()
    expect(cwdRefresh).not.toHaveBeenCalled()
  })

  it('reads the cwd on the first render and again whenever the session changes', () => {
    seedFetched()
    const { rerender } = renderHook(({ code }) => usePeerInfo(H, code, GEN), { initialProps: { code: CODE } })
    expect(cwdRefresh).toHaveBeenCalledTimes(1)
    expect(cwdRefresh).toHaveBeenCalledWith(H, CODE)
    rerender({ code: 'other1' })
    expect(cwdRefresh).toHaveBeenCalledTimes(2)
    expect(cwdRefresh).toHaveBeenLastCalledWith(H, 'other1')
  })
})

describe('refresh()', () => {
  it('always issues both refreshes, however fresh the cache is', () => {
    seedFetched()
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    peerRefresh.mockClear()
    cwdRefresh.mockClear()
    act(() => { result.current.refresh() })
    expect(peerRefresh).toHaveBeenCalledTimes(1)
    expect(cwdRefresh).toHaveBeenCalledTimes(1)
  })

  it('does nothing when the host is not connected', () => {
    seedFetched()
    setRuntime({ status: 'disconnected' })
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    act(() => { result.current.refresh() })
    expect(peerRefresh).not.toHaveBeenCalled()
    expect(cwdRefresh).not.toHaveBeenCalled()
  })
})

describe('what it returns', () => {
  it('returns the row for this session, the cwd, and the envelope flags', () => {
    seedFetched({ envelope: { partial: true, titlesUnavailable: false, unknownRegistryFiles: ['/x.json'] } })
    useSessionCwdStore.setState({
      byHost: { [H]: { [CODE]: { cwd: '/somewhere/else', fetchedAt: NOW, loading: false, error: null } } },
    })
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(result.current.row).toEqual(ROW)
    expect(result.current.cwd).toBe('/somewhere/else')
    expect(result.current.envelope).toEqual({ partial: true, titlesUnavailable: false, unknownRegistryFiles: ['/x.json'] })
    expect(result.current.connected).toBe(true)
    expect(result.current.fetched).toBe(true)
  })

  it('a host with an answer but no row for this session returns null, not undefined', () => {
    seedFetched({ rows: {} })
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(result.current.row).toBeNull()
    expect(result.current.fetched).toBe(true)
  })

  it('surfaces the two errors separately', () => {
    seedFetched({ error: 'peers boom' })
    useSessionCwdStore.setState({
      byHost: { [H]: { [CODE]: { cwd: '', fetchedAt: 0, loading: false, error: 'cwd boom' } } },
    })
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(result.current.error).toBe('peers boom')
    expect(result.current.cwdError).toBe('cwd boom')
  })

  it('is loading while either store is', () => {
    seedFetched({ loading: true })
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(result.current.loading).toBe(true)
    expect(result.current.row).toEqual(ROW)  // the previous value stays visible
  })
})

describe('staleness', () => {
  it('is fresh 30 s after the rows arrived', () => {
    seedFetched({ fetchedAt: NOW - 30_000 })
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(result.current.stale).toBe(false)
    expect(result.current.fetchedAt).toBe(NOW - 30_000)
  })

  it('is stale 90 s after the rows arrived', () => {
    seedFetched({ fetchedAt: NOW - 90_000 })
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(result.current.stale).toBe(true)
  })

  it('is never stale before the first answer', () => {
    usePeerStore.setState({ byHost: {} })
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(result.current.stale).toBe(false)
    expect(result.current.fetched).toBe(false)
  })

  it('goes stale on its own once the boundary passes, without another render', () => {
    seedFetched({ fetchedAt: NOW })
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(result.current.stale).toBe(false)
    act(() => { vi.advanceTimersByTime(PEER_STALE_AFTER_MS + 1) })
    expect(result.current.stale).toBe(true)
  })

  // The boundary timer is the one moment the hook already wakes up on its own,
  // so it is also where a host with an active pane gets its rows re-read:
  // "marked stale" becomes "refreshed at the boundary", one refresh per
  // answer, never a poll on render.
  describe('at the boundary', () => {
    it('refreshes a connected host with an active pane exactly once', () => {
      seedFetched({ fetchedAt: NOW })
      renderHook(() => usePeerInfo(H, CODE, GEN))
      peerRefresh.mockClear()
      act(() => { vi.advanceTimersByTime(PEER_STALE_AFTER_MS - 1) })
      expect(peerRefresh).not.toHaveBeenCalled()
      act(() => { vi.advanceTimersByTime(1) })
      expect(peerRefresh).toHaveBeenCalledTimes(1)
      expect(peerRefresh).toHaveBeenCalledWith(H)
    })

    it('does not refresh a host that is not connected', () => {
      seedFetched({ fetchedAt: NOW })
      setRuntime({ status: 'disconnected' })
      renderHook(() => usePeerInfo(H, CODE, GEN))
      act(() => { vi.advanceTimersByTime(PEER_STALE_AFTER_MS + 1) })
      expect(peerRefresh).not.toHaveBeenCalled()
    })

    // The other door into the same failure: the boundary passed while no pane
    // of this host was on screen (or the host was down), and a tab is opened
    // late. There is no timer left to fire, so the mount is the boundary.
    it('refreshes once on mount when the cached answer is already past the boundary', () => {
      seedFetched({ fetchedAt: NOW - PEER_STALE_AFTER_MS - 1 })
      const { rerender } = renderHook(() => usePeerInfo(H, CODE, GEN))
      expect(peerRefresh).toHaveBeenCalledTimes(1)
      expect(peerRefresh).toHaveBeenCalledWith(H)
      for (let i = 0; i < 10; i++) rerender()
      expect(peerRefresh).toHaveBeenCalledTimes(1)
    })

    it('refreshes once the host reconnects with an answer already past the boundary, not before', () => {
      seedFetched({ fetchedAt: NOW - PEER_STALE_AFTER_MS - 1 })
      setRuntime({ status: 'disconnected' })
      renderHook(() => usePeerInfo(H, CODE, GEN))
      expect(peerRefresh).not.toHaveBeenCalled()
      act(() => { useHostStore.getState().setRuntime(H, { status: 'connected' }) })
      expect(peerRefresh).toHaveBeenCalledTimes(1)
    })

    it('does not refresh on mount when the cached answer is still fresh — switching tabs fetches no fresh data', () => {
      seedFetched({ fetchedAt: NOW - 30_000 })
      const { rerender } = renderHook(({ code }) => usePeerInfo(H, code, GEN), { initialProps: { code: CODE } })
      rerender({ code: 'other1' })
      expect(peerRefresh).not.toHaveBeenCalled()
    })

    it('does not refresh when the pane is no longer active — the hook is handed nulls', () => {
      seedFetched({ fetchedAt: NOW })
      const { rerender } = renderHook(({ host }) => usePeerInfo(host, CODE, GEN), { initialProps: { host: H as string | null } })
      peerRefresh.mockClear()
      rerender({ host: null })
      act(() => { vi.advanceTimersByTime(PEER_STALE_AFTER_MS + 1) })
      expect(peerRefresh).not.toHaveBeenCalled()
    })

    it('a host whose boundary refresh fails is not refetched on re-render, nor by a later timer', () => {
      seedFetched({ fetchedAt: NOW })
      // The store keeps `fetchedAt` on a failed refresh (the rows go stale
      // rather than blank), which is what leaves no new boundary to fire at.
      peerRefresh.mockImplementationOnce(async (hostId) => {
        usePeerStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...s.byHost[hostId], error: 'boom', loading: false } } }))
      })
      const { result, rerender } = renderHook(() => usePeerInfo(H, CODE, GEN))
      peerRefresh.mockClear()
      act(() => { vi.advanceTimersByTime(PEER_STALE_AFTER_MS) })
      expect(peerRefresh).toHaveBeenCalledTimes(1)
      expect(result.current.error).toBe('boom')
      for (let i = 0; i < 10; i++) rerender()
      act(() => { vi.advanceTimersByTime(PEER_STALE_AFTER_MS * 3) })
      expect(peerRefresh).toHaveBeenCalledTimes(1)
    })
  })
})

// The generation guard above catches a tmux RESTART. It cannot catch a Claude
// Code session being REPLACED inside a live tmux session — same code, same
// server, nothing at the tmux level moves — and that is how a status bar came
// to show `purdex-4a` for a pane whose session now held `purdex-bb`. The SPA
// does already learn about the replacement: a qualifying `SessionStart` writes
// the new agent's session id into the pane's rebuild record. That write is the
// trigger.
describe('a replaced Claude Code session', () => {
  it('refreshes the host when a SessionStart records a different session id for this pane', () => {
    seedFetched()
    seedTerminalPane(CODE, 'sid-4a')
    renderHook(() => usePeerInfo(H, CODE, GEN))
    peerRefresh.mockClear()
    act(() => { sessionStart('sid-bb') })
    expect(peerRefresh).toHaveBeenCalledTimes(1)
    expect(peerRefresh).toHaveBeenCalledWith(H)
  })

  it('does not refresh when a SessionStart records the SAME session id — a status change is not a new agent', () => {
    seedFetched()
    seedTerminalPane(CODE, 'sid-4a')
    renderHook(() => usePeerInfo(H, CODE, GEN))
    peerRefresh.mockClear()
    act(() => { sessionStart('sid-4a') })
    expect(peerRefresh).not.toHaveBeenCalled()
  })

  it('does not refresh on an agent event that carries no provenance', () => {
    seedFetched()
    seedTerminalPane(CODE, 'sid-4a')
    renderHook(() => usePeerInfo(H, CODE, GEN))
    peerRefresh.mockClear()
    act(() => {
      useAgentStore.getState().handleNormalizedEvent(H, CODE, {
        agent_type: 'cc', status: 'running', raw_event_name: 'PreToolUse', broadcast_ts: 2, subagents: [],
      })
    })
    expect(peerRefresh).not.toHaveBeenCalled()
  })

  it('does not refresh on mount for a record already present — the record is the baseline, not a change', () => {
    seedFetched()
    seedTerminalPane(CODE, 'sid-4a')
    renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(peerRefresh).not.toHaveBeenCalled()
  })

  it('does not refresh when the tab switches between panes that recorded different session ids', () => {
    seedFetched()
    seedTerminalPane(CODE, 'sid-4a')
    seedTerminalPane('other1', 'sid-other')
    const { rerender } = renderHook(({ code }) => usePeerInfo(H, code, GEN), { initialProps: { code: CODE } })
    peerRefresh.mockClear()
    rerender({ code: 'other1' })
    rerender({ code: CODE })
    expect(peerRefresh).not.toHaveBeenCalled()
  })

  it('does not refresh a host that is not connected', () => {
    seedFetched()
    seedTerminalPane(CODE, 'sid-4a')
    setRuntime({ status: 'disconnected' })
    renderHook(() => usePeerInfo(H, CODE, GEN))
    act(() => { sessionStart('sid-bb') })
    expect(peerRefresh).not.toHaveBeenCalled()
  })

  it('still refreshes once the record goes from empty to a session id — the first owner start after a fetch', () => {
    seedFetched()
    seedTerminalPane(CODE)
    renderHook(() => usePeerInfo(H, CODE, GEN))
    peerRefresh.mockClear()
    act(() => { sessionStart('sid-bb') })
    expect(peerRefresh).toHaveBeenCalledTimes(1)
  })
})

// A session code is tmux's `$N` re-encoded, and tmux hands `$N` out from zero
// again after a restart — so a cached row can carry a *different* session's
// address under this pane's code (reference: session code cross-host collision,
// 2026-09-15). Rendering that is bad; copying it and sending to it is exactly
// the failure this line of work exists to remove, so a row is used only when
// the pane and the row name the same tmux server.
describe('the tmux generation', () => {
  it('returns the row when the pane and the row name the same generation', () => {
    seedFetched()
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(result.current.row).toEqual(ROW)
  })

  it('withholds a row stamped with another generation — tmux restarted and reused the code', () => {
    seedFetched({ rows: { [CODE]: { ...ROW, tmuxInstance: '4242:1700000000' } } })
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(result.current.row).toBeNull()
  })

  it('withholds the row when the pane generation is unknown — unknown is not a match', () => {
    seedFetched()
    const { result } = renderHook(() => usePeerInfo(H, CODE, ''))
    expect(result.current.row).toBeNull()
  })

  it('withholds the row when the pane generation is null — the session is not reconciled yet', () => {
    seedFetched()
    const { result } = renderHook(() => usePeerInfo(H, CODE, null))
    expect(result.current.row).toBeNull()
  })

  it('withholds the row when the row generation is unknown — an older daemon says nothing', () => {
    seedFetched({ rows: { [CODE]: { ...ROW, tmuxInstance: '' } } })
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(result.current.row).toBeNull()
  })

  it('withholds the row when both generations are unknown — two unknowns are not one machine', () => {
    seedFetched({ rows: { [CODE]: { ...ROW, tmuxInstance: '' } } })
    const { result } = renderHook(() => usePeerInfo(H, CODE, ''))
    expect(result.current.row).toBeNull()
  })

  it('leaves the rest of the answer alone on a mismatch: the host was still fetched', () => {
    seedFetched({ rows: { [CODE]: { ...ROW, tmuxInstance: 'other' } }, error: 'boom' })
    const { result } = renderHook(() => usePeerInfo(H, CODE, GEN))
    expect(result.current.row).toBeNull()
    expect(result.current.fetched).toBe(true)
    expect(result.current.error).toBe('boom')
    expect(peerRefresh).not.toHaveBeenCalled()
  })

  it('still fetches, and still reads the cwd, for a pane whose generation is unknown', () => {
    renderHook(() => usePeerInfo(H, CODE, ''))
    expect(peerRefresh).toHaveBeenCalledTimes(1)
    expect(cwdRefresh).toHaveBeenCalledTimes(1)
  })

  it('picks the row up as soon as the pane learns its generation', () => {
    seedFetched()
    const { result, rerender } = renderHook(({ gen }) => usePeerInfo(H, CODE, gen), { initialProps: { gen: '' } })
    expect(result.current.row).toBeNull()
    rerender({ gen: GEN })
    expect(result.current.row).toEqual(ROW)
  })
})
