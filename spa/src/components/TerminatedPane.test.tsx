import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { TerminatedPane } from './TerminatedPane'
import { useTabStore } from '../stores/useTabStore'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import type { PaneContent, Tab } from '../types/tab'

vi.mock('./SessionPickerList', () => ({
  SessionPickerList: ({ onSelect }: { onSelect: (sel: unknown) => void }) => (
    <button
      data-testid="session-picker"
      onClick={() =>
        onSelect({
          hostId: 'new-host',
          sessionCode: 'new001',
          cachedName: 'new-session',
          tmuxInstance: 'tmux:inst',
        })
      }
    >
      Mock SessionPickerList
    </button>
  ),
}))

const TAB_ID = 'tab-1'
const PANE_ID = 'pane-1'

function makeContent(reason: 'session-closed' | 'tmux-restarted' | 'host-removed', mode: 'terminal' | 'stream' = 'terminal'): Extract<PaneContent, { kind: 'tmux-session' }> {
  return {
    kind: 'tmux-session',
    hostId: 'host-1',
    sessionCode: 'dev001',
    mode,
    cachedName: 'my-session',
    tmuxInstance: '123:456',
    terminated: reason,
  }
}

function setupTab(content: PaneContent) {
  const tab: Tab = {
    id: TAB_ID,
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: PANE_ID, content } },
  }
  useTabStore.setState({
    tabs: { [TAB_ID]: tab },
    tabOrder: [TAB_ID],
    activeTabId: TAB_ID,
  })
}

beforeEach(() => {
  cleanup()
  useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {}, activeHostId: null })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
})

describe('TerminatedPane', () => {
  it('shows session-closed message', () => {
    const content = makeContent('session-closed')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByText('Session closed')).toBeInTheDocument()
    expect(screen.getByText('my-session no longer exists')).toBeInTheDocument()
  })

  it('shows tmux-restarted message', () => {
    const content = makeContent('tmux-restarted')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByText('tmux restarted')).toBeInTheDocument()
    expect(screen.getByText('Previous sessions are no longer valid')).toBeInTheDocument()
  })

  it('shows host-removed message', () => {
    const content = makeContent('host-removed')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByText('Host removed')).toBeInTheDocument()
    expect(screen.getByText('This host has been removed')).toBeInTheDocument()
  })

  it('has a close tab button that calls closeTab', () => {
    const content = makeContent('session-closed')
    setupTab(content)
    useWorkspaceStore.getState().reset()
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, TAB_ID)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)

    const closeBtn = screen.getByText('Close tab')
    expect(closeBtn).toBeInTheDocument()

    fireEvent.click(closeBtn)
    // Tab should be closed
    expect(useTabStore.getState().tabs[TAB_ID]).toBeUndefined()
  })

  it('session selection calls setPaneContent with correct data, preserving mode', () => {
    const content = makeContent('session-closed', 'stream')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)

    fireEvent.click(screen.getByTestId('session-picker'))

    const updatedTab = useTabStore.getState().tabs[TAB_ID]
    expect(updatedTab).toBeDefined()
    if (updatedTab.layout.type === 'leaf') {
      const newContent = updatedTab.layout.pane.content
      expect(newContent).toEqual({
        kind: 'tmux-session',
        hostId: 'new-host',
        sessionCode: 'new001',
        mode: 'stream', // preserved from original tab
        cachedName: 'new-session',
        tmuxInstance: 'tmux:inst',
      })
    }
  })

  it('session selection for terminal mode tab preserves terminal mode', () => {
    const content = makeContent('session-closed', 'terminal')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)

    fireEvent.click(screen.getByTestId('session-picker'))

    const updatedTab = useTabStore.getState().tabs[TAB_ID]
    if (updatedTab.layout.type === 'leaf') {
      const newContent = updatedTab.layout.pane.content
      expect(newContent.kind).toBe('tmux-session')
      if (newContent.kind === 'tmux-session') {
        expect(newContent.mode).toBe('terminal')
      }
    }
  })

  it('renders the SessionPickerList', () => {
    const content = makeContent('session-closed')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByTestId('session-picker')).toBeInTheDocument()
  })
})
