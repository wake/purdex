import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
import { RenamePopover } from './RenamePopover'
import { emptyPeerHostEntry, usePeerStore, type PeerHostEntry, type PeerRow } from '../stores/usePeerStore'
import { useSessionCwdStore } from '../stores/useSessionCwdStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { copyText } from '../lib/copy-text'
import type { PaneContent, Tab } from '../types/tab'

vi.mock('../lib/copy-text', () => ({ copyText: vi.fn(async () => {}) }))
const copyTextMock = vi.mocked(copyText)

describe('RenamePopover', () => {
  const defaultProps = {
    anchorRect: { left: 100, top: 30, width: 120, height: 26, bottom: 56, right: 220 } as DOMRect,
    currentName: 'my-session',
    onConfirm: vi.fn().mockResolvedValue(undefined),
    onCancel: vi.fn(),
  }

  beforeEach(() => { cleanup(); vi.clearAllMocks() })
  afterEach(() => cleanup())

  it('renders input with current name', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    expect(input).toBeInTheDocument()
  })

  it('selects all text on mount', () => {
    const selectSpy = vi.spyOn(HTMLInputElement.prototype, 'select')
    render(<RenamePopover {...defaultProps} />)
    expect(selectSpy).toHaveBeenCalled()
    selectSpy.mockRestore()
  })

  it('calls onConfirm with new name on Enter', async () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.change(input, { target: { value: 'new-name' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(defaultProps.onConfirm).toHaveBeenCalledWith('new-name')
  })

  it('calls onCancel on Escape', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(defaultProps.onCancel).toHaveBeenCalled()
  })

  it('does not call onConfirm with empty name', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.change(input, { target: { value: '' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(defaultProps.onConfirm).not.toHaveBeenCalled()
  })

  it('does not call onConfirm when name unchanged', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(defaultProps.onConfirm).not.toHaveBeenCalled()
  })

  it('shows error message when provided', () => {
    render(<RenamePopover {...defaultProps} error="Rename failed" />)
    expect(screen.getByText('Rename failed')).toBeInTheDocument()
  })

  describe('client-side name format validation', () => {
    it('shows format error for names with invalid characters', () => {
      render(<RenamePopover {...defaultProps} />)
      const input = screen.getByDisplayValue('my-session')
      fireEvent.change(input, { target: { value: 'has space' } })
      expect(screen.getByText(/only letters|僅允許/i)).toBeInTheDocument()
    })

    it.each([
      ['dot', 'my.session'],
      ['colon', 'tmux:session'],
      ['slash', 'path/name'],
    ])('shows format error for %s in name', (_label, value) => {
      render(<RenamePopover {...defaultProps} />)
      const input = screen.getByDisplayValue('my-session')
      fireEvent.change(input, { target: { value } })
      expect(screen.getByText(/only letters|僅允許/i)).toBeInTheDocument()
    })

    it('does not call onConfirm when name has invalid format', () => {
      render(<RenamePopover {...defaultProps} />)
      const input = screen.getByDisplayValue('my-session')
      fireEvent.change(input, { target: { value: 'bad name!' } })
      fireEvent.keyDown(input, { key: 'Enter' })
      expect(defaultProps.onConfirm).not.toHaveBeenCalled()
    })

    it('clears format error when corrected to valid name', () => {
      render(<RenamePopover {...defaultProps} />)
      const input = screen.getByDisplayValue('my-session')
      fireEvent.change(input, { target: { value: 'bad.name' } })
      expect(screen.getByText(/only letters|僅允許/i)).toBeInTheDocument()
      fireEvent.change(input, { target: { value: 'good-name' } })
      expect(screen.queryByText(/only letters|僅允許/i)).not.toBeInTheDocument()
    })

    it('does not show format error for valid names', () => {
      render(<RenamePopover {...defaultProps} />)
      const input = screen.getByDisplayValue('my-session')
      fireEvent.change(input, { target: { value: 'valid_Name-01' } })
      expect(screen.queryByText(/only letters|僅允許/i)).not.toBeInTheDocument()
    })

    it('does not show format error when input is empty', () => {
      render(<RenamePopover {...defaultProps} />)
      const input = screen.getByDisplayValue('my-session')
      fireEvent.change(input, { target: { value: '' } })
      expect(screen.queryByText(/only letters|僅允許/i)).not.toBeInTheDocument()
    })

    it('format error overrides API error prop', () => {
      render(<RenamePopover {...defaultProps} error="API error message" />)
      const input = screen.getByDisplayValue('my-session')
      fireEvent.change(input, { target: { value: 'bad.name' } })
      expect(screen.getByText(/only letters|僅允許/i)).toBeInTheDocument()
      expect(screen.queryByText('API error message')).not.toBeInTheDocument()
    })

    it('does not show format error when name matches currentName (legacy session)', () => {
      const props = { ...defaultProps, currentName: 'legacy.session' }
      render(<RenamePopover {...props} />)
      expect(screen.queryByText(/only letters|僅允許/i)).not.toBeInTheDocument()
    })
  })

  describe('vertical viewport clamping', () => {
    let offsetHeightDescriptor: PropertyDescriptor | undefined
    let innerHeightDescriptor: PropertyDescriptor | undefined

    beforeEach(() => {
      offsetHeightDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
      innerHeightDescriptor = Object.getOwnPropertyDescriptor(window, 'innerHeight')
    })

    afterEach(() => {
      if (offsetHeightDescriptor) {
        Object.defineProperty(HTMLElement.prototype, 'offsetHeight', offsetHeightDescriptor)
      }
      if (innerHeightDescriptor) {
        Object.defineProperty(window, 'innerHeight', innerHeightDescriptor)
      }
    })

    it('positions popover below anchor when space is sufficient', () => {
      Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 40 })
      Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
      const anchor = { left: 100, top: 30, width: 120, height: 26, bottom: 56, right: 220 } as DOMRect
      const { container } = render(<RenamePopover {...defaultProps} anchorRect={anchor} />)
      const el = container.firstElementChild as HTMLElement
      expect(el.style.top).toBe(`${anchor.bottom + 4}px`)
    })

    it('flips popover above anchor when below would overflow', () => {
      Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 40 })
      Object.defineProperty(window, 'innerHeight', { configurable: true, value: 200 })
      const anchor = { left: 100, top: 170, width: 120, height: 20, bottom: 190, right: 220 } as DOMRect
      const { container } = render(<RenamePopover {...defaultProps} anchorRect={anchor} />)
      const el = container.firstElementChild as HTMLElement
      // anchorRect.top - PADDING - popoverHeight = 170 - 4 - 40 = 126
      expect(el.style.top).toBe('126px')
    })

    it('clamps to PADDING when both above and below overflow', () => {
      Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 40 })
      Object.defineProperty(window, 'innerHeight', { configurable: true, value: 50 })
      const anchor = { left: 100, top: 10, width: 120, height: 20, bottom: 30, right: 220 } as DOMRect
      const { container } = render(<RenamePopover {...defaultProps} anchorRect={anchor} />)
      const el = container.firstElementChild as HTMLElement
      // below: 30 + 4 = 34, 34 + 40 = 74 > 50 - 4 = 46 → flip
      // above: 10 - 4 - 40 = -34 < 4 → clamp to PADDING
      expect(el.style.top).toBe('4px')
    })

    it('recalculates position when error changes popover height', () => {
      Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 40 })
      Object.defineProperty(window, 'innerHeight', { configurable: true, value: 200 })
      // Anchor near bottom but popover (height=40) fits below: 160 + 4 + 40 = 204 > 196 → actually flips
      // Use anchor where height=40 fits below but height=70 does not
      const anchor = { left: 100, top: 130, width: 120, height: 20, bottom: 150, right: 220 } as DOMRect
      // below: 150 + 4 = 154, 154 + 40 = 194 < 200 - 4 = 196 → fits below
      const { container, rerender } = render(<RenamePopover {...defaultProps} anchorRect={anchor} />)
      const el = container.firstElementChild as HTMLElement
      expect(el.style.top).toBe(`${anchor.bottom + 4}px`)

      // Now error appears, popover grows taller
      Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 70 })
      rerender(<RenamePopover {...defaultProps} anchorRect={anchor} error="Name taken" />)
      // below: 150 + 4 = 154, 154 + 70 = 224 > 196 → flip above
      // above: 130 - 4 - 70 = 56
      expect(el.style.top).toBe('56px')
    })
  })
})

