import { describe, it, expect, beforeEach } from 'vitest'
import { clearAllInputHistory, isWalking, sentTexts, setHistorySource, stepHistory } from './input-history'
import { clearAllDrafts, readDraft } from './draft-memory'
import { clearAllAttachments, readAttachments } from './attachment-memory'
import type { ConversationItem, UserItem } from './types'

const K = 'p|h|s'
let n = 0
const user = (text: string, source: UserItem['source'] = 'user', over: Partial<UserItem> = {}): UserItem => ({ id: `u${++n}`, at: n, index: n, type: 'user', text, source, ...over })

beforeEach(() => { clearAllInputHistory(); clearAllDrafts(); clearAllAttachments() })

describe('sentTexts', () => {
  it('keeps what the person typed (user, delivered queued), drops peers, notices, empties and cut text', () => {
    const items: ConversationItem[] = [
      user('a'), user('from peer', 'peer'), user('<task-notification>', 'background'), user('  ', 'user'),
      user('b', 'queued'), user('cut', 'user', { truncated: true }),
      { id: 'x', at: 1, index: 1, type: 'agent_text', markdown: 'hi' } as ConversationItem,
    ]
    expect(sentTexts(items)).toEqual(['a', 'b'])
  })
  it('collapses consecutive duplicates only', () => {
    expect(sentTexts([user('a'), user('a'), user('b'), user('a')])).toEqual(['a', 'b', 'a'])
  })
})

