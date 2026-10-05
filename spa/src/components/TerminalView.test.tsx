import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, fireEvent } from '@testing-library/react'
import TerminalView from './TerminalView'
import { useAgentStore } from '../stores/useAgentStore'
import { useHostStore } from '../stores/useHostStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { compositeKey } from '../lib/composite-key'

const { mockClose, TerminalSpy, capturedCallbacks } = vi.hoisted(() => {
  const mockClose = vi.fn()
  const capturedCallbacks: {
    onData?: (data: ArrayBuffer) => void
    onClose?: () => void
    onOpen?: () => void
  } = {}
  const TerminalSpy = vi.fn(function (this: Record<string, unknown>, opts: Record<string, unknown>) {
    this._opts = opts
    return {
      loadAddon: vi.fn(),
      open: vi.fn(),
      write: vi.fn(),
      onData: vi.fn(() => ({ dispose: vi.fn() })),
      onResize: vi.fn(() => ({ dispose: vi.fn() })),
      onTitleChange: vi.fn(() => ({ dispose: vi.fn() })),
      registerLinkProvider: vi.fn(() => ({ dispose: vi.fn() })),
      buffer: { active: { getLine: () => ({ translateToString: () => '' }) } },
      dispose: vi.fn(),
      focus: vi.fn(),
      unicode: { activeVersion: '6' },
      cols: 80,
      rows: 24,
      _opts: opts,
    }
  })
  return { mockClose, TerminalSpy, capturedCallbacks }
})

// xterm.js requires DOM APIs not available in jsdom, so we test mounting only
vi.mock('@xterm/xterm', () => ({
  Terminal: TerminalSpy,
}))

vi.mock('@xterm/addon-fit', () => ({
  FitAddon: vi.fn(function () {
    return {
      fit: vi.fn(),
      dispose: vi.fn(),
    }
  }),
}))

vi.mock('@xterm/addon-webgl', () => ({
  WebglAddon: vi.fn(function () {
    return { dispose: vi.fn(), onContextLoss: vi.fn() }
  }),
}))

vi.mock('@xterm/addon-unicode11', () => ({
  Unicode11Addon: vi.fn(function () {
    return { dispose: vi.fn() }
  }),
}))

vi.mock('../lib/ws', () => ({
  connectTerminal: vi.fn((_url: string, onData: () => void, onClose: () => void, onOpen: () => void) => {
    capturedCallbacks.onData = onData
    capturedCallbacks.onClose = onClose
    capturedCallbacks.onOpen = onOpen
    return {
      send: vi.fn(),
      resize: vi.fn(),
      close: mockClose,
    }
  }),
}))

