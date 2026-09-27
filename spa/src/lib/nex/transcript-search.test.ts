// spa/src/lib/nex/transcript-search.test.ts — the search index (R3 plan T3.1).
import { describe, it, expect } from 'vitest'
import {
  buildSearchUnits, findCurrent, findMatches, matchIdentity, SEARCH_MATCH_LIMIT, searchUnitId,
  type SearchUnit, type SearchUnitOptions,
} from './transcript-search'
import { indexOperations } from './operations'
import type { ContentBlock, StreamMessage } from './message-types'
import type { ToolActivity } from './tool-activity'

const asst = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'assistant', message: { id: 'm', role: 'assistant', content: blocks, stop_reason: null } } as StreamMessage)
const usr = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'user', message: { role: 'user', content: blocks, stop_reason: null } } as StreamMessage)
const said = (text: string): StreamMessage => usr({ type: 'text', text })
const use = (id: string, name: string, input: Record<string, unknown> = {}): ContentBlock =>
  ({ type: 'tool_use', id, name, input })
const res = (id: string, content: string, isError = false): ContentBlock =>
  ({ type: 'tool_result', tool_use_id: id, content, is_error: isError })
const child = (m: StreamMessage, parent: string): StreamMessage =>
  ({ ...m, parent_tool_use_id: parent }) as StreamMessage
/** A finished Bash call whose command is its N2 primary arg (so no raw-input unit). */
const ran = (command: string, status: ToolActivity['status'] = 'done'): ToolActivity =>
  ({ name: 'Bash', startedAt: 0, endedAt: 1, status, primaryArg: { key: 'command', value: command } })

/** The matches alone (the limit is far away in these tests). */
const find = (list: SearchUnit[], query: string) => findMatches(list, query).matches

function units(messages: StreamMessage[], view: 'room' | 'chat', extra: Partial<SearchUnitOptions> = {}) {
  return buildSearchUnits({
    messages,
    index: indexOperations(messages),
    tools: extra.tools,
    view,
    keyPrefix: extra.keyPrefix ?? 'k',
    turnStarts: extra.turnStarts ?? [0],
  })
}

