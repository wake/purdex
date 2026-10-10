import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { StatusBar } from './StatusBar'
import { useAgentStore } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'
import { useSessionStore } from '../stores/useSessionStore'
import { compositeKey } from '../lib/composite-key'
import { HOST_ID, GEN, peerRefresh, setupStores, sessionTab, makeTab } from './StatusBar.test-helpers'

vi.mock('../lib/copy-text', () => ({ copyText: vi.fn(async () => {}) }))

beforeEach(() => {
  cleanup()
  setupStores()
})

describe('StatusBar', () => {
  it('renders host and session info', () => {
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    expect(screen.getByText('mlab')).toBeTruthy()
    expect(screen.getByText('dev-server')).toBeTruthy()
    expect(screen.getByText('connected')).toBeTruthy()
  })

  it('renders empty state when no active tab', () => {
    render(<StatusBar activeTab={null} />)
    expect(screen.getByText('No active session')).toBeTruthy()
  })

  it('shows no view-mode badge or dropdown for session tabs (P-D.3: terminal is the only mode)', () => {
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    expect(screen.queryByTestId('status-view-mode')).toBeNull()
    expect(screen.queryByTitle('Toggle view mode')).toBeNull()
  })

  it('shows simplified status for non-session tabs', () => {
    const tab = makeTab('t1', { kind: 'dashboard' })
    render(<StatusBar activeTab={tab} />)
    expect(screen.getByText('dashboard')).toBeTruthy()
    expect(screen.queryByTitle('Toggle view mode')).toBeNull()
  })

  it('does not render the global fallback bar for editor tabs', () => {
    const tab = makeTab('t1', { kind: 'editor', source: { type: 'inapp' }, filePath: '/notes/a.md' })
    const { container } = render(<StatusBar activeTab={tab} />)
    expect(container).toBeEmptyDOMElement()
  })

  // Shell cleanup P6 (spec D.3, §9.6): the split buttons are gone — splitting stays in the title bar and the pane
  // context menu — and the mode buttons (§9.3) take their place in the controls block.
  it('a tmux-session bar has the mode buttons in its controls block, terminal pressed, and no split buttons', () => {
    render(<StatusBar activeTab={sessionTab()} />)
    const modes = screen.getByTestId('status-mode-buttons')
    expect(screen.getByTestId('status-controls')).toContainElement(modes)
    expect(screen.getByRole('button', { name: 'Terminal' }).getAttribute('aria-pressed')).toBe('true')
    expect(screen.queryByTestId('status-split-buttons')).toBeNull()
    expect(screen.queryByTitle('Split Horizontal')).toBeNull()
    expect(screen.queryByTitle('Split Vertical')).toBeNull()
  })

  it('no mode buttons for a non-session pane or when there is no active tab', () => {
    render(<StatusBar activeTab={makeTab('t1', { kind: 'dashboard' })} />)
    expect(screen.queryByTestId('status-mode-buttons')).toBeNull()
    cleanup()
    render(<StatusBar activeTab={null} />)
    expect(screen.queryByTestId('status-mode-buttons')).toBeNull()
  })

  it('falls back to sessionCode when session not in store', () => {
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'unknown999', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    expect(screen.getByText('unknown999')).toBeTruthy()
  })
})

describe('StatusBar upload progress', () => {
  beforeEach(() => {
    setupStores()
    useUploadStore.setState({ sessions: {} })
    useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {} })
  })

  it('shows uploading progress', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useUploadStore.setState({
      sessions: { [ck]: { total: 5, completed: 1, failed: 0, currentFile: 'photo.png', status: 'uploading' } },
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    expect(screen.getByTestId('upload-status')).toBeTruthy()
    expect(screen.getByText(/photo\.png/)).toBeTruthy()
    expect(screen.getByText(/2\/5/)).toBeTruthy()
  })

  it('shows typing status after upload completes', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useUploadStore.setState({
      sessions: { [ck]: { total: 2, completed: 2, failed: 0, currentFile: '', status: 'typing' } },
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    expect(screen.getByTestId('upload-status')).toBeTruthy()
    expect(screen.getByText('Typing into session...')).toBeTruthy()
  })

  it('shows upload done', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useUploadStore.setState({
      sessions: { [ck]: { total: 3, completed: 3, failed: 0, currentFile: '', status: 'done' } },
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    expect(screen.getByText(/3 files uploaded/)).toBeTruthy()
  })

  it('shows upload error', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useUploadStore.setState({
      sessions: { [ck]: { total: 1, completed: 0, failed: 1, currentFile: '', error: 'bad.mp4', status: 'error' } },
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    expect(screen.getByText(/bad\.mp4/)).toBeTruthy()
  })

  // #2501: the reason, worded like the deck / chat input
  it.each([
    [{ kind: 'too_large', status: 0 }, /File too large: bad\.mp4/],
    [{ kind: 'too_many', status: 0 }, /Too many files uploading/],
    [{ kind: 'not_found', status: 0 }, /Session not found; cannot upload/],
    [{ kind: 'network', status: 0 }, /Connection lost; upload failed: bad\.mp4/],
    [{ kind: 'http', status: 500 }, /Upload failed \(500\): bad\.mp4/],
  ])('shows the reason for an upload error %j', (errorCause, text) => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useUploadStore.setState({
      sessions: { [ck]: { total: 1, completed: 0, failed: 1, currentFile: '', error: 'bad.mp4', errorCause, status: 'error' } },
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    expect(screen.getByTestId('upload-status').textContent).toMatch(text)
  })
})

// A conversation's rebuild tab (conversation entity spec §13.4) has no session code: the bar names it by the session
// it would create, the pane's cached name, rather than by an empty string.
describe('StatusBar — a conversation-ended pane', () => {
  it('shows the pane cached name as the session name; the rest of the bar is a closed pane bar', () => {
    render(<StatusBar activeTab={makeTab('t1', {
      kind: 'tmux-session', hostId: HOST_ID, sessionCode: '', mode: 'terminal', cachedName: 'proj-2', tmuxInstance: GEN,
      terminated: 'conversation-ended',
    })} />)
    expect(screen.getByTestId('status-seg-session-name').textContent).toBe('proj-2')
    expect(screen.getByTestId('status-seg-peer-id')).toBeInTheDocument()
    expect(peerRefresh).not.toHaveBeenCalled()
  })

  it('an ordinary closed pane is unchanged: its live session name, else its code', () => {
    const closed = (sessionCode: string) => makeTab('t1', {
      kind: 'tmux-session', hostId: HOST_ID, sessionCode, mode: 'terminal', cachedName: 'my-session', tmuxInstance: GEN,
      terminated: 'session-closed',
    })
    render(<StatusBar activeTab={closed('dev001')} />)
    expect(screen.getByTestId('status-seg-session-name').textContent).toBe('dev-server')
    cleanup()
    render(<StatusBar activeTab={closed('gone01')} />)
    expect(screen.getByTestId('status-seg-session-name').textContent).toBe('gone01')
  })
})
