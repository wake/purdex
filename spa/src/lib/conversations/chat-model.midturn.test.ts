// c9d9c7-59 pt 2: the chat marks which user messages came in during a turn (not the one that began it); a turn that left its first items out
// cannot name the one that began it, so every user message in view counts as mid-turn.
import { describe, it, expect } from 'vitest'
import { buildChat } from './chat-model'
import type { PanelTurn } from './panel-resolve'
import type { ConversationItem } from './types'

const user = (id: string, index: number, source = 'user'): ConversationItem => ({ type: 'user', id, at: 1, index, text: id, source })
const agent = (id: string, index: number): ConversationItem => ({ type: 'agent_text', id, at: 1, index, markdown: id })
const userEntries = (turns: PanelTurn[]) => buildChat(turns).flatMap((e) => (e.kind === 'user' ? [[e.item.id, e.midTurn] as const] : []))

describe('buildChat midTurn', () => {
  it('the first user message of a turn is not mid-turn; a later one is', () => {
    const t: PanelTurn = { id: 't0', index: 0, items: [user('a', 0), agent('r', 1), user('b', 2, 'queued')] }
    expect(userEntries([t])).toEqual([['a', false], ['b', true]])
  })
  it('each turn has its own first message', () => {
    const turns: PanelTurn[] = [
      { id: 't0', index: 0, items: [user('a', 0), agent('r', 1)] },
      { id: 't1', index: 1, items: [user('b', 0, 'queued'), agent('s', 1)] },
    ]
    expect(userEntries(turns)).toEqual([['a', false], ['b', false]])
  })
  it('a turn with omitted leading items: every user message in view is mid-turn', () => {
    const t: PanelTurn = { id: 't0', index: 0, omitted_items: 12, items: [user('q', 13, 'queued')] }
    expect(userEntries([t])).toEqual([['q', true]])
  })
})