describe('buildSearchUnits / findMatches', () => {
  it('finds a match in folded tool output and lists the key that reveals it', () => {
    const long = Array.from({ length: 50 }, (_, n) => `line ${n}`).join('\n') + '\nneedle here'
    const messages = [said('go'), asst(use('t1', 'Bash', { command: 'ls' })), usr(res('t1', long))]
    const matches = find(units(messages, 'room'), 'needle')
    expect(matches).toHaveLength(1)
    expect(matches[0].unitId).toBe(searchUnitId('1:0', 'output'))
    expect(matches[0].reveal).toEqual(['1:0'])
    expect(long.slice(matches[0].start, matches[0].end)).toBe('needle')
  })

  it('a subagent match needs the Task\'s subagent key', () => {
    const messages = [
      said('go'),
      asst(use('task', 'Task', { description: 'explore' })),
      child(asst({ type: 'text', text: 'the subagent found a needle' }), 'task'),
      child(asst(use('c1', 'Grep', { pattern: 'x' })), 'task'),
      child(usr(res('c1', 'needle in child output')), 'task'),
      usr(res('task', 'done')),
    ]
    const matches = find(units(messages, 'room'), 'needle')
    expect(matches.map((m) => m.unitId)).toEqual([
      searchUnitId('2:0', 'text'),
      searchUnitId('3:0', 'output'),
    ])
    expect(matches[0].reveal).toEqual(['1:0:subagent'])
    expect(matches[1].reveal).toEqual(['1:0:subagent', '3:0'])
  })

  it('chat reveals through the tools line', () => {
    const tools: Record<string, ToolActivity> = {
      e1: { name: 'Edit', startedAt: 0, endedAt: 1, status: 'done',
        diff: { path: '/a/notes.md', added: 1, removed: 0, truncated: false,
          hunks: [{ oldStart: 1, oldLines: 0, newStart: 1, newLines: 1, lines: ['+needle edit'] }] } },
      f1: ran('false', 'error'),
      p1: ran('echo needle'),
    }
    const messages = [
      said('go'),
      asst(use('p1', 'Bash', { command: 'echo needle' })),
      usr(res('p1', 'needle out')),
      said('next'),
      asst(use('e1', 'Edit', { file_path: '/a/notes.md' }), use('f1', 'Bash', { command: 'false' })),
      usr(res('e1', 'ok'), res('f1', 'needle failed', true)),
    ]
    const found = find(units(messages, 'chat', { tools, keyPrefix: 'P', turnStarts: [0, 3] }), 'needle')
    const byId = Object.fromEntries(found.map((m) => [m.unitId, m.reveal]))
    // A plain operation: the turn's tools line first, then the room block's own fold.
    expect(byId[searchUnitId('1:0', 'arg')]).toEqual(['P-turn-0:chat-tools'])
    expect(byId[searchUnitId('1:0', 'output')]).toEqual(['P-turn-0:chat-tools', '1:0'])
    // An edit is its own line, and only its diff is drawn behind it.
    expect(byId[searchUnitId('4:0', 'diff', 0)]).toEqual(['4:0:chat-edited', '4:0:diff'])
    // A failure is its own red line, expanding into the room block.
    expect(byId[searchUnitId('4:1', 'output')]).toEqual(['4:1:chat-failed', '4:1'])
    expect(found).toHaveLength(4)
  })

  it('chat does not search thinking', () => {
    const messages = [said('q'), asst({ type: 'thinking', thinking: 'a deep needle' }, { type: 'text', text: 'Answer' })]
    expect(find(units(messages, 'chat'), 'needle')).toEqual([])
  })

  it('room does', () => {
    const messages = [said('q'), asst({ type: 'thinking', thinking: 'a deep needle' }, { type: 'text', text: 'Answer' })]
    const matches = find(units(messages, 'room'), 'needle')
    expect(matches).toEqual([{ unitId: searchUnitId('1:0', 'thinking'), start: 7, end: 13, reveal: ['1:0:thinking'] }])
  })

  it('is case insensitive and literal', () => {
    const messages = [said('Find A.B and axb and a.b')]
    const matches = find(units(messages, 'room'), 'a.b')
    expect(matches.map((m) => m.start)).toEqual([5, 21])
    expect(find(units(messages, 'room'), 'AXB').map((m) => m.start)).toEqual([13])
  })

  it('ignores a one-letter query', () => {
    const messages = [said('a a a')]
    expect(find(units(messages, 'room'), 'a')).toEqual([])
    expect(find(units(messages, 'room'), '')).toEqual([])
  })

  // A10: a single Han character narrows a transcript as well as two Latin letters.
  it('a single Han character searches', () => {
    const messages = [said('有錯誤，檔案沒找到'), said('一切正常')]
    expect(find(units(messages, 'room'), '錯').map((m) => [m.unitId, m.start])).toEqual([[searchUnitId('0:0', 'text'), 1]])
    // Kana and Hangul count as CJK too.
    expect(find(units([said('ファイル 파일')], 'room'), 'フ')).toHaveLength(1)
    expect(find(units([said('ファイル 파일')], 'room'), '파')).toHaveLength(1)
  })

  // A F8: Bopomofo, and the long-vowel mark (full and half width), are CJK too.
  it('a single Bopomofo letter or long-vowel mark searches', () => {
    expect(find(units([said('注音 ㄅㄆㄇ')], 'room'), 'ㄅ')).toHaveLength(1)
    expect(find(units([said('コーヒー')], 'room'), 'ー')).toHaveLength(2)
    expect(find(units([said('ｺｰﾋｰ')], 'room'), 'ｰ')).toHaveLength(2)
  })

  it('a single Latin letter does not', () => {
    const messages = [said('a b c')]
    expect(find(units(messages, 'room'), 'a')).toEqual([])
    // Padding does not make it long enough: the query is trimmed first.
    expect(find(units(messages, 'room'), ' a ')).toEqual([])
    expect(find(units(messages, 'room'), 'é')).toEqual([])
  })

  it('whitespace-only searches nothing', () => {
    const messages = [said('a  b\t\tc')]
    expect(find(units(messages, 'room'), '  ')).toEqual([])
    expect(find(units(messages, 'room'), '\t\t')).toEqual([])
    expect(find(units(messages, 'room'), '')).toEqual([])
    // Around real text the spaces are trimmed, not searched.
    expect(find(units([said('xx yy')], 'room'), '  yy ').map((m) => m.start)).toEqual([3])
  })

  it('NFD input matches NFC text', () => {
    const nfc = 'café'
    const nfd = 'café'
    expect(nfc).not.toBe(nfd)
    // An NFD query over NFC text…
    expect(find(units([said(`a ${nfc} b`)], 'room'), nfd).map((m) => [m.start, m.end])).toEqual([[2, 6]])
    // …and an NFC query over NFD text: the unit text is NFC at index time, so
    // the offsets are offsets into that NFC text.
    const list = units([said(`a ${nfd} b`)], 'room')
    expect(list[0].text).toBe(`a ${nfc} b`)
    expect(find(list, nfc).map((m) => [m.start, m.end])).toEqual([[2, 6]])
  })

  it('keeps transcript order', () => {
    const messages = [
      said('xx one'),
      asst({ type: 'text', text: 'xx two' }, use('t1', 'Bash', { command: 'xx three', timeout: 5 })),
      usr(res('t1', 'xx four')),
      said('xx five'),
      asst({ type: 'text', text: 'xx six' }),
    ]
    const tools: Record<string, ToolActivity> = {
      t1: { name: 'Bash', startedAt: 0, endedAt: 1, status: 'done', primaryArg: { key: 'command', value: 'xx three' } },
    }
    const room = find(units(messages, 'room', { tools, turnStarts: [0, 3] }), 'xx')
    expect(room.map((m) => m.unitId)).toEqual([
      searchUnitId('0:0', 'text'),
      searchUnitId('1:0', 'text'),
      searchUnitId('1:1', 'arg'),
      searchUnitId('1:1', 'input'),
      searchUnitId('1:1', 'output'),
      searchUnitId('3:0', 'text'),
      searchUnitId('4:0', 'text'),
    ])
    // The raw input needs its own key on top of nothing else.
    expect(room[3].reveal).toEqual(['1:1:input'])
  })

  it('chat draws the tools line where the first ordinary tool sits, holding every one of the turn', () => {
    const messages = [
      said('go'),
      asst(use('a', 'Bash', { command: 'xx a' })),
      usr(res('a', 'ok')),
      asst({ type: 'text', text: 'xx between' }),
      asst(use('b', 'Bash', { command: 'xx b' })),
      usr(res('b', 'ok')),
    ]
    const chat = find(units(messages, 'chat', { tools: { a: ran('xx a'), b: ran('xx b') } }), 'xx')
    expect(chat.map((m) => m.unitId)).toEqual([
      searchUnitId('1:0', 'arg'),
      searchUnitId('4:0', 'arg'),
      searchUnitId('3:0', 'text'),
    ])
  })

  it('finds every occurrence inside one unit', () => {
    const matches = find(units([said('abab ab')], 'room'), 'ab')
    expect(matches.map((m) => [m.start, m.end])).toEqual([[0, 2], [2, 4], [5, 7]])
  })

  it.each<'room' | 'chat'>(['room', 'chat'])('%s indexes agent prose as rendered, not as markdown source', (view) => {
    // R1-F2: the URL is not on screen, the split word is.
    const prose = asst({ type: 'text', text: 'see [docs](https://needle.dev), nee**dle** and nee**dle**' })
    const matches = find(units([said('go'), prose], view), 'needle')
    expect(matches.map((m) => m.unitId)).toEqual([searchUnitId('1:0', 'text'), searchUnitId('1:0', 'text')])
    // A user's line is drawn verbatim: its text stays the source.
    expect(find(units([said('[docs](https://needle.dev)')], view), 'needle')).toHaveLength(1)
  })

  it('stops at the limit and says so', () => {
    const list: SearchUnit[] = [{ id: 'a', text: 'ab '.repeat(6), reveal: [] }, { id: 'b', text: 'ab', reveal: [] }]
    expect(findMatches(list, 'ab', 4)).toMatchObject({ truncated: true })
    expect(findMatches(list, 'ab', 4).matches).toHaveLength(4)
    // Exactly at the limit is not truncated; the default is 10,000.
    expect(findMatches(list, 'ab', 7)).toMatchObject({ truncated: false })
    expect(findMatches(list, 'ab', 7).matches).toHaveLength(7)
    expect(SEARCH_MATCH_LIMIT).toBe(10_000)
    const many: SearchUnit[] = [{ id: 'm', text: 'ab'.repeat(10_001), reveal: [] }]
    expect(findMatches(many, 'ab')).toMatchObject({ truncated: true })
    expect(findMatches(many, 'ab').matches).toHaveLength(10_000)
  })
})

