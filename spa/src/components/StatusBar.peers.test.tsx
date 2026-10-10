import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act, waitFor } from '@testing-library/react'
import { StatusBar } from './StatusBar'
import type { PaneContent } from '../types/tab'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useTabStore } from '../stores/useTabStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { usePeerStore, type PeerHostEntry } from '../stores/usePeerStore'
import { compositeKey } from '../lib/composite-key'
import {
  HOST_ID, GEN, PEER_ROW, peerRefresh, cwdRefresh, copyTextMock,
  setupStores, seedPeers, seedCwd, sessionTab, makeTab,
} from './StatusBar.test-helpers'

vi.mock('../lib/copy-text', () => ({ copyText: vi.fn(async () => {}) }))

beforeEach(() => {
  cleanup()
  setupStores()
})

// Peer information in the status bar (peer-info-panel spec §4).
describe('StatusBar peer segments', () => {
  beforeEach(() => {
    setupStores()
    useUploadStore.setState({ sessions: {} })
    seedPeers()
    seedCwd('/Users/wake/Workspace/wake/purdex')
  })

  it('renders host, cwd, peer name, peer id and status, each in its own element', () => {
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-seg-host').textContent).toBe('mlab')
    expect(screen.getByTestId('status-seg-cwd').textContent).toBe('/Users/wake/Workspace/wake/purdex')
    // The *name* is displayed with its ref; the full address is what a click copies.
    expect(screen.getByTestId('status-seg-peer-id').textContent).toBe('purdex-b0 [q34psn]')
    expect(screen.getByTestId('status-seg-status').textContent).toContain('connected')
  })

  // Spec §5.8. The two strings differ on purpose: the display drops the host
  // because the row already shows it, while the clipboard keeps the host *and*
  // the ref, because a copied address is pasted hours later — exactly the window
  // in which a name drifts or is taken by someone else.
  it('shows the name with its ref and copies the exact form', async () => {
    render(<StatusBar activeTab={sessionTab()} />)
    const seg = screen.getByText(/purdex-b0 \[q34psn\]/)
    expect(seg.textContent).not.toContain('mlab/')
    fireEvent.click(seg)
    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith('mlab/purdex-b0 [q34psn]'))
  })

  // The address segment (name only, no ref) duplicated the peer id segment, which already carries the name, the ref
  // and the title. It is gone; the peer id segment is the one place the address is shown and copied from.
  it('has no separate address segment, and the peer id segment stays', () => {
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.queryByTestId('status-seg-agent')).toBeNull()
    expect(screen.getByTestId('status-seg-peer-id')).toBeTruthy()
    expect(screen.getByTestId('status-segments').textContent).not.toContain('ai-chat-story-3a')
  })

  // The old guard declined to render a row whose *title* was empty. Under v4 a
  // title is usually empty and never identified a row, so an untitled peer must
  // still appear — its name is what identifies it.
  it('renders a peer that has no title', () => {
    render(<StatusBar activeTab={sessionTab()} />)
    expect(PEER_ROW.title).toBe('')
    expect(screen.getByTestId('status-seg-peer-id').textContent).toContain('purdex-b0')
    expect(screen.getByTestId('status-seg-peer-id')).not.toBeDisabled()
  })

  it('renders a set title beside the name rather than instead of it', () => {
    seedPeers({}, { ...PEER_ROW, title: 'Purdex Tester 01', titleSource: 'user' })
    render(<StatusBar activeTab={sessionTab()} />)
    const seg = screen.getByTestId('status-seg-peer-id')
    expect(seg.textContent).toContain('purdex-b0 [q34psn]')
    expect(seg.textContent).toContain('Purdex Tester 01')
  })

  // A session whose registry name is not routable is addressed by its ref, so
  // the address already ends in it. Bracketing would say the same thing twice.
  it('leaves an address that is already its ref unbracketed', async () => {
    seedPeers({}, { ...PEER_ROW, address: 'mlab/_q34psn' })
    render(<StatusBar activeTab={sessionTab()} />)
    const seg = screen.getByTestId('status-seg-peer-id')
    expect(seg.textContent).toBe('_q34psn')
    fireEvent.click(seg)
    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith('mlab/_q34psn'))
  })

  it('renders and copies the address unchanged when the row has no ref', async () => {
    seedPeers({}, { ...PEER_ROW, ref: '' })
    render(<StatusBar activeTab={sessionTab()} />)
    const seg = screen.getByTestId('status-seg-peer-id')
    expect(seg.textContent).toBe('purdex-b0')
    fireEvent.click(seg)
    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith('mlab/purdex-b0'))
  })

  it('separates segments with border rules, never a pipe glyph that would be copied with the text', () => {
    render(<StatusBar activeTab={sessionTab()} />)
    const seps = screen.getAllByTestId('status-separator')
    expect(seps.length).toBeGreaterThan(0)
    for (const sep of seps) {
      expect(sep.textContent).toBe('')
      expect(sep.className).toContain('border-l')
    }
    expect(screen.getByTestId('status-segments').textContent).not.toContain('|')
  })

  it.each([
    ['status-seg-host', 'mlab', 'copied: host'],
    ['status-seg-cwd', '/Users/wake/Workspace/wake/purdex', 'copied: cwd'],
    ['status-seg-peer-id', 'mlab/purdex-b0 [q34psn]', 'copied: peer id'],
  ])('%s copies its value and confirms in the fixed slot', async (testId, value, message) => {
    render(<StatusBar activeTab={sessionTab()} />)
    fireEvent.click(screen.getByTestId(testId))
    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith(value))
    await waitFor(() => expect(screen.getByTestId('status-copy-feedback').textContent).toBe(message))
  })

  // The host segment carries two gestures: the single click that copies (new),
  // and the double click that opens host settings (pre-existing). A browser
  // dispatches two `click`s before `dblclick`, so without a grace period the
  // navigating gesture also copies — silently putting the host name on the
  // clipboard over whatever the user had there.
  describe('the host segment carries two gestures', () => {
    beforeEach(() => { vi.useFakeTimers() })
    afterEach(() => { vi.useRealTimers() })

    it('a double-click navigates and copies nothing', () => {
      const onNavigateToHost = vi.fn()
      render(<StatusBar activeTab={sessionTab()} onNavigateToHost={onNavigateToHost} />)
      const seg = screen.getByTestId('status-seg-host')
      // What a browser actually sends for one double-click.
      fireEvent.click(seg)
      fireEvent.click(seg)
      fireEvent.dblClick(seg)
      act(() => { vi.advanceTimersByTime(2000) })
      expect(onNavigateToHost).toHaveBeenCalledWith(HOST_ID)
      expect(copyTextMock).not.toHaveBeenCalled()
      expect(screen.getByTestId('status-copy-feedback').textContent).toBe('')
    })

    it('a single click still copies once the double-click window has passed', async () => {
      render(<StatusBar activeTab={sessionTab()} onNavigateToHost={vi.fn()} />)
      fireEvent.click(screen.getByTestId('status-seg-host'))
      expect(copyTextMock).not.toHaveBeenCalled()
      await act(async () => { vi.advanceTimersByTime(2000) })
      expect(copyTextMock).toHaveBeenCalledWith('mlab')
      expect(screen.getByTestId('status-copy-feedback').textContent).toBe('copied: host')
    })

    it('a segment with no double-click gesture copies immediately', async () => {
      render(<StatusBar activeTab={sessionTab()} />)
      await act(async () => { fireEvent.click(screen.getByTestId('status-seg-cwd')) })
      expect(copyTextMock).toHaveBeenCalledWith('/Users/wake/Workspace/wake/purdex')
    })
  })

  it('the status segment is not a button — it is not a value to copy', () => {
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-seg-status').tagName).not.toBe('BUTTON')
  })

  it('shows a distinct message when copyText rejects', async () => {
    copyTextMock.mockRejectedValueOnce(new Error('copy unsupported'))
    render(<StatusBar activeTab={sessionTab()} />)
    fireEvent.click(screen.getByTestId('status-seg-cwd'))
    await waitFor(() => expect(screen.getByTestId('status-copy-feedback').textContent).toBe('copy failed'))
  })

  it('keeps the feedback slot present and fixed-width before any copy, so a copy does not reflow the row', () => {
    render(<StatusBar activeTab={sessionTab()} />)
    const slot = screen.getByTestId('status-copy-feedback')
    expect(slot.textContent).toBe('')
    expect(slot.className).toMatch(/\bw-\[?\d/)
    expect(slot.className).toContain('shrink-0')
  })

  it('dims a stale peer id but still copies it on click — it does not refresh (spec §3.4)', async () => {
    seedPeers({ fetchedAt: Date.now() - 90_000 })
    render(<StatusBar activeTab={sessionTab()} />)
    const seg = screen.getByTestId('status-seg-peer-id')
    expect(seg).toHaveAttribute('data-dim', 'true')
    peerRefresh.mockClear()
    fireEvent.click(seg)
    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith('mlab/purdex-b0 [q34psn]'))
    expect(peerRefresh).not.toHaveBeenCalled()
  })

  // Seen on a real machine: the bar showed `purdex-4a` for a pane whose tmux
  // session now held `purdex-bb`. The old agent exited and a new one started
  // in the SAME tmux session, so the generation guard had nothing to catch.
  // The SPA does hear about it — the owner SessionStart writes the new session
  // id into the pane's rebuild record — and that write must re-read the rows.
  it('re-reads the rows when a SessionStart replaces the Claude Code session behind the pane', async () => {
    seedPeers({}, { ...PEER_ROW, address: 'mlab/purdex-4a', ref: '_4a4a4a' })
    const tab = sessionTab()
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
    act(() => {
      useTabStore.getState().setPaneRebuild(HOST_ID, 'dev001', GEN, {
        kind: 'agent-group',
        record: { tmuxInstance: GEN, agent: { type: 'cc', sessionId: 'sid-4a', updatedAt: 1 }, capturedAt: 1 },
      })
    })
    // The refreshed answer names the new occupant.
    peerRefresh.mockImplementationOnce(async (hostId) => {
      usePeerStore.setState((s) => ({
        byHost: { ...s.byHost, [hostId]: { ...s.byHost[hostId], rows: { dev001: { ...PEER_ROW, address: 'mlab/purdex-bb', ref: '_bbbbbb' } }, fetchedAt: Date.now() } },
      }))
    })
    render(<StatusBar activeTab={tab} />)
    expect(screen.getByTestId('status-seg-peer-id').textContent).toBe('purdex-4a [4a4a4a]')
    peerRefresh.mockClear()
    act(() => {
      useAgentStore.getState().handleNormalizedEvent(HOST_ID, 'dev001', {
        agent_type: 'cc', status: 'idle', raw_event_name: 'PdxSessionStart', broadcast_ts: 2, subagents: [],
        detail: { pdx_provenance: { owner_session_start: true, agent_type: 'cc', session_id: 'sid-bb', cwd: '/w', tmux_pane_id: '%1', tmux_instance: GEN } },
      })
    })
    await waitFor(() => expect(peerRefresh).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(screen.getByTestId('status-seg-peer-id').textContent).toBe('purdex-bb [bbbbbb]'))
  })

  it('the refresh control refreshes both stores once', async () => {
    render(<StatusBar activeTab={sessionTab()} />)
    peerRefresh.mockClear()
    cwdRefresh.mockClear()
    fireEvent.click(screen.getByTestId('status-peer-refresh'))
    await waitFor(() => expect(peerRefresh).toHaveBeenCalledTimes(1))
    expect(peerRefresh).toHaveBeenCalledWith(HOST_ID)
    expect(cwdRefresh).toHaveBeenCalledTimes(1)
  })

  // Shell polish spec §4 (rule F): a mouse press on a copy segment or the refresh control leaves focus on the pane.
  // jsdom does not focus on mousedown, so `fireEvent.mouseDown(...) === false` proves the button is wired to
  // `keepFocus`; the helper itself is proven in a real browser (spec §5).
  it('a mouse press on a copy segment keeps focus where it was; a press then a click still copies', async () => {
    render(<StatusBar activeTab={sessionTab()} />)
    for (const testId of ['status-seg-host', 'status-seg-cwd', 'status-seg-peer-id']) {
      const seg = screen.getByTestId(testId)
      expect(fireEvent.mouseDown(seg), testId).toBe(false)
      expect(seg.tabIndex, testId).toBeGreaterThanOrEqual(0)
    }
    const cwd = screen.getByTestId('status-seg-cwd')
    await act(async () => {
      fireEvent.mouseDown(cwd)
      fireEvent.click(cwd)
    })
    expect(copyTextMock).toHaveBeenCalledWith('/Users/wake/Workspace/wake/purdex')
    expect(screen.getByTestId('status-copy-feedback').textContent).toBe('copied: cwd')
  })

  it('a mouse press on the refresh control keeps focus where it was; a press then a click still refreshes', async () => {
    render(<StatusBar activeTab={sessionTab()} />)
    peerRefresh.mockClear()
    cwdRefresh.mockClear()
    const refresh = screen.getByTestId('status-peer-refresh')
    expect(fireEvent.mouseDown(refresh)).toBe(false)
    expect(refresh.tabIndex).toBeGreaterThanOrEqual(0)
    fireEvent.click(refresh)
    await waitFor(() => expect(peerRefresh).toHaveBeenCalledTimes(1))
    expect(cwdRefresh).toHaveBeenCalledTimes(1)
  })

  it('disables the refresh control while loading', () => {
    seedPeers({ loading: true })
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-peer-refresh')).toBeDisabled()
  })

  it('disables the refresh control when the host is not connected', () => {
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } })
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-peer-refresh')).toBeDisabled()
  })

  it('dims the peer id and agent when the host is not connected', () => {
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } })
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-seg-peer-id')).toHaveAttribute('data-dim', 'true')
  })

  // Uncertainty is signalled beside the value, not by darkening it. Two
  // rounds of "make it dimmer" drew the same complaint from a live screenshot
  // both times: a segment darker than its neighbours reads as broken, not as
  // provisional. The text now always matches host and session name; the
  // refresh control — which sits next to the peer id and is what fixes the
  // condition — carries the state.
  it('a stale peer id is as bright as its neighbours, and the refresh control carries the state', () => {
    seedPeers({ fetchedAt: Date.now() - 90_000 })
    seedCwd('/tmp/here')
    render(<StatusBar activeTab={sessionTab()} />)

    const peerId = screen.getByTestId('status-seg-peer-id')
    const host = screen.getByTestId('status-seg-host')
    // Same colour class, and no opacity class anywhere on the stale segment.
    expect(peerId.className).toContain('text-text-secondary')
    expect(host.className).toContain('text-text-secondary')
    expect(peerId.className).not.toMatch(/opacity-\d/)

    expect(screen.getByTestId('status-peer-refresh')).toHaveAttribute('data-stale', 'true')
  })

  it('the refresh control is not marked stale for a fresh answer', () => {
    seedPeers()
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-peer-refresh')).not.toHaveAttribute('data-stale')
  })

  it.each([
    ['a failed fetch', { error: 'boom' } as Partial<PeerHostEntry>, /boom/],
    ['a partial envelope with no row', { envelope: { partial: true, titlesUnavailable: false, unknownRegistryFiles: [] } } as Partial<PeerHostEntry>, /could not be determined/],
    ['a complete envelope with no row', {} as Partial<PeerHostEntry>, /no peer/],
  ])('renders an em dash for %s, with the reason in the tooltip', (_label, entry, tooltip) => {
    seedPeers(entry, null)
    render(<StatusBar activeTab={sessionTab()} />)
    const seg = screen.getByTestId('status-seg-peer-id')
    expect(seg.textContent).toBe('—')
    expect(seg.getAttribute('title')).toMatch(tooltip)
  })

  // A failed refresh is not a stale address with a warning on it: spec §6 says
  // the status bar shows `—` and keeps the error in the tooltip. The row the
  // cache still holds is the row the daemon has just refused to confirm, and the
  // segment is click-to-copy — the value would go into `pdx msg send`.
  it('shows an em dash, not the last known address, when the refresh failed', () => {
    seedPeers({ error: 'connection refused' })
    render(<StatusBar activeTab={sessionTab()} />)
    const seg = screen.getByTestId('status-seg-peer-id')
    expect(seg.textContent).toBe('—')
    expect(seg.getAttribute('title')).toMatch(/connection refused/)
  })

  it('offers nothing to copy while the refresh is failing', () => {
    seedPeers({ error: 'connection refused' })
    render(<StatusBar activeTab={sessionTab()} />)
    for (const testId of ['status-seg-peer-id']) {
      const seg = screen.getByTestId(testId)
      expect(seg, testId).toBeDisabled()
      fireEvent.click(seg)
    }
    expect(copyTextMock).not.toHaveBeenCalled()
  })

  it('renders an em dash for a row that has neither a ref nor an address', () => {
    seedPeers({}, { ...PEER_ROW, ref: '', address: '', agent: null })
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-seg-peer-id').textContent).toBe('—')
  })

  it.each(['inbox_dead', 'ambiguous'])('shows an %s row dimmed, with its reason in the tooltip', (reason) => {
    seedPeers({}, { ...PEER_ROW, deliverable: false, reason })
    render(<StatusBar activeTab={sessionTab()} />)
    const seg = screen.getByTestId('status-seg-peer-id')
    expect(seg.textContent).toBe('purdex-b0 [q34psn]')
    expect(seg).toHaveAttribute('data-dim', 'true')
    expect(seg.getAttribute('title')).toContain('mlab/purdex-b0')
  })

  it('each copy control is a native focusable button (Enter/Space activation is the platform’s — jsdom does not simulate it)', () => {
    render(<StatusBar activeTab={sessionTab()} />)
    for (const testId of ['status-seg-host', 'status-seg-cwd', 'status-seg-peer-id', 'status-peer-refresh']) {
      const el = screen.getByTestId(testId)
      expect(el.tagName, testId).toBe('BUTTON')
      expect(el.getAttribute('tabindex'), testId).toBeNull()
      expect((el as HTMLButtonElement).disabled, testId).toBe(false)
      ;(el as HTMLButtonElement).focus()
      expect(document.activeElement, testId).toBe(el)
    }
  })

  it('renders the narrow-width decisions with a model badge, an upload and a pane title all present (jsdom does no layout — see the manual 400px check)', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useUploadStore.setState({
      sessions: { [ck]: { total: 5, completed: 1, failed: 0, currentFile: 'photo.png', status: 'uploading' } },
    })
    // The three things that live in the fixed-width `shrink-0` controls group
    // are present together: this is the state §4.3's priority list is *about*,
    // and the one that overflowed 400 px.
    useAgentStore.setState({ models: { [ck]: 'Claude Opus 4' } })
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'dev001', name: 'dev-server', cwd: '/tmp', mode: 'terminal', tmux_instance: GEN, pane_title: 'plan review' },
        ],
      },
      activeHostId: HOST_ID,
      activeCode: null,
    })
    useAgentStore.setState({ agentTypes: { [ck]: 'cc' } })
    useUISettingsStore.setState({ showAgentTitleInStatusBar: true })
    render(<StatusBar activeTab={sessionTab()} />)

    // Dropped, in the order spec §4.3 gives up on them.
    expect(screen.getByTestId('status-seg-cwd').className).toContain('max-[600px]:hidden')
    expect(screen.getByTestId('agent-pane-title').className).toContain('max-[700px]:hidden')
    // The model badge is the third of the agent-identity decorations, and it
    // sits in the `shrink-0` group, so without a rule of its own a long model
    // name ("Claude Opus 4") takes its width off the top of the row.
    expect(screen.getByTestId('agent-label').className).toContain('max-[700px]:hidden')
    expect(screen.getByTestId('agent-label').className).toContain('truncate')
    expect(screen.getByTestId('agent-label').className).toMatch(/max-w-\[\d+ch\]/)
    expect(screen.getByTestId('status-mode-buttons').className).toContain('max-[500px]:hidden')
    // Never dropped.
    for (const testId of ['status-seg-status', 'upload-status', 'status-seg-host', 'status-seg-peer-id']) {
      expect(screen.getByTestId(testId).className, testId).not.toContain(':hidden')
    }
    expect(screen.getByTestId('status-seg-status').className).toContain('shrink-0')
    // Host survives by shrinking to 8ch, not by disappearing.
    expect(screen.getByTestId('status-seg-host').className).toContain('max-[500px]:max-w-[8ch]')
    // Truncation, per segment.
    for (const testId of ['status-seg-host', 'status-seg-cwd', 'status-seg-peer-id', 'status-seg-session-name']) {
      expect(screen.getByTestId(testId).className, testId).toContain('truncate')
    }
    // cwd truncates from the *left*: its tail is the informative end.
    expect(screen.getByTestId('status-seg-cwd').style.direction).toBe('rtl')
  })

  it('lays the row out as three containers, with ml-auto only on the controls', () => {
    render(<StatusBar activeTab={sessionTab()} />)
    const segments = screen.getByTestId('status-segments')
    const controls = screen.getByTestId('status-controls')
    expect(segments.className).toContain('min-w-0')
    expect(segments.className).not.toContain('ml-auto')
    expect(controls.className).toContain('shrink-0')
    expect(controls.className).toContain('ml-auto')
    expect(screen.getByTestId('status-bar').querySelectorAll('.ml-auto').length).toBe(1)
  })

  it.each([
    ['no active tab', null],
    ['an editor pane', { kind: 'editor', source: { type: 'inapp' }, filePath: '/notes/a.md' } as PaneContent],
    ['a non-tmux pane', { kind: 'dashboard' } as PaneContent],
    // Codex R1: a terminated pane has no peer, and a tmux restart may have
    // handed its session code to somebody else — so asking would risk
    // rendering, and copying, a stranger's address.
    ['a terminated tmux pane', {
      kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal',
      cachedName: '', tmuxInstance: GEN, terminated: 'tmux-restarted',
    } as PaneContent],
  ])('does not fetch peer data for %s', (_label, content) => {
    render(<StatusBar activeTab={content ? makeTab('t1', content) : null} />)
    expect(peerRefresh).not.toHaveBeenCalled()
    expect(cwdRefresh).not.toHaveBeenCalled()
  })
})

