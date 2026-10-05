// spa/src/hooks/useTerminalWs.reveal.test.ts — reveal() focus gating (shell cleanup spec §8.2).
//
// reveal() runs on the first data after a connect (and again after a wsUrl change). It is a first-mount activation
// path, so it focuses the terminal only if, at reveal time, the tab is active AND the pane is its focus target.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createElement, type RefObject } from 'react'
import { render, act } from '@testing-library/react'
import type { Terminal } from '@xterm/xterm'
import type { FitAddon } from '@xterm/addon-fit'
import { useTerminalWs } from './useTerminalWs'

const captured = vi.hoisted(() => ({ onData: undefined as ((data: ArrayBuffer) => void) | undefined }))

vi.mock('../lib/ws', () => ({
  connectTerminal: vi.fn((_url: string, onData: (data: ArrayBuffer) => void) => {
    captured.onData = onData
    return { send: vi.fn(), resize: vi.fn(), close: vi.fn() }
  }),
}))

interface PaneRefs {
  term: RefObject<Terminal | null>
  fit: RefObject<FitAddon | null>
  container: RefObject<HTMLDivElement | null>
  focus: ReturnType<typeof vi.fn>
}

function makeRefs(): PaneRefs {
  const focus = vi.fn()
  const term = {
    cols: 80,
    rows: 24,
    write: vi.fn(),
    focus,
    onData: vi.fn(() => ({ dispose: vi.fn() })),
    onResize: vi.fn(() => ({ dispose: vi.fn() })),
  } as unknown as Terminal
  return {
    term: { current: term },
    fit: { current: { fit: vi.fn() } as unknown as FitAddon },
    container: { current: document.createElement('div') },
    focus,
  }
}

interface HarnessProps {
  wsUrl: string
  refs: PaneRefs
  active: boolean
  isFocusTarget: boolean
  onReady: () => void
}

function Harness({ wsUrl, refs, active, isFocusTarget, onReady }: HarnessProps) {
  useTerminalWs({
    wsUrl,
    termRef: refs.term,
    fitAddonRef: refs.fit,
    containerRef: refs.container,
    active,
    isFocusTarget,
    onReady,
    onDisconnect: () => {},
    onReconnect: () => {},
  })
  return createElement('div')
}

const URL_A = 'ws://1.2.3.4:7860/ws/terminal/a'
const URL_B = 'ws://1.2.3.4:7860/ws/terminal/b'

/** First data after a connect, then the reveal delay elapses. */
function firstData() {
  act(() => { captured.onData?.(new ArrayBuffer(1)) })
  act(() => { vi.runOnlyPendingTimers() })
}

beforeEach(() => {
  vi.useFakeTimers()
  captured.onData = undefined
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useTerminalWs reveal() focus', () => {
  it('first mount as the active tab: reveal focuses the focus target', () => {
    const refs = makeRefs()
    const onReady = vi.fn()
    render(createElement(Harness, { wsUrl: URL_A, refs, active: true, isFocusTarget: true, onReady }))
    firstData()
    expect(onReady).toHaveBeenCalledTimes(1)
    expect(refs.focus).toHaveBeenCalledTimes(1)
  })

  it('reveal does not focus a pane that is not the focus target', () => {
    const refs = makeRefs()
    const onReady = vi.fn()
    render(createElement(Harness, { wsUrl: URL_A, refs, active: true, isFocusTarget: false, onReady }))
    firstData()
    expect(onReady).toHaveBeenCalledTimes(1)
    expect(refs.focus).not.toHaveBeenCalled()
  })

  it('reveal does not focus a pane whose tab is not active, even as the target', () => {
    const refs = makeRefs()
    const onReady = vi.fn()
    render(createElement(Harness, { wsUrl: URL_A, refs, active: false, isFocusTarget: true, onReady }))
    firstData()
    expect(onReady).toHaveBeenCalledTimes(1)
    expect(refs.focus).not.toHaveBeenCalled()
  })

  it('reads active and isFocusTarget at reveal time, not at connect time', () => {
    const refs = makeRefs()
    const onReady = vi.fn()
    const view = render(createElement(Harness, { wsUrl: URL_A, refs, active: false, isFocusTarget: false, onReady }))
    act(() => { captured.onData?.(new ArrayBuffer(1)) })
    view.rerender(createElement(Harness, { wsUrl: URL_A, refs, active: true, isFocusTarget: true, onReady }))
    act(() => { vi.runOnlyPendingTimers() })
    expect(refs.focus).toHaveBeenCalledTimes(1)
  })

  it('a wsUrl change reveals again, under the same gate', () => {
    const refs = makeRefs()
    const onReady = vi.fn()
    const view = render(createElement(Harness, { wsUrl: URL_A, refs, active: true, isFocusTarget: true, onReady }))
    firstData()
    expect(refs.focus).toHaveBeenCalledTimes(1)

    view.rerender(createElement(Harness, { wsUrl: URL_B, refs, active: true, isFocusTarget: true, onReady }))
    firstData()
    expect(refs.focus).toHaveBeenCalledTimes(2)

    view.rerender(createElement(Harness, { wsUrl: URL_A, refs, active: true, isFocusTarget: false, onReady }))
    firstData()
    expect(onReady).toHaveBeenCalledTimes(3)
    expect(refs.focus).toHaveBeenCalledTimes(2)
  })
})