describe('stepHistory', () => {
  beforeEach(() => setHistorySource(K, [user('one'), user('two'), user('three')]))

  it('Up from an empty box walks newest to oldest, Down walks back and ends in an empty box', () => {
    expect(stepHistory(K, 'up', '', true)?.draft).toBe('three')
    expect(readDraft(K)).toBe('three')
    expect(stepHistory(K, 'up', 'three', true)?.draft).toBe('two')
    expect(stepHistory(K, 'up', 'two', true)?.draft).toBe('one')
    expect(stepHistory(K, 'up', 'one', true)).toBeNull() // nothing older
    expect(stepHistory(K, 'down', 'one', true)?.draft).toBe('two')
    expect(stepHistory(K, 'down', 'two', true)?.draft).toBe('three')
    expect(stepHistory(K, 'down', 'three', true)?.draft).toBe('')
    expect(readDraft(K)).toBeUndefined()
    expect(isWalking(K, '')).toBe(false)
  })
  it('does not start unless allowed, nor with no history; Down does not start', () => {
    expect(stepHistory(K, 'up', '', false)).toBeNull()
    expect(stepHistory(K, 'down', '', true)).toBeNull()
    setHistorySource(K, [])
    expect(stepHistory(K, 'up', '', true)).toBeNull()
  })
  it('an edit ends the walk: the shown text is an ordinary draft', () => {
    stepHistory(K, 'up', '', true)
    expect(isWalking(K, 'three')).toBe(true)
    expect(isWalking(K, 'three!')).toBe(false)
    expect(stepHistory(K, 'up', 'three!', false)).toBeNull()
    expect(stepHistory(K, 'down', 'three!', true)).toBeNull()
  })
  it('restores a sent image as a chip with its placeholder, and drops it when the walk moves on', () => {
    setHistorySource(K, [user('plain'), user('look [Image: source: /tmp/a/shot.png] here')])
    const r = stepHistory(K, 'up', '', true)!
    const chips = readAttachments(K)
    expect(chips).toHaveLength(1)
    expect(chips[0]).toMatchObject({ name: 'shot.png', path: '/tmp/a/shot.png', image: true })
    expect(r.draft).toBe(`look ${chips[0].text} here`)
    expect(chips[0].seq).toBeDefined()
    expect(stepHistory(K, 'up', r.draft, true)?.draft).toBe('plain')
    expect(readAttachments(K)).toHaveLength(0)
  })
  it('an edited (placeholder deleted) item leaks no chips: Up from empty leaves only the current item\'s chips', () => {
    setHistorySource(K, [user('plain'), user('a [Image: source: /tmp/a.png]'), user('b [Image: source: /tmp/b.png]')])
    const first = stepHistory(K, 'up', '', true)! // b with its chip
    expect(readAttachments(K)).toHaveLength(1)
    expect(first.draft).toContain('[Image #')
    stepHistory(K, 'up', first.draft, true) // a, b's chip gone
    expect(readAttachments(K).map((c) => c.path)).toEqual(['/tmp/a.png'])
    const edited = '' // the person deletes the placeholder and the text
    stepHistory(K, 'up', edited, true) // a new walk from the newest: b again
    expect(readAttachments(K).map((c) => c.path)).toEqual(['/tmp/b.png'])
  })
  it('keeps the place by identity: older items prepended while one is shown, the next Up is its true older neighbour', () => {
    const [a, b, c] = [user('one'), user('two'), user('three')]
    setHistorySource(K, [a, b, c])
    stepHistory(K, 'up', '', true)
    stepHistory(K, 'up', 'three', true) // showing two
    setHistorySource(K, [user('zero-a'), user('zero-b'), a, b, c])
    expect(stepHistory(K, 'up', 'two', true)?.draft).toBe('one')
    expect(stepHistory(K, 'up', 'one', true)?.draft).toBe('zero-b')
  })
  it('older items removed while one is shown: Up is null at the new oldest, Down still the true newer neighbour', () => {
    const [a, b, c] = [user('one'), user('two'), user('three')]
    setHistorySource(K, [a, b, c])
    stepHistory(K, 'up', '', true)
    stepHistory(K, 'up', 'three', true) // two
    setHistorySource(K, [b, c])
    expect(stepHistory(K, 'up', 'two', true)).toBeNull()
    expect(stepHistory(K, 'down', 'two', true)?.draft).toBe('three')
  })
  it('a truncated flip of an older item does not shift the walk', () => {
    const [a, b, c] = [user('one'), user('two'), user('three')]
    setHistorySource(K, [a, b, c])
    stepHistory(K, 'up', '', true)
    stepHistory(K, 'up', 'three', true) // two
    setHistorySource(K, [{ ...a, truncated: true }, b, c])
    expect(stepHistory(K, 'up', 'two', true)).toBeNull() // the cut one left the history; two is now the oldest
  })
  it('a duplicate-collapse change does not shift the walk', () => {
    const [a, b, c] = [user('one'), user('two'), user('three')]
    setHistorySource(K, [a, b, c])
    stepHistory(K, 'up', '', true)
    stepHistory(K, 'up', 'three', true) // two
    setHistorySource(K, [a, user('one'), b, c]) // a duplicate of one appears: collapses into a
    expect(stepHistory(K, 'up', 'two', true)?.draft).toBe('one')
    expect(stepHistory(K, 'up', 'one', true)).toBeNull()
  })
  it('the shown item vanished: the walk ends and the box is left alone', () => {
    const [a, b, c] = [user('one'), user('two'), user('three')]
    setHistorySource(K, [a, b, c])
    stepHistory(K, 'up', '', true)
    stepHistory(K, 'up', 'three', true) // two
    setHistorySource(K, [a, c])
    expect(stepHistory(K, 'up', 'two', true)).toBeNull()
    expect(readDraft(K)).toBe('two')
    expect(isWalking(K, 'two')).toBe(false)
  })
  it('keeps the position when a message arrives meanwhile', () => {
    const [a, b, c] = [user('one'), user('two'), user('three')]
    setHistorySource(K, [a, b, c])
    stepHistory(K, 'up', '', true)
    stepHistory(K, 'up', 'three', true)
    setHistorySource(K, [a, b, c, user('four')])
    expect(stepHistory(K, 'up', 'two', true)?.draft).toBe('one')
  })
})
