// K-2a-2: the terminal adapter's capability gate, its live send target, and the fallback port that can no longer reach a submit route.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { paneSendTarget, terminalAdapter } from './terminal-adapter'
import { unboundSendPort } from './send'
import { sendQueueFor, clearAllSendQueues } from './send-queue'
import { clearAllAdapters } from './deck-adapter'
import { useTabStore } from '../../stores/useTabStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { createTab } from '../../types/tab'

const fetchMock = vi.hoisted(() => vi.fn())
vi.mock('../host-api', () => ({ pinnedHostFetch: fetchMock, agentUploadToPath: vi.fn() }))

const H = 'h'
const tabWith = (tmuxInstance: string) => {
  const tab = createTab({ kind: 'tmux-session', hostId: H, sessionCode: 'dev001', mode: 'terminal', cachedName: 'x', tmuxInstance })
  return { tab, paneId: (tab.layout as { pane: { id: string } }).pane.id }
}

beforeEach(() => {
  fetchMock.mockReset()
  clearAllSendQueues(); clearAllAdapters()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useSessionStore.setState({ sessions: {} })
})

describe('terminalAdapter.canSend', () => {
  it.each([[{ send: 'prompt' }, true], [{ send: 'keys' }, true], [{ send: 'not_wired' }, false], [{ send: 'execution' }, false], [{}, false], [undefined, false]])('%j -> %s', (caps, want) => {
    expect(terminalAdapter(H, 's1', 'dev001').canSend(caps as never)).toBe(want)
  })
})

describe('paneSendTarget', () => {
  it('the pane\'s own generation first; the session list\'s when the pane has none; null when neither; null without a code', () => {
    const { tab, paneId } = tabWith('g-pane')
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id] })
    useSessionStore.setState({ sessions: { [H]: [{ code: 'dev001', name: 'x', cwd: '/', mode: 'terminal', tmux_instance: 'g-list' }] } })
    expect(paneSendTarget(paneId, H, 'dev001')).toEqual({ code: 'dev001', instance: 'g-pane' })
    const bare = tabWith('')
    useTabStore.setState({ tabs: { [bare.tab.id]: bare.tab }, tabOrder: [bare.tab.id] })
    expect(paneSendTarget(bare.paneId, H, 'dev001')).toEqual({ code: 'dev001', instance: 'g-list' })
    useSessionStore.setState({ sessions: {} })
    expect(paneSendTarget(bare.paneId, H, 'dev001')).toBeNull()
    expect(paneSendTarget(bare.paneId, H, '')).toBeNull()
  })
})

describe('the fallback port', () => {
  it('a queue made before any adapter was bound cannot submit (no request, a rejected outcome) but can interrupt through the mod route', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ status: 'accepted' }))))
    const q = sendQueueFor('k', () => unboundSendPort(H, 's1'))
    await expect(unboundSendPort(H, 's1').submit('a', 'c1')).resolves.toMatchObject({ kind: 'rejected' })
    expect(fetchMock).not.toHaveBeenCalled()
    await q.interrupt()
    expect(fetchMock.mock.calls[0][1]).toBe('/api/conversations/claude/s1/interrupt')
  })
})
