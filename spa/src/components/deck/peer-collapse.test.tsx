// A peer message folds to its first three visual lines; click / Enter / Space toggles; the state lives in the pane's fold memory (deck rules 2026-10-11).
// jsdom lays nothing out, so scrollHeight is stubbed: 200 is more than three lines, 40 fits.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { useEffect, useState } from 'react'
import { UserBlock } from './UserBlock'
import { PeerLine } from './ChatPeerLine'
import { FoldContext, TurnIndexContext, useFoldMemory, type FoldStore } from '../room/fold-context'
import type { UserItem } from '../../lib/conversations/types'

const H = { px: 200 }
const peer = (id = 'p1', over: Partial<UserItem> = {}): UserItem => ({ type: 'user', id, at: 1, index: 0, text: 'a long\nmessage', source: 'peer', from: { kind: 'peer', name: 'host/a' }, ...over })

let storeRef: FoldStore
function Pane({ children }: { children: React.ReactNode }) {
  const store = useFoldMemory()
  useEffect(() => { storeRef = store })
  return <FoldContext.Provider value={store}><TurnIndexContext.Provider value={0}>{children}</TurnIndexContext.Provider></FoldContext.Provider>
}
// Re-mounts the block (as a tab switch does) while the pane's fold memory stays.
function Harness({ item, chat = false }: { item: UserItem; chat?: boolean }) {
  const [shown, setShown] = useState(true)
  return (
    <Pane>
      <button data-testid="swap" onClick={() => setShown((v) => !v)} />
      {shown && (chat ? <PeerLine items={[item]} /> : <UserBlock item={item} />)}
    </Pane>
  )
}
const mount = (item = peer(), chat = false) => render(<Harness item={item} chat={chat} />)
const block = () => screen.getByTestId('deck-user')
const toggleBtn = (el: HTMLElement = block()) => within(el).getByRole('button')
const open = (el: HTMLElement) => toggleBtn(el).getAttribute('aria-expanded')

beforeEach(() => {
  H.px = 200
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(() => H.px)
})
afterEach(() => { cleanup(); vi.restoreAllMocks(); window.getSelection()?.removeAllRanges() })

describe('peer block in the deck', () => {
  it('more than three lines: folded to three, the frame is not a button, a real toggle button controls the body, brightens on hover', () => {
    mount()
    expect(block()).not.toHaveAttribute('role')
    expect(block()).not.toHaveAttribute('tabindex')
    expect(toggleBtn()).toHaveAttribute('type', 'button')
    expect(open(block())).toBe('false')
    expect(block()).toHaveClass('group', 'hover:border-peer')
    expect(block().querySelector('.line-clamp-3')).not.toBeNull()
    expect(block().querySelector('.group-hover\\:text-text-primary')).not.toBeNull()
  })

  it('the toggle has aria-controls pointing at the body and a name from i18n (not the message text)', () => {
    mount()
    const body = block().querySelector('.deck-t-up')!
    expect(body.id).not.toBe('')
    expect(toggleBtn()).toHaveAttribute('aria-controls', body.id)
    expect(toggleBtn().getAttribute('aria-label')).toBeTruthy()
    expect(toggleBtn().textContent).not.toContain('message')
    // the body keeps plain text semantics
    expect(body).not.toHaveAttribute('role')
  })

  it('a click on the block opens it, a second click folds it again', () => {
    mount()
    fireEvent.click(block())
    expect(open(block())).toBe('true')
    expect(block().querySelector('.line-clamp-3')).toBeNull()
    fireEvent.click(block())
    expect(open(block())).toBe('false')
    expect(block().querySelector('.line-clamp-3')).not.toBeNull()
  })

  it('three lines or fewer: no toggle, no hover hint, a click does nothing', () => {
    H.px = 40
    mount()
    expect(within(block()).queryByRole('button')).toBeNull()
    expect(block()).not.toHaveClass('hover:border-peer')
    expect(block().querySelector('.line-clamp-3')).toBeNull()
    fireEvent.click(block())
    expect(block().querySelector('[aria-expanded]')).toBeNull()
  })

  it('the toggle is focusable (in the tab order), and activating it toggles exactly once', () => {
    mount()
    const btn = toggleBtn()
    expect(btn.tabIndex).toBeGreaterThanOrEqual(0)
    btn.focus()
    expect(document.activeElement).toBe(btn)
    // a native <button> turns Enter / Space into this click
    fireEvent.click(btn)
    expect(open(block())).toBe('true')
    fireEvent.click(toggleBtn())
    expect(open(block())).toBe('false')
  })

  it('the frame shows a focus ring when the toggle has focus', () => {
    mount()
    expect(block().className).toContain('focus-within:ring')
  })

  it('a click on a link inside the message does not toggle', () => {
    mount()
    const a = document.createElement('a')
    a.href = '#x'
    a.textContent = 'link'
    block().querySelector('.deck-t-up')!.appendChild(a)
    fireEvent.click(a)
    expect(open(block())).toBe('false')
  })

  it('a click that ends a text selection inside the block does not toggle', () => {
    mount()
    const text = block().querySelector('.deck-t-up')!
    const range = document.createRange()
    range.selectNodeContents(text)
    window.getSelection()!.removeAllRanges()
    window.getSelection()!.addRange(range)
    vi.spyOn(window.getSelection()!, 'toString').mockReturnValue('long')
    fireEvent.click(block())
    expect(open(block())).toBe('false')
  })

  it('stays open after the block is unmounted and mounted again (fold memory, not local state)', () => {
    mount()
    fireEvent.click(block())
    fireEvent.click(screen.getByTestId('swap'))
    expect(screen.queryByTestId('deck-user')).toBeNull()
    fireEvent.click(screen.getByTestId('swap'))
    expect(open(block())).toBe('true')
  })

  it('fold-context expand([key]) opens it (a search hit reveals it)', () => {
    mount(peer('p9'))
    act(() => storeRef.expand(['p9:peerfold']))
    expect(open(block())).toBe('true')
  })

  it('other tones are untouched: a message from the person has no toggle', () => {
    mount(peer('u1', { source: 'user', from: undefined }))
    expect(within(block()).queryByRole('button')).toBeNull()
  })
})

