import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react'
import { SessionSplit, panelDocks, CHAT_MIN_W } from './SessionSplit'

afterEach(cleanup)
const panel = <aside data-testid="the-panel">panel</aside>
const r = (width: number | undefined, over: Partial<React.ComponentProps<typeof SessionSplit>> = {}) => {
  const onClose = vi.fn()
  render(<SessionSplit panel={panel} open onClose={onClose} widthOverride={width} {...over}><div data-testid="chat">chat</div></SessionSplit>)
  return onClose
}

describe('panelDocks', () => {
  it('docks iff the chat keeps CHAT_MIN_W beside the panel (panel 320 up to a 762 container, then 42 %)', () => {
    expect(CHAT_MIN_W).toBe(360)
    expect(panelDocks(679)).toBe(false) // 359 left for the chat
    expect(panelDocks(680)).toBe(true) // 360 + 320
    expect(panelDocks(359)).toBe(false)
    expect(panelDocks(1000)).toBe(true) // 420 panel, 580 chat
    expect(panelDocks(null)).toBe(true) // not measured yet
    expect(panelDocks(0)).toBe(true)
  })
})

describe('SessionSplit', () => {
  it('wide container: the panel sits beside the chat, no overlay', () => {
    r(700)
    expect(screen.getByTestId('session-split').dataset.mode).toBe('docked')
    expect(screen.getByTestId('the-panel')).toBeTruthy()
    expect(screen.queryByTestId('split-overlay')).toBeNull()
    expect(screen.getByTestId('chat')).toBeTruthy()
  })

  it('narrow container: the panel is an overlay with a scrim, chat still mounted', () => {
    r(500)
    expect(screen.getByTestId('session-split').dataset.mode).toBe('overlay')
    expect(screen.getByTestId('split-overlay').contains(screen.getByTestId('the-panel'))).toBe(true)
    expect(screen.getByTestId('split-scrim')).toBeTruthy()
    expect(screen.getByTestId('chat')).toBeTruthy()
  })

  it('the threshold: 679 overlays, 680 docks (360 chat + 320 panel)', () => {
    r(679)
    expect(screen.getByTestId('session-split').dataset.mode).toBe('overlay')
    cleanup()
    r(680)
    expect(screen.getByTestId('session-split').dataset.mode).toBe('docked')
  })

  it('closed: only the chat, in either width', () => {
    r(500, { open: false })
    expect(screen.queryByTestId('the-panel')).toBeNull()
    expect(screen.getByTestId('session-split').dataset.mode).toBe('closed')
  })

  it('overlay closes by scrim click and by Esc, but not by Esc inside a text field', () => {
    const onClose = r(500)
    fireEvent.click(screen.getByTestId('split-scrim'))
    expect(onClose).toHaveBeenCalledTimes(1)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(2)
    const ta = document.createElement('textarea')
    document.body.appendChild(ta)
    fireEvent.keyDown(ta, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(2)
    ta.remove()
  })

  it('docked: the scrim does not exist and Esc is not ours (the panel itself handles it)', () => {
    const onClose = r(900)
    expect(screen.queryByTestId('split-scrim')).toBeNull()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).not.toHaveBeenCalled()
  })

  it('follows the observed width when not overridden', () => {
    let cb: ResizeObserverCallback = () => {}
    vi.stubGlobal('ResizeObserver', class { constructor(c: ResizeObserverCallback) { cb = c } observe() {} disconnect() {} unobserve() {} })
    r(undefined)
    expect(screen.getByTestId('session-split').dataset.mode).toBe('docked') // unmeasured
    act(() => cb([{ contentRect: { width: 500 } } as ResizeObserverEntry], {} as ResizeObserver))
    expect(screen.getByTestId('session-split').dataset.mode).toBe('overlay')
    act(() => cb([{ contentRect: { width: 900 } } as ResizeObserverEntry], {} as ResizeObserver))
    expect(screen.getByTestId('session-split').dataset.mode).toBe('docked')
    vi.unstubAllGlobals()
  })
})