// The join is (host, session code, tmux generation), never (host, session code)
// alone: tmux hands `$N` out from zero again after a restart, so a row cached
// before the restart can carry another session's address under this pane's code.
// Showing it is bad; this segment is click-to-copy, so it would be pasted into
// `pdx msg send` and reach a stranger's agent.
describe('StatusBar peer generation', () => {
  beforeEach(() => {
    setupStores()
    useUploadStore.setState({ sessions: {} })
    seedPeers()
    seedCwd('/Users/wake/Workspace/wake/purdex')
  })

  function setSessionGeneration(tmux_instance: string | undefined) {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'dev001', name: 'dev-server', cwd: '/tmp', mode: 'terminal', tmux_instance },
        ],
      },
      activeHostId: HOST_ID,
      activeCode: null,
    })
  }

  it('shows the peer id when the pane and the row share a generation', () => {
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-seg-peer-id').textContent).toBe('purdex-b0 [q34psn]')
  })

  it.each([
    ['the row is from another tmux server', GEN, '4242:1700000000'],
    ['the pane generation is unknown', '', GEN],
    ['the daemon did not stamp the row', GEN, ''],
    ['neither side knows', '', ''],
  ])('shows no peer when %s', (_label, paneGen, rowGen) => {
    setSessionGeneration(paneGen)
    seedPeers({}, { ...PEER_ROW, tmuxInstance: rowGen })
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-seg-peer-id').textContent).toBe('—')
  })

  it('offers nothing to copy for a row from another generation', () => {
    setSessionGeneration('4242:1700000000')
    render(<StatusBar activeTab={sessionTab()} />)
    const seg = screen.getByTestId('status-seg-peer-id')
    expect(seg).toBeDisabled()
    fireEvent.click(seg)
    expect(copyTextMock).not.toHaveBeenCalled()
  })

  it('shows no peer for a session the store has not reconciled yet — no generation to match on', () => {
    setSessionGeneration(undefined)
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-seg-peer-id').textContent).toBe('—')
  })

  it('still shows the cwd on a generation mismatch — that reading is the daemon’s answer for this pane', () => {
    setSessionGeneration('4242:1700000000')
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-seg-cwd').textContent).toBe('/Users/wake/Workspace/wake/purdex')
  })
})

