// Regression (CLAUDE.md tab-hosted rule, K-2b): a take-back answered while the tab is switched away still lands in the draft memory, and the reader
// finds the text in the box on return. Real TabContent (the pane really unmounts: keepAliveCount 0), the real terminal adapter over a mocked fetch.
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
import { clearAllDrafts, draftKey, readDraft } from '../../lib/conversations/draft-memory'
import { UNDO_MS, clearAllSendQueues } from '../../lib/conversations/send-queue'
import { bindAdapter, clearAllAdapters } from '../../lib/conversations/deck-adapter'
import { terminalAdapter } from '../../lib/conversations/terminal-adapter'
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
let answerRecall: (r: Response) => void

beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  fetchMock.mockReset().mockImplementation((_h: string, path: string) => {
    if (path.endsWith('/recall')) return new Promise<Response>((resolve) => { answerRecall = resolve })
    return Promise.resolve(new Response(JSON.stringify({ outcome: 'handed', mode: 'fill' })))
  })
  clearModuleRegistry()
  registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: SessionRenderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
})
afterEach(() => { cleanup(); clearAllDrafts(); clearAllSendQueues(); clearAllAdapters(); clearAllTakeBack(); vi.useRealTimers() })

describe('a take-back across a tab switch', () => {
  it('answered while switched away: the draft holds the text on return', async () => {
    const { rerender } = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'queued by claude' } })
    fireEvent.keyDown(box(), { key: 'Enter' })
    await act(() => vi.advanceTimersByTimeAsync(UNDO_MS)) // handed over: the entry waits for its echo
    const m = screen.getByTestId('queued-message')
    expect(m).toHaveAttribute('data-state', 'sent')
    fireEvent.click(m)
    expect(fetchMock.mock.calls.filter((c) => String(c[1]).endsWith('/recall'))).toHaveLength(1)

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('session-input')).toBeNull() // really unmounted
    await act(async () => { answerRecall(new Response(JSON.stringify({ text: 'queued by claude' }))); await vi.advanceTimersByTimeAsync(0) })
    expect(readDraft(draftKey(paneId, H, SID))).toBe('queued by claude')

    rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(box().value).toBe('queued by claude')
    expect(screen.queryByTestId('queued-message')).toBeNull() // the entry stood for the line that came back
  })
})
