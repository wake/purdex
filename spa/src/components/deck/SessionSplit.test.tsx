import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react'
import { SessionSplit } from './SessionSplit'
import { panelDocks, CHAT_MIN_W } from './split-layout'

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
    // no valid measurement: never docks (the chat must keep 360)
    for (const bad of [null, 0, -5, NaN, Infinity]) expect(panelDocks(bad)).toBe(false)
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

  it('escActive false (a pane that is not the focused one): Esc does not close its overlay, the scrim still does', () => {
    const onClose = r(500, { escActive: false })
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('split-scrim'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('an overlay of a pane that is not the focused one is shown but not modal: no focus taken, chat not inert; it becomes modal when the pane gets focus and lets go when it loses it', () => {
    const outside = document.createElement('button')
    document.body.appendChild(outside)
    outside.focus()
    const ui = (escActive: boolean) => <SessionSplit panel={<aside data-testid="the-panel"><button>in panel</button></aside>} open onClose={() => {}} widthOverride={500} escActive={escActive}><div data-testid="chat">chat</div></SessionSplit>
    const { rerender } = render(ui(false))
    expect(screen.getByTestId('split-overlay')).toBeTruthy() // still drawn, with its scrim
    expect(document.activeElement).toBe(outside)
    expect(screen.getByTestId('split-chat').hasAttribute('inert')).toBe(false)
    expect(screen.getByRole('dialog', { hidden: true }).getAttribute('aria-modal')).toBeNull()
    rerender(ui(true))
    expect(screen.getByTestId('split-overlay').contains(document.activeElement)).toBe(true)
    expect(screen.getByTestId('split-chat').hasAttribute('inert')).toBe(true)
    // focus moves to another pane, then this pane stops being the focused one: it must not take focus back
    outside.focus()
    rerender(ui(false))
    expect(document.activeElement).toBe(outside)
    expect(screen.getByTestId('split-chat').hasAttribute('inert')).toBe(false)
    outside.remove()
  })

  it('docked: the scrim does not exist and Esc is not ours (the panel itself handles it)', () => {
    const onClose = r(900)
    expect(screen.queryByTestId('split-scrim')).toBeNull()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).not.toHaveBeenCalled()
  })

  it('follows the observed width when not overridden; no valid width means no panel at all', () => {
    let cb: ResizeObserverCallback = () => {}
    vi.stubGlobal('ResizeObserver', class { constructor(c: ResizeObserverCallback) { cb = c } observe() {} disconnect() {} unobserve() {} })
    r(undefined)
    const mode = () => screen.getByTestId('session-split').dataset.mode
    expect(mode()).toBe('measuring') // jsdom measures 0: not docked, not drawn
    expect(screen.queryByTestId('the-panel')).toBeNull()
    expect(screen.getByTestId('split-chat').hasAttribute('inert')).toBe(false)
    act(() => cb([{ contentRect: { width: 500 } } as ResizeObserverEntry], {} as ResizeObserver))
    expect(mode()).toBe('overlay')
    act(() => cb([{ contentRect: { width: 900 } } as ResizeObserverEntry], {} as ResizeObserver))
    expect(mode()).toBe('docked')
    act(() => cb([{ contentRect: { width: 0 } } as ResizeObserverEntry], {} as ResizeObserver))
    expect(mode()).toBe('measuring')
    expect(screen.queryByTestId('the-panel')).toBeNull()
    act(() => cb([{ contentRect: { width: 900 } } as ResizeObserverEntry], {} as ResizeObserver))
    expect(screen.getByTestId('the-panel')).toBeTruthy()
    vi.unstubAllGlobals()
  })

  it('the first paint is already right: the width is read synchronously on mount (700 docks, 500 overlays, 679 / 680 edge)', () => {
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
    let w = 700
    const spy = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => ({ width: w, height: 300 } as DOMRect))
    for (const [width, expected] of [[700, 'docked'], [500, 'overlay'], [679, 'overlay'], [680, 'docked']] as const) {
      w = width
      r(undefined)
      expect(screen.getByTestId('session-split').dataset.mode).toBe(expected) // no act, no observer tick: the very first render result
      cleanup()
    }
    spy.mockRestore()
    vi.unstubAllGlobals()
  })
})

describe('SessionSplit overlay is modal', () => {
  const twoBtn = (
    <aside data-testid="the-panel"><button data-testid="pa">a</button><button data-testid="pb">b</button></aside>
  )
  const mount = (open: boolean, width = 500) => (
    <SessionSplit panel={twoBtn} open={open} onClose={() => {}} widthOverride={width}>
      <button data-testid="chat-btn">chat</button>
    </SessionSplit>
  )

  it('overlay: the panel is a labelled modal dialog and the chat is inert + aria-hidden', () => {
    render(mount(true))
    const dlg = screen.getByRole('dialog')
    expect(dlg.getAttribute('aria-modal')).toBe('true')
    expect(dlg.getAttribute('aria-label')).toBeTruthy()
    expect(dlg.contains(screen.getByTestId('the-panel'))).toBe(true)
    const chat = screen.getByTestId('split-chat')
    expect(chat.hasAttribute('inert')).toBe(true)
    expect(chat.getAttribute('aria-hidden')).toBe('true')
  })

  it('docked: no dialog role, nothing inert', () => {
    render(mount(true, 900))
    expect(screen.queryByRole('dialog')).toBeNull()
    const chat = screen.getByTestId('split-chat')
    expect(chat.hasAttribute('inert')).toBe(false)
    expect(chat.getAttribute('aria-hidden')).toBeNull()
  })

  it('moves focus into the panel on open and restores it to the opener on close', () => {
    const { rerender } = render(mount(false))
    screen.getByTestId('chat-btn').focus()
    rerender(mount(true))
    expect(document.activeElement).toBe(screen.getByTestId('pa'))
    rerender(mount(false))
    expect(document.activeElement).toBe(screen.getByTestId('chat-btn'))
  })

  it('Tab from the last control wraps to the first, Shift+Tab from the first wraps to the last', () => {
    render(mount(true))
    const a = screen.getByTestId('pa'), b = screen.getByTestId('pb')
    b.focus()
    expect(fireEvent.keyDown(b, { key: 'Tab' })).toBe(false) // default prevented
    expect(document.activeElement).toBe(a)
    expect(fireEvent.keyDown(a, { key: 'Tab', shiftKey: true })).toBe(false)
    expect(document.activeElement).toBe(b)
    a.focus() // in the middle the browser's own Tab order is left alone
    expect(fireEvent.keyDown(a, { key: 'Tab' })).toBe(true)
  })

  it('a panel with nothing focusable takes focus itself and keeps Tab inside', () => {
    render(<SessionSplit panel={<aside>text</aside>} open onClose={() => {}} widthOverride={500}><button>c</button></SessionSplit>)
    const dlg = screen.getByRole('dialog')
    expect(document.activeElement).toBe(dlg)
    expect(fireEvent.keyDown(dlg, { key: 'Tab' })).toBe(false)
  })
})