describe('peer messages in the chat', () => {
  it('an opened long message is folded to three lines, opens on click, and stays open across a remount', () => {
    mount(peer('c1'), true)
    fireEvent.click(screen.getByTestId('chat-peer-head'))
    const item = screen.getByTestId('chat-peer-item')
    expect(item).not.toHaveAttribute('role')
    expect(item.querySelector('.line-clamp-3')).not.toBeNull()
    const body = item.querySelector('[id]')!
    expect(toggleBtn(item)).toHaveAttribute('aria-controls', body.id)
    fireEvent.click(item)
    expect(open(screen.getByTestId('chat-peer-item'))).toBe('true')
    fireEvent.click(screen.getByTestId('swap'))
    fireEvent.click(screen.getByTestId('swap'))
    expect(open(screen.getByTestId('chat-peer-item'))).toBe('true')
  })

  it('a short opened message has no toggle', () => {
    H.px = 40
    mount(peer('c2'), true)
    fireEvent.click(screen.getByTestId('chat-peer-head'))
    expect(within(screen.getByTestId('chat-peer-item')).queryByRole('button')).toBeNull()
  })
})

describe('re-measuring when fonts settle', () => {
  let listeners: Record<string, Array<() => void>>
  let resolveReady: () => void
  beforeEach(() => {
    listeners = {}
    const ready = new Promise<void>((r) => { resolveReady = r })
    Object.defineProperty(document, 'fonts', {
      configurable: true,
      value: {
        ready,
        addEventListener: (n: string, f: () => void) => { (listeners[n] ??= []).push(f) },
        removeEventListener: (n: string, f: () => void) => { listeners[n] = (listeners[n] ?? []).filter((x) => x !== f) },
      },
    })
  })
  afterEach(() => { Reflect.deleteProperty(document, 'fonts') })

  it('a message that wrapped to 4 lines in the fallback font stops being foldable once the real font makes it 3', () => {
    H.px = 200
    mount()
    expect(within(block()).queryByRole('button')).not.toBeNull()
    H.px = 40
    act(() => { listeners.loadingdone.forEach((f) => f()) })
    expect(within(block()).queryByRole('button')).toBeNull()
    expect(block()).not.toHaveClass('hover:border-peer')
    expect(block().querySelector('.group-hover\\:text-text-primary')).toBeNull()
  })

  it('also re-measures when document.fonts.ready resolves', async () => {
    mount()
    H.px = 40
    await act(async () => { resolveReady(); await Promise.resolve() })
    expect(within(block()).queryByRole('button')).toBeNull()
  })

  it('after unmount the listener is removed and a late event is harmless', async () => {
    mount()
    cleanup()
    expect(listeners.loadingdone ?? []).toHaveLength(0)
    await act(async () => { resolveReady(); await Promise.resolve() })
  })
})
