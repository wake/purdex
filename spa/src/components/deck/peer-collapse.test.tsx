// A peer message folds to its first three visual lines; click / Enter / Space toggles; the state lives in the pane's fold memory (deck rules 2026-10-11).
// jsdom lays nothing out, so scrollHeight is stubbed: 200 is more than three lines, 40 fits.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
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

beforeEach(() => {
  H.px = 200
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(() => H.px)
})
afterEach(() => { cleanup(); vi.restoreAllMocks(); window.getSelection()?.removeAllRanges() })

describe('peer block in the deck', () => {
  it('more than three lines: folded to three, a button, aria-expanded=false, brightens on hover', () => {
    mount()
    expect(block()).toHaveAttribute('role', 'button')
    expect(block()).toHaveAttribute('aria-expanded', 'false')
    expect(block()).toHaveClass('group', 'hover:border-peer')
    expect(block().querySelector('.line-clamp-3')).not.toBeNull()
    expect(block().querySelector('.group-hover\\:text-text-primary')).not.toBeNull()
  })

  it('a click opens it, a second click folds it again', () => {
    mount()
    fireEvent.click(block())
    expect(block()).toHaveAttribute('aria-expanded', 'true')
    expect(block().querySelector('.line-clamp-3')).toBeNull()
    fireEvent.click(block())
    expect(block()).toHaveAttribute('aria-expanded', 'false')
    expect(block().querySelector('.line-clamp-3')).not.toBeNull()
  })

  it('three lines or fewer: not a button, no hover hint, a click does nothing', () => {
    H.px = 40
    mount()
    expect(block()).not.toHaveAttribute('role')
    expect(block()).not.toHaveClass('hover:border-peer')
    expect(block().querySelector('.line-clamp-3')).toBeNull()
    fireEvent.click(block())
    expect(block()).not.toHaveAttribute('aria-expanded')
  })

  it('Enter and Space toggle it', () => {
    mount()
    fireEvent.keyDown(block(), { key: 'Enter' })
    expect(block()).toHaveAttribute('aria-expanded', 'true')
    fireEvent.keyDown(block(), { key: ' ' })
    expect(block()).toHaveAttribute('aria-expanded', 'false')
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
    expect(block()).toHaveAttribute('aria-expanded', 'false')
  })

  it('stays open after the block is unmounted and mounted again (fold memory, not local state)', () => {
    mount()
    fireEvent.click(block())
    fireEvent.click(screen.getByTestId('swap'))
    expect(screen.queryByTestId('deck-user')).toBeNull()
    fireEvent.click(screen.getByTestId('swap'))
    expect(block()).toHaveAttribute('aria-expanded', 'true')
  })

  it('fold-context expand([key]) opens it (a search hit reveals it)', () => {
    mount(peer('p9'))
    act(() => storeRef.expand(['p9:peerfold']))
    expect(block()).toHaveAttribute('aria-expanded', 'true')
  })

  it('other tones are untouched: a message from the person is never a button', () => {
    mount(peer('u1', { source: 'user', from: undefined }))
    expect(block()).not.toHaveAttribute('role')
  })
})

describe('peer messages in the chat', () => {
  it('an opened long message is folded to three lines, opens on click, and stays open across a remount', () => {
    mount(peer('c1'), true)
    fireEvent.click(screen.getByTestId('chat-peer-head'))
    const item = screen.getByTestId('chat-peer-item')
    expect(item).toHaveAttribute('role', 'button')
    expect(item.querySelector('.line-clamp-3')).not.toBeNull()
    fireEvent.click(item)
    expect(screen.getByTestId('chat-peer-item')).toHaveAttribute('aria-expanded', 'true')
    fireEvent.click(screen.getByTestId('swap'))
    fireEvent.click(screen.getByTestId('swap'))
    expect(screen.getByTestId('chat-peer-item')).toHaveAttribute('aria-expanded', 'true')
  })

  it('a short opened message is not a button', () => {
    H.px = 40
    mount(peer('c2'), true)
    fireEvent.click(screen.getByTestId('chat-peer-head'))
    expect(screen.getByTestId('chat-peer-item')).not.toHaveAttribute('role')
  })
})
