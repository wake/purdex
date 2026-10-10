import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act, within } from '@testing-library/react'
import { StatusBar } from './StatusBar'
import { PaneLayoutRenderer } from './PaneLayoutRenderer'
import { clearModuleRegistry, registerModule } from '../lib/module-registry'
import { findPane } from '../lib/pane-tree'
import type { Tab, PaneContent, PaneLayout } from '../types/tab'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'
import { useTabStore } from '../stores/useTabStore'
import { usePaneFocusStore } from '../stores/usePaneFocusStore'
import { compositeKey } from '../lib/composite-key'
import { HOST_ID, GEN, cwdRefresh, setupStores, makeTab } from './StatusBar.test-helpers'

vi.mock('../lib/copy-text', () => ({ copyText: vi.fn(async () => {}) }))
// The worker bar reads the host quota through `fetchNexHost`; no test here may reach a real network.
vi.mock('../lib/nex/nex-api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/nex/nex-api')>()
  return { ...actual, fetchNexHost: vi.fn(async () => ({ active_account: 'a', quota: { five_hour_pct: 41, seven_day_pct: 72, resets_at: 0, source: 'usage_api' } })) }
})

beforeEach(() => {
  cleanup()
  setupStores()
})

// Shell cleanup P6 (spec §9.1, rule D.4): the whole bar shows one pane of the tab, the status target, not the
// primary pane. "Clicking" a pane here is what PaneLayoutRenderer's pointerdown writer does: a `touch` on the focus
// record.
describe('StatusBar status target pane', () => {
  const CC_CODE = 'dev001'
  const ccTerminal: PaneContent = { kind: 'tmux-session', hostId: HOST_ID, sessionCode: CC_CODE, mode: 'terminal', cachedName: '', tmuxInstance: '' }
  const plainTerminal: PaneContent = { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'plain01', mode: 'terminal', cachedName: '', tmuxInstance: '' }
  const editorContent: PaneContent = { kind: 'editor', source: { type: 'inapp' }, filePath: '/notes/a.md' }
  const workerContent: PaneContent = { kind: 'execution', executionId: 'exc_1', host: HOST_ID }

  function splitTab(id: string, left: { id: string; content: PaneContent }, right: { id: string; content: PaneContent }): Tab {
    const layout: PaneLayout = {
      type: 'split', id: `${id}-split`, direction: 'h',
      children: [{ type: 'leaf', pane: left }, { type: 'leaf', pane: right }],
      sizes: [50, 50],
    }
    return { ...makeTab(id, { kind: 'new-tab' }), layout }
  }

  const click = (tabId: string, paneId: string) => act(() => usePaneFocusStore.getState().touch(tabId, paneId))

  beforeEach(() => {
    setupStores()
    useUploadStore.setState({ sessions: {} })
    usePaneFocusStore.setState({ recent: {} })
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: CC_CODE, name: 'cc-session', cwd: '/tmp', mode: 'terminal', tmux_instance: GEN },
          { code: 'plain01', name: 'plain-shell', cwd: '/tmp', mode: 'terminal', tmux_instance: GEN },
        ],
      },
    })
    useAgentStore.setState({ agentTypes: { [compositeKey(HOST_ID, CC_CODE)]: 'cc' } })
  })

  it('D.4-3: editor on the left, CC terminal on the right, never clicked → the terminal’s session name', () => {
    useAgentStore.setState({ models: { [compositeKey(HOST_ID, CC_CODE)]: 'Claude Opus 4' } })
    render(<StatusBar activeTab={splitTab('t1', { id: 'ed', content: editorContent }, { id: 'cc', content: ccTerminal })} />)
    expect(screen.getByTestId('status-seg-session-name').textContent).toBe('cc-session')
    expect(screen.getByTestId('status-seg-host').textContent).toBe('mlab')
    // The agent decorations read the target as well, not the primary (editor) pane.
    expect(screen.getByTestId('agent-label').textContent).toBe('Claude Opus 4')
  })

  it('a worker pane shows the host quota, and a terminal beside it does not leak ccStatus into it', async () => {
    render(<StatusBar activeTab={splitTab('t1', { id: 'term', content: plainTerminal }, { id: 'w', content: workerContent })} />)
    await act(async () => {})
    expect(screen.getByTestId('status-seg-quota-five-hour').textContent).toBe('59%')
    expect(screen.getByTestId('status-seg-quota-seven-day').textContent).toBe('28%')
    // Both are in the controls, ahead of the mode buttons.
    const controls = screen.getByTestId('status-controls')
    const five = screen.getByTestId('status-seg-quota-five-hour')
    expect(controls.contains(five)).toBe(true)
    expect(five.compareDocumentPosition(screen.getByTestId('status-mode-buttons')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getByTestId('status-segments').querySelector('[data-testid^="status-seg-quota"]')).toBeNull()
    expect(screen.queryByTestId('status-seg-usage-context')).toBeNull()
  })

  it('D.4-1: worker + plain terminal → after a pointerdown on the plain terminal the bar still shows the worker', () => {
    render(<StatusBar activeTab={splitTab('t1', { id: 'term', content: plainTerminal }, { id: 'w', content: workerContent })} />)
    expect(screen.getByTestId('status-seg-worker-name')).toBeInTheDocument()
    click('t1', 'term')
    expect(screen.getByTestId('status-seg-worker-name')).toBeInTheDocument()
    expect(screen.queryByTestId('status-seg-session-name')).toBeNull()
    expect(screen.queryByText('plain-shell')).toBeNull()
  })

  it('D.4-1 end to end: after a real pointerdown on the plain terminal, the bar still shows the worker and its chat button switches the worker pane', () => {
    clearModuleRegistry()
    const stub = ({ pane }: { pane: { id: string } }) => <div data-testid={`pane-${pane.id}`} />
    registerModule({
      id: 'status-bar-test-panes',
      name: 'Stub panes',
      panes: [{ kind: 'tmux-session', component: stub }, { kind: 'execution', component: stub }],
    })
    // The plain terminal is the primary (first) pane: a button that acted on the primary would write the terminal.
    const tab = splitTab('t1', { id: 'term', content: plainTerminal }, { id: 'w', content: workerContent })
    useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1', visitHistory: [] })
    function Live() {
      const live = useTabStore((s) => s.tabs.t1)
      return (
        <>
          <PaneLayoutRenderer layout={live.layout} tabId="t1" isActive />
          <StatusBar activeTab={live} />
        </>
      )
    }
    render(<Live />)

    fireEvent.pointerDown(screen.getByTestId('pane-term'))
    expect(usePaneFocusStore.getState().recent.t1).toEqual(['term'])
    expect(screen.getByTestId('status-seg-worker-name')).toBeInTheDocument()
    expect(screen.queryByTestId('status-seg-session-name')).toBeNull()

    fireEvent.click(within(screen.getByTestId('status-bar')).getByRole('button', { name: 'Chat' }))
    const layout = useTabStore.getState().tabs.t1.layout
    expect(findPane(layout, 'w')?.content).toEqual({ ...workerContent, mode: 'chat' })
    expect(findPane(layout, 'term')?.content).toEqual(plainTerminal)
    expect(within(screen.getByTestId('status-bar')).getByRole('button', { name: 'Chat' }).getAttribute('aria-pressed')).toBe('true')
    clearModuleRegistry()
  })

  it('D.4-2: two agent panes → follows the click', () => {
    useAgentStore.setState({ agentTypes: { [compositeKey(HOST_ID, CC_CODE)]: 'cc', [compositeKey(HOST_ID, 'plain01')]: 'codex' } })
    render(<StatusBar activeTab={splitTab('t1', { id: 'cc', content: ccTerminal }, { id: 'cx', content: plainTerminal })} />)
    expect(screen.getByTestId('status-seg-session-name').textContent).toBe('cc-session')
    click('t1', 'cx')
    expect(screen.getByTestId('status-seg-session-name').textContent).toBe('plain-shell')
    click('t1', 'cc')
    expect(screen.getByTestId('status-seg-session-name').textContent).toBe('cc-session')
  })

  it('rule 3: no agent pane → the clicked pane; an editor target renders no bar', () => {
    useAgentStore.setState({ agentTypes: {} })
    const tab = splitTab('t1', { id: 'term', content: plainTerminal }, { id: 'ed', content: editorContent })
    const { container } = render(<StatusBar activeTab={tab} />)
    expect(screen.getByTestId('status-seg-session-name').textContent).toBe('plain-shell')
    click('t1', 'ed')
    expect(container).toBeEmptyDOMElement()
    click('t1', 'term')
    expect(screen.getByTestId('status-seg-session-name').textContent).toBe('plain-shell')
  })

  it('rule 4: no agent pane and no record → the primary pane', () => {
    useAgentStore.setState({ agentTypes: {} })
    const { container } = render(<StatusBar activeTab={splitTab('t1', { id: 'ed', content: editorContent }, { id: 'term', content: plainTerminal })} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('peer data is asked for the target session, not the primary pane', () => {
    render(<StatusBar activeTab={splitTab('t1', { id: 'ed', content: editorContent }, { id: 'cc', content: ccTerminal })} />)
    expect(cwdRefresh).toHaveBeenCalledWith(HOST_ID, CC_CODE)
  })
})
