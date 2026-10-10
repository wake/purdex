// Regression (CLAUDE.md tab-hosted rule): the draft in the session input, and the messages still waiting in the send queue,
// survive switching to another tab and back. The alive pool keeps no execution-kind tab by default (keepAliveCount 0), so the
// pane really unmounts: this mounts the real TabContent, not SessionInput alone.
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
import { clearAllSendQueues } from '../../lib/conversations/send-queue'

const fetchMock = vi.hoisted(() => vi.fn())
vi.mock('../../lib/host-api', () => ({ pinnedHostFetch: fetchMock }))

const H = 'h', SID = '11111111-2222-4333-8444-555555555555'

function SessionRenderer({ pane }: PaneRendererProps) {
  return <SessionInput paneKey={pane.id} hostId={H} sessionId={SID} capabilities={{ send: 'prompt' }} onSwitchToTerminal={() => {}} />
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
  fetchMock.mockReset().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ status: 'accepted' }))))
  clearModuleRegistry()
  registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: SessionRenderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
})
afterEach(() => { cleanup(); clearAllDrafts(); clearAllSendQueues(); vi.useRealTimers() })

describe('session input across tab switches', () => {
  it('keeps the typed draft when the reader switches to another tab and back', () => {
    const { rerender } = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'half a thought' } })

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('session-input')).toBeNull() // really unmounted
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()
    expect(readDraft(draftKey(paneId, H, SID))).toBe('half a thought')

    rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(box().value).toBe('half a thought')
  })

  it('keeps it across a full unmount and remount', () => {
    const first = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'keep me' } })
    first.unmount()
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(box().value).toBe('keep me')
  })

  it('a message in its undo window is still queued (and still goes out) after the switch', async () => {
    const { rerender } = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'ship it' } })
    fireEvent.keyDown(box(), { key: 'Enter' })
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(box().value).toBe('')
    expect(screen.getByTestId('queued-message')).toHaveTextContent('ship it')
    await act(() => vi.advanceTimersByTimeAsync(3000))
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})
