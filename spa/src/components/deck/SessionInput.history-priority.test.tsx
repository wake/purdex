// #2666 (codex finding): an entry handed to the daemon that the header does not show yet still takes priority over the history walk.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { SessionInput } from './SessionInput'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { clearAllDrafts, draftKey } from '../../lib/conversations/draft-memory'
import { UNDO_MS, clearAllSendQueues } from '../../lib/conversations/send-queue'
import { bindAdapter, clearAllAdapters } from '../../lib/conversations/deck-adapter'
import { terminalAdapter } from '../../lib/conversations/terminal-adapter'
import { clearAllInputHistory, setHistorySource } from '../../lib/conversations/input-history'
import { clearAllTakeBack } from '../../lib/conversations/take-back'

const fetchMock = vi.hoisted(() => vi.fn())
vi.mock('../../lib/host-api', () => ({ pinnedHostFetch: fetchMock }))

const H = 'h', SID = '11111111-2222-4333-8444-555555555555'

function SessionRenderer({ pane }: PaneRendererProps) {
  bindAdapter(draftKey(pane.id, H, SID), terminalAdapter(H, SID, undefined, () => ({ code: 'dev001', instance: 'g1' })))
  return <SessionInput paneKey={pane.id} hostId={H} sessionId={SID} capabilities={{ send: 'keys' }} onSwitchToTerminal={() => {}} />
}
const Other = () => <div data-testid="other-tab" />

const sessionTab: Tab = { ...createTab({ kind: 'execution', executionId: 'exc_1', host: H }), id: 't-session' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [sessionTab, dashTab]
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const paneId = (sessionTab.layout as { pane: { id: string } }).pane.id

beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  fetchMock.mockReset().mockImplementation((_h: string, path: string) => {
    if (path.endsWith('/recall')) return new Promise<Response>(() => {})
    return Promise.resolve(new Response(JSON.stringify({ outcome: 'handed', mode: 'fill' })))
  })
  clearModuleRegistry()
  registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: SessionRenderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
})
afterEach(() => { cleanup(); clearAllDrafts(); clearAllSendQueues(); clearAllAdapters(); clearAllTakeBack(); clearAllInputHistory(); vi.useRealTimers() })

describe('Up right after a send', () => {
  it('handed to the daemon, header not updated yet, empty box: Up takes back, never history', async () => {
    const key = draftKey(paneId, H, SID)
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    setHistorySource(key, [{ id: 'u1', at: 1, index: 1, type: 'user', text: 'old history', source: 'user' }])
    fireEvent.change(box(), { target: { value: 'just sent' } })
    fireEvent.keyDown(box(), { key: 'Enter' })
    await act(() => vi.advanceTimersByTimeAsync(UNDO_MS)) // handed, nothing in the header
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'sent')
    fireEvent.keyDown(box(), { key: 'ArrowUp' })
    expect(box().value).toBe('') // not the history
    expect(fetchMock.mock.calls.filter((c) => String(c[1]).endsWith('/recall'))).toHaveLength(1)
  })
})
