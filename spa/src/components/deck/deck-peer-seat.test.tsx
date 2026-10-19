// spa/src/components/deck/deck-peer-seat.test.tsx — the places of the deck that say who a message came from show the person (name pool v2 spec §9.3):
// the peer block's caption, the chat's peer line, a peer's message waiting in Claude Code's queue. The sender string is the address, so `seatOf`
// resolves it; the full address stays the tooltip. A sender that is no persona reads as it always did.
import { describe, it, expect, beforeEach } from 'vitest'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import type { ReactNode } from 'react'
import { UserBlock } from './UserBlock'
import { PeerLine } from './ChatPeerLine'
import { PendingTail } from './PendingTail'
import { FoldContext, TurnIndexContext, useFoldMemory } from '../room/fold-context'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { makeCollection, makePersona } from '../../lib/names/__fixtures__/personas'
import type { HeaderQueued, UserItem } from '../../lib/conversations/types'

// the peer block and the chat line keep their fold state in the pane's fold memory
function Pane({ children }: { children: ReactNode }) {
  const store = useFoldMemory()
  return <FoldContext.Provider value={store}><TurnIndexContext.Provider value={0}>{children}</TurnIndexContext.Provider></FoldContext.Provider>
}
const inPane = (node: ReactNode) => render(<Pane>{node}</Pane>)

const ADDRESS = 'mlab/purdex-1f-kenji'
const peerItem = (over: Partial<UserItem> = {}): UserItem => ({ type: 'user', id: 'u1', at: Date.UTC(2026, 9, 19, 4, 12), index: 0, text: 'ship it', source: 'peer', from: { kind: 'peer', name: ADDRESS }, ...over })

function seedKenji() {
  const c = makeCollection(['kenji'])
  c.items[0] = makePersona('kenji', { name: 'Kenji', avatar_data: 'data:image/png;base64,AA==' })
  usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: c } } })
}

beforeEach(() => { cleanup(); useI18nStore.getState().setLocale('zh-TW'); usePersonaStore.setState({ byHost: {} }) })

describe('the peer block caption', () => {
  it('names the person with a face, and keeps the address as the tooltip', () => {
    seedKenji()
    inPane(<UserBlock item={peerItem()} />)
    const cap = screen.getByTestId('deck-user-caption')
    expect(cap).toHaveTextContent(/^來自 Kenji · \d\d:\d\d$/)
    expect(within(cap).getByTestId('seat-inline')).toHaveAttribute('title', ADDRESS)
    expect(cap.querySelector('img')).not.toBeNull()
  })

  it('a sender that is no persona keeps the address in the caption', () => {
    seedKenji()
    inPane(<UserBlock item={peerItem({ from: { kind: 'peer', name: 'mlab/purdex-1f-ghost' } })} />)
    expect(screen.getByTestId('deck-user-caption')).toHaveTextContent('來自 mlab/purdex-1f-ghost')
    expect(screen.queryByTestId('seat-inline')).toBeNull()
  })

  it('a mod notice (from kind plugin) is no person even when its name looks like one', () => {
    seedKenji()
    inPane(<UserBlock item={peerItem({ from: { kind: 'plugin', name: 'kenji' } })} />)
    expect(screen.queryByTestId('seat-inline')).toBeNull()
  })
})

describe('the chat peer line', () => {
  it('one message: the head line and the opened item both name the person', () => {
    seedKenji()
    inPane(<PeerLine items={[peerItem()]} />)
    const head = screen.getByTestId('chat-peer-head')
    expect(head).toHaveTextContent('↪ Kenji：ship it')
    expect(within(head).getByTestId('seat-inline')).toHaveAttribute('title', ADDRESS)
    fireEvent.click(head)
    expect(within(screen.getByTestId('chat-peer-item')).getByTestId('seat-inline')).toHaveAttribute('title', ADDRESS)
  })

  it('a sender that is no persona keeps today\'s line', () => {
    inPane(<PeerLine items={[peerItem()]} />)
    expect(screen.getByTestId('chat-peer-head')).toHaveTextContent(`↪ ${ADDRESS}：ship it`)
  })

  it('an unverified sender still says so after the name', () => {
    seedKenji()
    inPane(<PeerLine items={[peerItem({ from: { kind: 'peer', name: ADDRESS, unverified: true } })]} />)
    expect(screen.getByTestId('chat-peer-head')).toHaveTextContent('↪ Kenji（未驗證）：ship it')
  })
})

describe('a peer\'s message waiting in Claude Code\'s queue', () => {
  const queued = (from: string): HeaderQueued => ({ text: 'wait for me', at: 6, source: 'peer', from })
  const tail = (from: string) => render(<PendingTail paneKey="p-pend" hostId="h" sessionId="s" entries={[]} queued={[queued(from)]} />)

  it('names the person', () => {
    seedKenji()
    tail(ADDRESS)
    const row = screen.getByTestId('header-queued')
    expect(row).toHaveTextContent('來自 Kenji · 排隊中')
    expect(within(row).getByTestId('seat-inline')).toHaveAttribute('title', ADDRESS)
  })

  it('a sender that is no persona keeps the address', () => {
    seedKenji()
    tail('mlab/purdex-1f-ghost')
    expect(screen.getByTestId('header-queued')).toHaveTextContent('來自 mlab/purdex-1f-ghost · 排隊中')
  })
})
