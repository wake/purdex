// #2665: the units the ⌘F search looks through — what the deck and the chat draw as text, in the order they draw it, under the id of the element
// that holds it (the DOM side is in components/deck/FindBar.test.tsx).
import { describe, it, expect } from 'vitest'
import { chatUnits, deckUnits, fullTurnKey, stepUnitId, userUnitId, agentUnitId } from './find-units'
import { proseText } from '../nex/markdown-text'
import { imageMarker } from './image-placeholder'
import type { ConversationItem, Turn } from './types'

const user = (id: string, text: string, source = 'user'): ConversationItem => ({ type: 'user', id, at: 1, index: 0, text, source })
const agent = (id: string, markdown: string): ConversationItem => ({ type: 'agent_text', id, at: 1, index: 0, markdown })
const step = (id: string, over: Record<string, unknown> = {}): ConversationItem => ({
  type: 'step', id, at: 1, index: 0, kind: 'read', tool: 'Read', status: 'done', summary: `sum-${id}`, started_at: 1, input: null, ...over,
}) as ConversationItem
const turn = (i: number, items: ConversationItem[]): Turn => ({ id: `t${i}`, index: i, started_at: i, outcome: 'done', items })

describe('deckUnits', () => {
  it('the person\'s words, the agent\'s prose (as drawn, not the markdown source) and each step\'s line, in the order drawn', () => {
    const units = deckUnits([turn(0, [user('u1', 'hello'), agent('a1', 'see **bold** and [a link](http://x.test)'), step('s1')])], 20)
    expect(units.map((u) => u.id)).toEqual([userUnitId('u1'), agentUnitId('a1'), stepUnitId('s1')])
    expect(units[1].text).toBe(proseText('see **bold** and [a link](http://x.test)'))
    expect(units[1].text).not.toContain('http://x.test')
    expect(units[2].text).toBe('sum-s1')
  })

  it('what the steps draw: a command with its `$`, a diff by its path, a subagent by its description', () => {
    const units = deckUnits([turn(0, [
      step('e', { kind: 'execute', command: { text: 'pnpm test' } }),
      step('d', { kind: 'edit', diff: { path: 'src/a.ts', added: 1, removed: 0, exact: true } }),
      step('k', { kind: 'task', subagent: { agent_id: 'ag', description: 'look around' } }),
    ])], 20)
    expect(units.map((u) => u.text)).toEqual(['$ pnpm test', 'src/a.ts', 'look around'])
  })

  it('a bash input is drawn with its `!`; a command\'s output (folded) and a question step have no line of their own', () => {
    const units = deckUnits([turn(0, [user('b', 'ls', 'bash'), user('o', 'file.txt', 'command_output'), step('q', { kind: 'other', question: { questions: [] } })])], 20)
    expect(units.map((u) => u.text)).toEqual(['! ls'])
  })

  it('a sent image is a thumbnail (no text); a path that is not an image stays the marker it was', () => {
    const text = `look ${imageMarker('/tmp/a.png')} and ${imageMarker('/tmp/notes.txt')}`
    const [u] = deckUnits([turn(0, [user('u', text)])], 20)
    expect(u.text).toBe(`look  and ${imageMarker('/tmp/notes.txt')}`)
    // a peer's text is drawn verbatim
    const [p] = deckUnits([turn(0, [user('p', text, 'peer')])], 20)
    expect(p.text).toBe(text)
  })

  it('the prose of a turn far from the end is drawn as plain text until a search asks for it: its unit names the key; the newest turns need none', () => {
    const turns = Array.from({ length: 4 }, (_, i) => turn(i, [agent(`a${i}`, `word ${i}`)]))
    const units = deckUnits(turns, 2)
    expect(units.map((u) => u.reveal)).toEqual([[fullTurnKey('t0')], [fullTurnKey('t1')], [], []])
  })

  it('the text is NFC (the query is)', () => {
    const [u] = deckUnits([turn(0, [user('u', 'café')])], 20)
    expect(u.text).toBe('café')
  })

  it('an empty text is no unit; an item this build does not know is skipped', () => {
    expect(deckUnits([turn(0, [user('u', ''), { type: 'future', id: 'z', at: 1, index: 0 } as ConversationItem])], 20)).toEqual([])
  })
})

describe('chatUnits', () => {
  it('the bubbles: the person\'s messages and the agent\'s replies (a chain of work is a row that opens the panel, a peer message a line)', () => {
    const units = chatUnits([turn(0, [user('u1', 'run it'), step('s1'), agent('a1', 'done **now**'), user('p1', 'from a peer', 'peer')])])
    expect(units.map((u) => u.id)).toEqual([userUnitId('u1'), agentUnitId('a1')])
    expect(units[1].text).toBe('done now')
  })
})
