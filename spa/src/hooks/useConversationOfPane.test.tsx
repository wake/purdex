import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useConversationOfPane } from './useConversationOfPane'
import { useConversationStore } from '../stores/useConversationStore'
import type { TmuxSessionContent } from '../types/tab'

const prov = vi.hoisted(() => ({ fetch: vi.fn() }))
vi.mock('../lib/host-api', () => ({ fetchSessionProvenance: prov.fetch }))

const acquire = vi.fn()
const releases: Array<ReturnType<typeof vi.fn>> = []

const pane = (over: Partial<TmuxSessionContent> = {}): TmuxSessionContent => ({
  kind: 'tmux-session', hostId: 'h1', sessionCode: 'dev001', mode: 'terminal', cachedName: 'dev', tmuxInstance: 'i', ...over,
})
const withSession = (id: string): Partial<TmuxSessionContent> =>
  ({ rebuild: { agent: { type: 'cc', sessionId: id } } } as unknown as Partial<TmuxSessionContent>)
const answer = (over: Record<string, unknown> = {}) => ({ found: true, agentType: 'cc', sessionId: 'sid-1', ...over })
const settle = async () => { await act(async () => { for (let i = 0; i < 6; i++) await Promise.resolve() }) }

beforeEach(() => {
  prov.fetch.mockReset().mockImplementation(async () => answer())
  acquire.mockReset()
  releases.length = 0
  acquire.mockImplementation(() => { const r = vi.fn(); releases.push(r); return r })
  useConversationStore.setState({ byKey: {}, acquire } as never)
})

describe('useConversationOfPane', () => {
  it('holds nothing when disabled, for a terminated pane, or for a pane that is not a session', async () => {
    for (const [content, enabled] of [[pane(), false], [pane({ terminated: 'session-closed' }), true], [{ kind: 'dashboard' } as never, true], [null, true]] as const) {
      const { result } = renderHook(() => useConversationOfPane(content, enabled))
      await settle()
      expect(result.current).toEqual({ state: 'off' })
    }
    expect(prov.fetch).not.toHaveBeenCalled()
    expect(acquire).not.toHaveBeenCalled()
  })

  it('resolves the session id from the provenance, then holds the conversation', async () => {
    const { result } = renderHook(() => useConversationOfPane(pane(), true))
    expect(result.current).toEqual({ state: 'resolving' })
    await settle()
    expect(prov.fetch.mock.calls[0].slice(0, 2)).toEqual(['h1', 'dev001'])
    expect(acquire).toHaveBeenCalledWith('h1', 'sid-1')
    expect(result.current).toMatchObject({ state: 'ready', hostId: 'h1', sessionId: 'sid-1' })
  })

  it.each([
    ['nothing found', answer({ found: false, sessionId: '' })],
    ['another agent', answer({ agentType: 'codex' })],
    ['no session id', answer({ sessionId: '' })],
  ])('%s → no_session, and nothing is held', async (_n, a) => {
    prov.fetch.mockImplementation(async () => a)
    const { result } = renderHook(() => useConversationOfPane(pane(), true))
    await settle()
    expect(result.current).toMatchObject({ state: 'unreadable', reason: 'no_session' })
    expect(acquire).not.toHaveBeenCalled()
  })

  it('an unreadable provenance is unreachable, and retry asks again', async () => {
    prov.fetch.mockRejectedValueOnce(new Error('down'))
    const { result } = renderHook(() => useConversationOfPane(pane(), true))
    await settle()
    expect(result.current).toMatchObject({ state: 'unreadable', reason: 'unreachable' })
    act(() => (result.current as { retry: () => void }).retry())
    await settle()
    expect(prov.fetch).toHaveBeenCalledTimes(2)
    expect(result.current).toMatchObject({ state: 'ready', sessionId: 'sid-1' })
  })

  it('reads the provenance again when the pane’s agent reports another session, and lets the old one go', async () => {
    const { result, rerender } = renderHook(({ c }) => useConversationOfPane(c, true), { initialProps: { c: pane(withSession('sid-1')) } })
    await settle()
    expect(acquire).toHaveBeenCalledWith('h1', 'sid-1')
    prov.fetch.mockImplementation(async () => answer({ sessionId: 'sid-2' }))
    rerender({ c: pane(withSession('sid-2')) })
    // while the new one is being asked for, the old id is not held
    expect(releases[0]).toHaveBeenCalled()
    expect(result.current).toEqual({ state: 'resolving' })
    await settle()
    expect(acquire).toHaveBeenLastCalledWith('h1', 'sid-2')
    expect(result.current).toMatchObject({ state: 'ready', sessionId: 'sid-2' })
  })

  it('an answer that arrives after the question changed is ignored', async () => {
    let first!: (v: unknown) => void
    prov.fetch.mockImplementationOnce(() => new Promise((r) => { first = r }))
    const { result, rerender } = renderHook(({ c }) => useConversationOfPane(c, true), { initialProps: { c: pane(withSession('a')) } })
    prov.fetch.mockImplementation(async () => answer({ sessionId: 'sid-new' }))
    rerender({ c: pane(withSession('b')) })
    await settle()
    first(answer({ sessionId: 'sid-old' }))
    await settle()
    expect(acquire).not.toHaveBeenCalledWith('h1', 'sid-old')
    expect(result.current).toMatchObject({ state: 'ready', sessionId: 'sid-new' })
  })

  it('unmounting lets the conversation go', async () => {
    const { unmount } = renderHook(() => useConversationOfPane(pane(), true))
    await settle()
    unmount()
    expect(releases[0]).toHaveBeenCalledTimes(1)
  })

  it('the conversation API saying it is unreadable shows through', async () => {
    const { result } = renderHook(() => useConversationOfPane(pane(), true))
    await settle()
    act(() => useConversationStore.setState({
      byKey: { ['h1\u0000sid-1']: { doc: {} as never, status: 'unreadable', reason: 'not_found', paging: false, subagents: {} } },
    }))
    expect(result.current).toMatchObject({ state: 'unreadable', reason: 'not_found' })
    act(() => useConversationStore.setState({
      byKey: { ['h1\u0000sid-1']: { doc: {} as never, status: 'unreadable', reason: 'provider_unsupported', paging: false, subagents: {} } },
    }))
    expect(result.current).toMatchObject({ state: 'unreadable', reason: 'provider_unsupported' })
  })
})