describe('TerminalView', () => {
  it('renders container div', () => {
    const { container } = render(
      <TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />
    )
    expect(container.querySelector('div')).toBeInTheDocument()
  })

  it('cleans up on unmount', () => {
    mockClose.mockClear()
    const { unmount } = render(
      <TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />
    )
    unmount()
    expect(mockClose).toHaveBeenCalled()
  })

  it('creates Terminal with macOptionClickForcesSelection enabled', () => {
    TerminalSpy.mockClear()
    render(<TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />)
    expect(TerminalSpy).toHaveBeenCalled()
    const opts = TerminalSpy.mock.calls[0][0]
    expect(opts.macOptionClickForcesSelection).toBe(true)
  })

  it('creates Terminal with rightClickSelectsWord enabled', () => {
    TerminalSpy.mockClear()
    render(<TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />)
    const opts = TerminalSpy.mock.calls[0][0]
    expect(opts.rightClickSelectsWord).toBe(true)
  })

  it('suppresses overlay when visible changes from false to true', () => {
    const { container, rerender } = render(
      <TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" visible={false} />
    )
    // Rerender with visible=true — effect forces ready=true (terminal was alive the whole time)
    rerender(<TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" visible={true} />)
    const overlay = container.querySelector('[data-testid="terminal-overlay"]')
    expect(overlay).toBeInTheDocument()
    expect(overlay?.getAttribute('style')).toContain('opacity: 0')
  })

  it('shows reconnecting overlay on disconnect', () => {
    const { container } = render(
      <TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />
    )
    // Simulate open then disconnect
    act(() => capturedCallbacks.onOpen?.())
    act(() => capturedCallbacks.onClose?.())

    const overlay = container.querySelector('[data-testid="terminal-overlay"]')
    expect(overlay).toBeInTheDocument()
    expect(overlay?.getAttribute('style')).toContain('opacity: 1')
    // 50% transparent background via color-mix
    expect(overlay?.getAttribute('style')).toContain('50%')
    expect(overlay?.textContent).toContain('reconnecting...')
  })

  it('hides reconnecting overlay on reconnect', async () => {
    const { container } = render(
      <TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />
    )
    // Simulate initial connect + first data → wait for reveal (300ms + margin)
    act(() => capturedCallbacks.onOpen?.())
    act(() => capturedCallbacks.onData?.(new ArrayBuffer(1)))
    await act(() => new Promise((r) => setTimeout(r, 400)))

    // Disconnect
    act(() => capturedCallbacks.onClose?.())
    let overlay = container.querySelector('[data-testid="terminal-overlay"]')
    expect(overlay?.getAttribute('style')).toContain('opacity: 1')

    // Reconnect — revealed=true so onOpen sets ready=true
    act(() => capturedCallbacks.onOpen?.())
    overlay = container.querySelector('[data-testid="terminal-overlay"]')
    expect(overlay?.getAttribute('style')).toContain('opacity: 0')
  })

  it('does not recreate terminal when revealDelay changes', async () => {
    const { useUISettingsStore } = await import('../stores/useUISettingsStore')
    mockClose.mockClear()
    TerminalSpy.mockClear()
    render(<TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />)
    expect(TerminalSpy).toHaveBeenCalledTimes(1)

    // Change revealDelay — should NOT trigger terminal rebuild
    act(() => useUISettingsStore.getState().setTerminalRevealDelay(500))
    expect(mockClose).not.toHaveBeenCalled()
    expect(TerminalSpy).toHaveBeenCalledTimes(1)
  })

  it('WebGL context loss disposes addon and re-fits terminal', async () => {
    const { WebglAddon } = await import('@xterm/addon-webgl')
    const { FitAddon } = await import('@xterm/addon-fit')
    const { useUISettingsStore } = await import('../stores/useUISettingsStore')

    useUISettingsStore.setState({ terminalRenderer: 'webgl' })
    vi.mocked(WebglAddon).mockClear()
    vi.mocked(FitAddon).mockClear()

    render(<TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />)

    // Verify onContextLoss callback was registered
    const webgl = vi.mocked(WebglAddon).mock.results[0]?.value
    expect(webgl.onContextLoss).toHaveBeenCalledTimes(1)
    const onLoss = webgl.onContextLoss.mock.calls[0][0] as () => void

    // Get FitAddon instance and clear prior fit() calls from mount
    const fit = vi.mocked(FitAddon).mock.results[0]?.value
    fit.fit.mockClear()

    // Trigger context loss
    act(() => { onLoss() })

    // dispose should be called synchronously
    expect(webgl.dispose).toHaveBeenCalled()

    // fit() is called in next rAF — flush it
    await act(async () => { await new Promise((r) => setTimeout(r, 20)) })
    expect(fit.fit).toHaveBeenCalled()
  })

  // Shell cleanup spec §8.2: programmatic focus only at activation, and only as the tab's focus target.
  describe('activation focus', () => {
    const URL = 'ws://localhost:7860/ws/terminal/test'
    type TermMock = { focus: ReturnType<typeof vi.fn> }
    const lastTerm = () => TerminalSpy.mock.results.at(-1)!.value as TermMock
    const flushRaf = () => act(async () => { await new Promise((r) => setTimeout(r, 20)) })

    beforeEach(() => {
      TerminalSpy.mockClear()
      // an earlier test changes the reveal delay; reveal() here must land inside the 400ms wait
      useUISettingsStore.setState({ terminalRevealDelay: 0 })
    })

    it('first mount as the active tab and the target → focuses', async () => {
      render(<TerminalView wsUrl={URL} visible={true} isFocusTarget={true} />)
      await flushRaf()
      expect(lastTerm().focus).toHaveBeenCalledTimes(1)
    })

    it('first mount as the active tab but not the target → no focus', async () => {
      render(<TerminalView wsUrl={URL} visible={true} isFocusTarget={false} />)
      await flushRaf()
      expect(lastTerm().focus).not.toHaveBeenCalled()
    })

    it('keep-alive reactivation as the target → refits and focuses', async () => {
      const { FitAddon } = await import('@xterm/addon-fit')
      vi.mocked(FitAddon).mockClear()
      const { rerender } = render(<TerminalView wsUrl={URL} visible={false} isFocusTarget={true} />)
      await flushRaf()
      const fit = vi.mocked(FitAddon).mock.results[0]!.value as { fit: ReturnType<typeof vi.fn> }
      fit.fit.mockClear()
      expect(lastTerm().focus).not.toHaveBeenCalled()

      rerender(<TerminalView wsUrl={URL} visible={true} isFocusTarget={true} />)
      await flushRaf()
      expect(fit.fit).toHaveBeenCalled()
      expect(lastTerm().focus).toHaveBeenCalledTimes(1)
    })

    it('keep-alive reactivation while not the target → refits, no focus', async () => {
      const { FitAddon } = await import('@xterm/addon-fit')
      vi.mocked(FitAddon).mockClear()
      const { rerender } = render(<TerminalView wsUrl={URL} visible={false} isFocusTarget={false} />)
      await flushRaf()
      const fit = vi.mocked(FitAddon).mock.results[0]!.value as { fit: ReturnType<typeof vi.fn> }
      fit.fit.mockClear()

      rerender(<TerminalView wsUrl={URL} visible={true} isFocusTarget={false} />)
      await flushRaf()
      expect(fit.fit).toHaveBeenCalled()
      expect(lastTerm().focus).not.toHaveBeenCalled()
    })

    it('click inside a visible tab: isFocusTarget flipping while visible never focuses', async () => {
      const { rerender } = render(<TerminalView wsUrl={URL} visible={true} isFocusTarget={false} />)
      await flushRaf()
      rerender(<TerminalView wsUrl={URL} visible={true} isFocusTarget={true} />)
      await flushRaf()
      rerender(<TerminalView wsUrl={URL} visible={true} isFocusTarget={false} />)
      await flushRaf()
      rerender(<TerminalView wsUrl={URL} visible={true} isFocusTarget={true} />)
      await flushRaf()
      expect(lastTerm().focus).not.toHaveBeenCalled()
    })

    it('reveal() on first data focuses only the active target', async () => {
      const visibleTarget = render(<TerminalView wsUrl={URL} visible={true} isFocusTarget={true} />)
      await flushRaf()
      const target = lastTerm()
      target.focus.mockClear()
      act(() => capturedCallbacks.onData?.(new ArrayBuffer(1)))
      await act(() => new Promise((r) => setTimeout(r, 20)))
      expect(target.focus).toHaveBeenCalledTimes(1)
      visibleTarget.unmount()

      render(<TerminalView wsUrl={URL} visible={true} isFocusTarget={false} />)
      const other = lastTerm()
      act(() => capturedCallbacks.onData?.(new ArrayBuffer(1)))
      await act(() => new Promise((r) => setTimeout(r, 20)))
      expect(other.focus).not.toHaveBeenCalled()
    })
  })

  it('registers terminal-link provider on mount', () => {
    TerminalSpy.mockClear()
    render(<TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />)
    const termInstance = TerminalSpy.mock.results[0]!.value as { registerLinkProvider: ReturnType<typeof vi.fn> }
    expect(termInstance.registerLinkProvider).toHaveBeenCalledTimes(1)
  })

  describe('linkContext (PR-5)', () => {
    it('passes workspaceId through linkContext', async () => {
      const mod = await import('../hooks/useTerminal')
      const spy = vi.spyOn(mod, 'useTerminal')
      try {
        render(
          <TerminalView
            wsUrl="ws://localhost:7860/ws/terminal/test"
            hostId="h1"
            sessionCode="s1"
            workspaceId="wsA"
          />,
        )
        const opts = spy.mock.calls[0]?.[0]
        expect(opts?.linkContext).toEqual({ hostId: 'h1', sessionCode: 's1', workspaceId: 'wsA' })
      } finally {
        spy.mockRestore()
      }
    })

    it('omits workspaceId when prop not supplied', async () => {
      const mod = await import('../hooks/useTerminal')
      const spy = vi.spyOn(mod, 'useTerminal')
      try {
        render(
          <TerminalView
            wsUrl="ws://localhost:7860/ws/terminal/test"
            hostId="h1"
            sessionCode="s1"
          />,
        )
        const opts = spy.mock.calls[0]?.[0]
        expect(opts?.linkContext).toEqual({ hostId: 'h1', sessionCode: 's1', workspaceId: undefined })
      } finally {
        spy.mockRestore()
      }
    })
  })

  describe('drag-drop', () => {
    const HOST = 'test-host'
    const SESSION = 'dev001'
    const CK = compositeKey(HOST, SESSION)

    function setAgentActive(active: boolean) {
      useAgentStore.setState({
        statuses: active ? { [CK]: 'idle' } : {},
      })
    }

    beforeEach(() => {
      // Ensure host store has a daemon base
      useHostStore.getState().reset()
    })

    afterEach(() => {
      useAgentStore.setState({ statuses: {}, lastEvents: {}, unread: {}, subagents: {} })
    })

    it('shows drop overlay on drag-enter when agent is active', () => {
      setAgentActive(true)
      const { container } = render(
        <TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" hostId={HOST} sessionCode={SESSION} />,
      )

      const root = container.firstElementChild!
      fireEvent.dragEnter(root, { dataTransfer: { types: ['Files'] } })

      expect(container.querySelector('[data-testid="drop-overlay"]')).toBeInTheDocument()
    })

    it('does NOT show drop overlay when agent is not active', () => {
      setAgentActive(false)
      const { container } = render(
        <TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" hostId={HOST} sessionCode={SESSION} />,
      )

      const root = container.firstElementChild!
      fireEvent.dragEnter(root, { dataTransfer: { types: ['Files'] } })

      expect(container.querySelector('[data-testid="drop-overlay"]')).not.toBeInTheDocument()
    })

    it('hides drop overlay on drag-leave', () => {
      setAgentActive(true)
      const { container } = render(
        <TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" hostId={HOST} sessionCode={SESSION} />,
      )

      const root = container.firstElementChild!
      fireEvent.dragEnter(root, { dataTransfer: { types: ['Files'] } })
      expect(container.querySelector('[data-testid="drop-overlay"]')).toBeInTheDocument()

      fireEvent.dragLeave(root, { dataTransfer: { types: ['Files'] } })
      expect(container.querySelector('[data-testid="drop-overlay"]')).not.toBeInTheDocument()
    })
  })
})