// Host ownership H2d-4 (§0.21): a pane on a host hidden in this workbench renders a placeholder and opens no
// connection — the status bar asks nothing about it either, as for a terminated pane.
describe('StatusBar peer info for a pane on a hidden host', () => {
  beforeEach(() => {
    setupStores()
    useUploadStore.setState({ sessions: {} })
  })

  it('hidden → usePeerInfo gets no host: no peer / cwd refresh', () => {
    // The host was never asked: a shown pane would fetch its peers and this session's cwd right away.
    useShownHostsStore.setState({ ids: [] })
    render(<StatusBar activeTab={sessionTab()} />)
    expect(peerRefresh).not.toHaveBeenCalled()
    expect(cwdRefresh).not.toHaveBeenCalled()
  })

  it('hidden with a cached row → no peer row renders', () => {
    seedPeers()
    useShownHostsStore.setState({ ids: [] })
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.queryByText(/purdex-b0/)).toBeNull()
  })

  it('shown → as today: the host is asked, and a cached row renders', () => {
    render(<StatusBar activeTab={sessionTab()} />)
    expect(peerRefresh).toHaveBeenCalledWith(HOST_ID)
    expect(cwdRefresh).toHaveBeenCalledWith(HOST_ID, 'dev001')
    cleanup()
    seedPeers()
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-seg-peer-id').textContent).toBe('purdex-b0 [q34psn]')
  })

  it('hiding the host while the bar is mounted drops the peer row live', () => {
    seedPeers()
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-seg-peer-id').textContent).toBe('purdex-b0 [q34psn]')
    act(() => { useShownHostsStore.setState({ ids: [] }) })
    expect(screen.queryByText(/purdex-b0/)).toBeNull()
  })
})
