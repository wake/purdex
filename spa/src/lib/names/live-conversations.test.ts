import { describe, it, expect, beforeEach } from 'vitest'
import { usePeerStore, emptyPeerHostEntry, type PeerRow } from '../../stores/usePeerStore'
import { liveConversationCount } from './live-conversations'

const row = (ref: string): PeerRow => ({
  address: `h/x-${ref}`, ref, title: '', titleSource: '', deliverable: true, reason: '', tmuxInstance: '', agent: null,
})

describe('liveConversationCount', () => {
  beforeEach(() => usePeerStore.setState({ byHost: {} }))

  it('counts the rows that carry a conversation (a ref), not plain tmux sessions', () => {
    usePeerStore.setState({ byHost: { h1: { ...emptyPeerHostEntry(), fetchedAt: 5, rows: { a: row('_aaaaaa'), b: row('_bbbbbb'), c: row('') } } } })
    expect(liveConversationCount('h1')).toBe(2)
  })

  it('is null when the inventory has never arrived or failed, so the caller writes no number', () => {
    expect(liveConversationCount('nobody')).toBeNull()
    usePeerStore.setState({ byHost: { h1: { ...emptyPeerHostEntry(), fetchedAt: 0, rows: { a: row('_aaaaaa') } } } })
    expect(liveConversationCount('h1')).toBeNull()
    usePeerStore.setState({ byHost: { h1: { ...emptyPeerHostEntry(), fetchedAt: 5, error: 'down', rows: { a: row('_aaaaaa') } } } })
    expect(liveConversationCount('h1')).toBeNull()
  })

  it('is null for a partial inventory, whose empty list proves nothing', () => {
    usePeerStore.setState({
      byHost: { h1: { ...emptyPeerHostEntry(), fetchedAt: 5, rows: {}, envelope: { partial: true, titlesUnavailable: false, unknownRegistryFiles: [] } } },
    })
    expect(liveConversationCount('h1')).toBeNull()
  })
})