// The peer section inside each pane block (peer-info-panel spec §5, §6).
describe('RenamePopover peer section', () => {
  const H1 = 'h1'
  const H2 = 'h2'

  const peerRefresh = vi.fn(async (_hostId: string) => {})
  const cwdRefresh = vi.fn(async (_hostId: string, _code: string) => {})

  const ROW: PeerRow = {
    address: 'mini-lab/ai-chat4:ai-chat4-ai-chat-story-3a',
    label: 'ai-chat4',
    labelSource: 'default',
    deliverable: true,
    reason: '',
    agent: { type: 'cc', peerName: 'ai-chat-story-3a', status: 'idle' },
  }

  const popoverProps = {
    anchorRect: { left: 0, top: 0, width: 100, height: 20, bottom: 20, right: 100 } as DOMRect,
    currentName: 'dev',
    onConfirm: vi.fn(async () => {}),
    onCancel: vi.fn(),
  }

  function terminalPane(over: Partial<Extract<PaneContent, { kind: 'tmux-session' }>> = {}): PaneContent {
    return {
      kind: 'tmux-session',
      hostId: H1,
      sessionCode: 'abc123',
      mode: 'terminal',
      cachedName: 'dev',
      tmuxInstance: '111:1000',
      ...over,
    }
  }

  function tabOf(...contents: PaneContent[]): Tab {
    const panes = contents.map((content, i) => ({ id: `p${i + 1}`, content }))
    return {
      id: 't1',
      pinned: false,
      locked: false,
      createdAt: 0,
      layout: panes.length === 1
        ? { type: 'leaf' as const, pane: panes[0] }
        : {
            type: 'split' as const,
            id: 's1',
            direction: 'h' as const,
            children: panes.map((pane) => ({ type: 'leaf' as const, pane })),
            sizes: panes.map(() => 100 / panes.length),
          },
    }
  }

  /** A host that answers, with one row for `code`. */
  function seedHost(hostId: string, code: string, row: PeerRow | null, entry: Partial<PeerHostEntry> = {}) {
    usePeerStore.setState({
      byHost: {
        ...usePeerStore.getState().byHost,
        [hostId]: { ...emptyPeerHostEntry(), rows: row ? { [code]: row } : {}, fetchedAt: Date.now(), ...entry },
      },
    })
  }

  /** The pane is reconciled: the session exists in the session store. */
  function seedSession(hostId: string, code: string) {
    useSessionStore.setState({
      sessions: {
        ...useSessionStore.getState().sessions,
        [hostId]: [{ code, name: 'dev', cwd: '/start/dir', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false }],
      },
    })
  }

  beforeEach(() => {
    cleanup()
    vi.clearAllMocks()
    copyTextMock.mockResolvedValue(undefined)
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    useHostStore.setState({
      hosts: {
        [H1]: { id: H1, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 },
        [H2]: { id: H2, name: 'air', ip: '100.64.0.4', port: 7860, order: 1 },
      },
      hostOrder: [H1, H2],
      runtime: { [H1]: { status: 'connected' }, [H2]: { status: 'connected' } },
    })
    usePeerStore.setState({ byHost: {}, refresh: peerRefresh })
    useSessionCwdStore.setState({ byHost: {}, refresh: cwdRefresh })
  })

  it('renders address, agent and deliverability for a live pane', () => {
    seedSession(H1, 'abc123')
    seedHost(H1, 'abc123', ROW)
    render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
    expect(screen.getByTestId('peer-address-p1').textContent).toContain('mini-lab/ai-chat4:ai-chat4-ai-chat-story-3a')
    const agent = screen.getByTestId('peer-agent-p1').textContent ?? ''
    expect(agent).toContain('cc')
    expect(agent).toContain('ai-chat-story-3a')
    expect(agent).toContain('idle')
    expect(screen.getByTestId('peer-deliverable-p1').textContent).toBe('yes')
  })

  it('copies the full address from the address row', async () => {
    seedSession(H1, 'abc123')
    seedHost(H1, 'abc123', ROW)
    render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
    fireEvent.click(screen.getByTestId('peer-address-p1'))
    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith('mini-lab/ai-chat4:ai-chat4-ai-chat-story-3a'))
  })

  it('marks a default label, and leaves a user label unmarked', () => {
    seedSession(H1, 'abc123')
    seedHost(H1, 'abc123', ROW)
    const { unmount } = render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
    expect(screen.getByTestId('peer-default-marker-p1')).toBeInTheDocument()
    unmount()

    seedHost(H1, 'abc123', { ...ROW, labelSource: 'user' })
    render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
    expect(screen.queryByTestId('peer-default-marker-p1')).toBeNull()
  })

  it.each([
    ['no_agent', 'no agent in this session'],
    ['not_cc', 'not a Claude Code session'],
    ['inbox_dead', "the agent's inbox is not responding"],
    ['proxy', 'reached through a proxy'],
    ['ambiguous', 'several agents share this session'],
  ])('shows the reason %s rather than a bare "no" when a row is not deliverable', (reason, text) => {
    seedSession(H1, 'abc123')
    seedHost(H1, 'abc123', { ...ROW, deliverable: false, reason })
    render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
    const cell = screen.getByTestId('peer-deliverable-p1')
    expect(cell.textContent).toBe(text)
    expect(cell.textContent).not.toBe('no')
  })

  it('omits the peer section for a terminated pane', () => {
    seedSession(H1, 'abc123')
    seedHost(H1, 'abc123', ROW)
    render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane({ terminated: 'tmux-restarted' }))} />)
    expect(screen.getByTestId('rename-pane-block-p1')).toBeInTheDocument()
    expect(screen.queryByTestId('peer-section-p1')).toBeNull()
  })

  it('keeps loading and errors per block: one host failing does not blank the other', () => {
    seedSession(H1, 'abc123')
    seedSession(H2, 'def456')
    seedHost(H1, 'abc123', null, { error: 'connection refused' })
    seedHost(H2, 'def456', ROW)
    render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane(), terminalPane({ hostId: H2, sessionCode: 'def456' }))} />)
    expect(screen.getByTestId('peer-error-p1').textContent).toContain('connection refused')
    expect(screen.queryByTestId('peer-address-p1')).toBeNull()
    expect(screen.getByTestId('peer-address-p2').textContent).toContain('mini-lab/ai-chat4')
    expect(screen.queryByTestId('peer-error-p2')).toBeNull()
  })

  it('refreshes once per distinct host when the panel opens', () => {
    seedSession(H1, 'abc123')
    seedSession(H2, 'def456')
    // Both hosts already answered, so anything counted here is the open
    // refresh, not the store's own first-render fetch.
    seedHost(H1, 'abc123', ROW)
    seedHost(H2, 'def456', ROW)
    render(<RenamePopover {...popoverProps} tab={tabOf(
      terminalPane(),
      terminalPane({ sessionCode: 'xyz789' }),
      terminalPane({ hostId: H2, sessionCode: 'def456' }),
    )} />)
    expect(peerRefresh).toHaveBeenCalledTimes(2)
    expect(peerRefresh).toHaveBeenCalledWith(H1)
    expect(peerRefresh).toHaveBeenCalledWith(H2)
  })

  it('refreshes the cwd of every live pane when the panel opens', () => {
    seedSession(H1, 'abc123')
    seedHost(H1, 'abc123', ROW)
    render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane(), terminalPane({ sessionCode: 'xyz789' }))} />)
    expect(cwdRefresh).toHaveBeenCalledWith(H1, 'abc123')
    expect(cwdRefresh).toHaveBeenCalledWith(H1, 'xyz789')
  })

  it('never fetches for a host that is not connected', () => {
    seedSession(H1, 'abc123')
    useHostStore.setState({ runtime: { [H1]: { status: 'disconnected' } } })
    render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
    expect(peerRefresh).not.toHaveBeenCalled()
    expect(cwdRefresh).not.toHaveBeenCalled()
    expect(screen.getByTestId('peer-status-p1').textContent).toBe('host not connected')
  })

  it('shows the last known row dimmed while the host is not connected', () => {
    seedSession(H1, 'abc123')
    seedHost(H1, 'abc123', ROW)
    useHostStore.setState({ runtime: { [H1]: { status: 'disconnected' } } })
    render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
    expect(screen.getByTestId('peer-address-p1').textContent).toContain('mini-lab/ai-chat4')
    expect(screen.getByTestId('peer-section-p1')).toHaveAttribute('data-dim', 'true')
  })

  it('does not re-refresh on a re-render with unchanged targets, but does when the host set changes', () => {
    seedSession(H1, 'abc123')
    seedHost(H1, 'abc123', ROW)
    seedHost(H2, 'def456', ROW)
    const { rerender } = render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
    expect(peerRefresh).toHaveBeenCalledTimes(1)
    rerender(<RenamePopover {...popoverProps} currentName="dev" tab={tabOf(terminalPane())} />)
    expect(peerRefresh).toHaveBeenCalledTimes(1)
    rerender(<RenamePopover {...popoverProps} tab={tabOf(terminalPane(), terminalPane({ hostId: H2, sessionCode: 'def456' }))} />)
    expect(peerRefresh).toHaveBeenCalledTimes(3)
    expect(peerRefresh).toHaveBeenNthCalledWith(2, H1)
    expect(peerRefresh).toHaveBeenNthCalledWith(3, H2)
  })

  it('does not close the popover when the refresh control inside it is clicked', () => {
    seedSession(H1, 'abc123')
    seedHost(H1, 'abc123', ROW)
    render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
    const button = screen.getByTestId('peer-refresh-p1')
    peerRefresh.mockClear()
    fireEvent.mouseDown(button)
    fireEvent.click(button)
    expect(popoverProps.onCancel).not.toHaveBeenCalled()
    expect(peerRefresh).toHaveBeenCalledWith(H1)
  })

  describe('spec §6 states', () => {
    it('shows the row plus a note naming the cause when the envelope is partial', () => {
      seedSession(H1, 'abc123')
      seedHost(H1, 'abc123', ROW, { envelope: { partial: true, labelsUnavailable: false, unknownRegistryFiles: [] } })
      render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
      expect(screen.getByTestId('peer-address-p1').textContent).toContain('mini-lab/ai-chat4')
      expect(screen.getByTestId('peer-partial-p1').textContent).toContain('unresolved owners')
    })

    it('names the unreadable registry files when they are the cause', () => {
      seedSession(H1, 'abc123')
      seedHost(H1, 'abc123', ROW, { envelope: { partial: true, labelsUnavailable: false, unknownRegistryFiles: ['/a/b.json'] } })
      render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
      expect(screen.getByTestId('peer-partial-p1').textContent).toContain('unreadable registry files')
    })

    it('says "could not be determined", not "no peer", when the envelope is partial and there is no row', () => {
      seedSession(H1, 'abc123')
      seedHost(H1, 'abc123', null, { envelope: { partial: true, labelsUnavailable: false, unknownRegistryFiles: [] } })
      render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
      const note = screen.getByTestId('peer-status-p1').textContent
      expect(note).toBe('could not be determined')
      expect(note).not.toContain('no peer')
    })

    it('says "no peer" when a complete envelope has no row', () => {
      seedSession(H1, 'abc123')
      seedHost(H1, 'abc123', null)
      render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
      expect(screen.getByTestId('peer-status-p1').textContent).toBe('no peer')
    })

    it('notes that labels could not be read, so addresses may be hash defaults', () => {
      seedSession(H1, 'abc123')
      seedHost(H1, 'abc123', ROW, { envelope: { partial: true, labelsUnavailable: true, unknownRegistryFiles: [] } })
      render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
      expect(screen.getByTestId('peer-labels-p1').textContent).toContain('hash defaults')
    })

    it('shows the agent row but no address when the row has no label (no cc agent)', () => {
      seedSession(H1, 'abc123')
      seedHost(H1, 'abc123', { ...ROW, label: '', address: '', labelSource: '' })
      render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
      expect(screen.queryByTestId('peer-address-p1')).toBeNull()
      expect(screen.getByTestId('peer-agent-p1').textContent).toContain('ai-chat-story-3a')
    })

    it('shows a spinner and no error while the pane is not yet reconciled with the session store', () => {
      seedHost(H1, 'abc123', null)
      render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane())} />)
      expect(screen.getByTestId('peer-spinner-p1')).toBeInTheDocument()
      expect(screen.queryByTestId('peer-error-p1')).toBeNull()
      expect(screen.queryByTestId('peer-status-p1')).toBeNull()
    })

    it('renders a peer section for the terminal panes of a tab whose primary pane is not a tmux session', () => {
      seedSession(H1, 'abc123')
      seedHost(H1, 'abc123', ROW)
      const tab = tabOf({ kind: 'editor', source: { type: 'local' }, filePath: '/a.md' }, terminalPane())
      render(<RenamePopover {...popoverProps} tab={tab} />)
      expect(screen.getByTestId('peer-address-p2').textContent).toContain('mini-lab/ai-chat4')
    })

    it('renders no block at all for a stream pane — the collector never offers one', () => {
      seedSession(H1, 'abc123')
      seedHost(H1, 'abc123', ROW)
      render(<RenamePopover {...popoverProps} tab={tabOf(terminalPane({ mode: 'stream' }))} />)
      expect(screen.queryByTestId('rename-pane-block-p1')).toBeNull()
      expect(screen.queryByTestId('peer-section-p1')).toBeNull()
    })
  })
})