// A5: the current match is remembered as (unit, ordinal within the unit), not
// as a position in the list, which shifts whenever a match lands before it.
describe('matchIdentity / findCurrent', () => {
  const U = (id: string, text: string): SearchUnit => ({ id, text, reveal: [] })

  it('keeps the current match by identity when others land before it', () => {
    const before = [U('a', 'ab'), U('c', 'ab ab')]
    const { matches: m1 } = findMatches(before, 'ab')
    const id = matchIdentity(before, m1, 2) // the second `ab` of c
    expect(id).toEqual({ unitId: 'c', ordinal: 1, unitPos: 1 })
    const after = [U('a', 'ab'), U('b', 'ab ab'), U('c', 'ab ab')]
    const { matches: m2 } = findMatches(after, 'ab')
    expect(findCurrent(after, m2, id)).toBe(4)
    // Two units landing before it push it past its old position.
    const more = [U('a', 'ab'), U('b', 'ab'), U('b2', 'ab'), U('c', 'ab ab')]
    expect(findCurrent(more, findMatches(more, 'ab').matches, id)).toBe(4)
  })

  it('falls to the nearest following match when it is gone, else the last', () => {
    const units1 = [U('a', 'ab'), U('b', 'ab ab'), U('c', 'ab')]
    const { matches } = findMatches(units1, 'ab')
    const id = matchIdentity(units1, matches, 2) // b's second
    // b now holds one occurrence: the next one after it is c's.
    const units2 = [U('a', 'ab'), U('b', 'ab'), U('c', 'ab')]
    expect(findCurrent(units2, findMatches(units2, 'ab').matches, id)).toBe(2)
    // Nothing follows it: the last.
    const units3 = [U('a', 'ab'), U('b', 'ab'), U('c', 'x')]
    expect(findCurrent(units3, findMatches(units3, 'ab').matches, id)).toBe(1)
  })

  // R1-4: the unit that moves up into the gone unit's position follows it.
  it('a gone unit hands over to the unit now in its place', () => {
    const units1 = [U('a', 'ab'), U('b', 'ab'), U('c', 'ab'), U('d', 'ab')]
    const id = matchIdentity(units1, findMatches(units1, 'ab').matches, 1) // b's
    const units2 = [U('a', 'ab'), U('c', 'ab'), U('d', 'ab')]
    const m2 = findMatches(units2, 'ab').matches
    expect(m2[findCurrent(units2, m2, id)].unitId).toBe('c')
  })

  it('starts at the first match and is -1 with none', () => {
    const units = [U('a', 'ab')]
    expect(findCurrent(units, findMatches(units, 'ab').matches, null)).toBe(0)
    expect(findCurrent(units, [], null)).toBe(-1)
    expect(matchIdentity(units, [], 0)).toBeNull()
  })
})
