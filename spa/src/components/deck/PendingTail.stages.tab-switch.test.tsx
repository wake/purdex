// Regression (CLAUDE.md tab-hosted rule, MS2-3): the stage mark of a pending message survives a tab switch. Real TabContent (the pane really unmounts: keepAliveCount 0);
// the stage lives in the pane's send queue, outside the component, so the mark is there on return and a stage that arrived while switched away shows too.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { SessionInput } from './SessionInput'
import { PendingTail } from './PendingTail'
import { usePendingEntries } from '../../hooks/usePending'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { clearAllDrafts, draftKey } from '../../lib/conversations/draft-memory'
import { UNDO_MS, clearAllSendQueues, sendQueueFor } from '../../lib/conversations/send-queue'
import { bindAdapter, clearAllAdapters } from '../../lib/conversations/deck-adapter'
import { terminalAdapter } from '../../lib/conversations/terminal-adapter'
import { unboundSendPort } from '../../lib/conversations/send'
import { clearAllTakeBack } from '../../lib/conversations/take-back'

const fetchMock = vi.hoisted(() => vi.fn())
vi.mock('../../lib/host-api', () => ({ pinnedHostFetch: fetchMock }))

const H = 'h', SID = '11111111-2222-4333-8444-555555555555'

function SessionRenderer({ pane }: PaneRendererProps) {
  bindAdapter(draftKey(pane.id, H, SID), terminalAdapter(H, SID, undefined, () => ({ code: 'dev001', instance: 'g1' })))
  const entries = usePendingEntries(pane.id, H, SID)
  return (
    <>
      <SessionInput paneKey={pane.id} hostId={H} sessionId={SID} capabilities={{ send: 'keys' }} onSwitchToTerminal={() => {}} pendingInStream />
      <PendingTail paneKey={pane.id} hostId={H} sessionId={SID} entries={entries} queued={[]} />
    </>
  )
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
  fetchMock.mockReset().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ outcome: 'handed', mode: 'fill' }))))
  clearModuleRegistry()
  registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: SessionRenderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
})
afterEach(() => { cleanup(); clearAllDrafts(); clearAllSendQueues(); clearAllAdapters(); clearAllTakeBack(); vi.useRealTimers() })

describe('a stage mark across a tab switch', () => {
  it('the mark is still there on return, and a stage that came while switched away shows', async () => {
    const { rerender } = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'hello' } })
    fireEvent.keyDown(box(), { key: 'Enter' })
    await act(() => vi.advanceTimersByTimeAsync(UNDO_MS))
    const queue = sendQueueFor(draftKey(paneId, H, SID), () => unboundSendPort(H, SID))
    const id = queue.entries()[0].id
    act(() => queue.setStages({ [id]: { client_msg_id: id, stage: 'entered', at: 1 } }))
    expect(screen.getByTestId('stage-mark')).toHaveTextContent('At the host')

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('stage-mark')).toBeNull() // really unmounted
    act(() => queue.setStages({ [id]: { client_msg_id: id, stage: 'queued', at: 2 } }))

    rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getAllByTestId('queued-message')).toHaveLength(1)
    expect(screen.getByTestId('stage-mark')).toHaveTextContent('Queued')
  })
})
