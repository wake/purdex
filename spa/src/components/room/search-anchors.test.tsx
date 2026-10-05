// spa/src/components/room/search-anchors.test.tsx — the search index and the
// transcripts must agree (R3 plan T3.1/T3.2): every unit's `reveal` names fold
// keys the components really register, expanding exactly those keys puts the
// unit's text on screen under `data-search-unit={id}`, and nothing on screen
// carries an anchor the index does not know. Rendered from the real room and
// chat transcripts so a renamed fold key or a moved anchor fails here.
import { useEffect, useMemo } from 'react'
import { describe, it, expect } from 'vitest'
import { act, render } from '@testing-library/react'
import PreludeSection from './prelude/PreludeSection'
import { derivePrelude, type PreludeView } from '../../lib/nex/prelude'
import { sanitizePreludePage } from '../../lib/nex/prelude-wire'
import golden from '../../lib/nex/__fixtures__/prelude-golden-nexen.json'
import RoomTranscript from './RoomTranscript'
import ChatTranscript from '../chat/ChatTranscript'
import { FoldContext, useFoldMemory, type FoldStore } from './fold-context'
import { indexOperations } from '../../lib/nex/operations'
import { buildSearchUnits, type SearchUnit } from '../../lib/nex/transcript-search'
import { INTERRUPT_TEXT } from '../../lib/nex/turns'
import type { ContentBlock, StreamMessage } from '../../lib/nex/message-types'
import type { ToolActivity } from '../../lib/nex/tool-activity'

const asst = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'assistant', message: { id: 'm', role: 'assistant', content: blocks, stop_reason: null } } as StreamMessage)
const usr = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'user', message: { role: 'user', content: blocks, stop_reason: null } } as StreamMessage)
const said = (text: string): StreamMessage => usr({ type: 'text', text })
const call = (id: string, name: string, input: Record<string, unknown> = {}): ContentBlock =>
  ({ type: 'tool_use', id, name, input })
const res = (id: string, content: string, isError = false): ContentBlock =>
  ({ type: 'tool_result', tool_use_id: id, content, is_error: isError })
const child = (m: StreamMessage, parent: string): StreamMessage =>
  ({ ...m, parent_tool_use_id: parent }) as StreamMessage
const lines = (n: number, tag: string) => Array.from({ length: n }, (_, k) => `${tag} ${k}`).join('\n')
const diff = (path: string, n: number): NonNullable<ToolActivity['diff']> => ({
  path, added: n, removed: 1, truncated: false,
  hunks: [{ oldStart: 1, oldLines: 1, newStart: 1, newLines: n, lines: ['-old line', ...Array.from({ length: n }, (_, k) => `+new ${k}`)] }],
})

interface Fixture {
  messages: StreamMessage[]
  turnStarts: number[]
  tools: Record<string, ToolActivity>
  prelude?: PreludeView
}

// Every kind of unit, folded several ways, across two turns.
const everyKind: Fixture = {
  messages: [
    said('first question'),                                                        // 0
    asst(
      { type: 'thinking', thinking: lines(20, 'thought') },
      { type: 'text', text: 'plain **prose** [answer](https://x.dev)\n\n```sh\nls\n```' },
      call('bash', 'Bash', { command: 'ls -la', timeout: 5 }),                      // raw input
    ),                                                                             // 1
    usr(res('bash', lines(60, 'out'))),                                            // 2 folded output
    asst(call('task', 'Task', { description: 'explore', subagent_type: 'Explore' })), // 3
    child(said('subagent prompt'), 'task'),                                        // 4
    child(asst({ type: 'thinking', thinking: 'child thinks' }, call('grep', 'Grep', { pattern: 'x' })), 'task'), // 5
    child(usr(res('grep', lines(10, 'hit'))), 'task'),                             // 6
    usr(res('task', 'hand back')),                                                 // 7
    said('/compact'),                                                              // 8
    said(INTERRUPT_TEXT),                                                          // 9
    said('second question'),                                                       // 10
    asst(
      call('edit', 'Edit', { file_path: '/r/notes.md' }),                          // header names the file: no path
      call('fail', 'Bash', { command: 'false' }),
      call('sed', 'Bash', { command: 'sed -i s/a/b/ /r/x.txt' }),                  // header is a command: the diff names the file
      call('touch', 'Write', { file_path: '/r/empty.txt' }),                       // a diff with no hunks draws nothing
    ),                                                                             // 11
    usr(res('edit', 'edited ok'), res('fail', lines(12, 'boom'), true), res('sed', 'sed ok'), res('touch', 'ok')), // 12
    usr(res('orphan', 'orphan body')),                                             // 13
  ],
  turnStarts: [0, 10],
  tools: {
    bash: { name: 'Bash', startedAt: 0, endedAt: 1, status: 'done', primaryArg: { key: 'command', value: 'ls -la' } },
    edit: { name: 'Edit', startedAt: 0, endedAt: 1, status: 'done', primaryArg: { key: 'file_path', value: '/r/notes.md' }, diff: diff('/r/notes.md', 12) },
    fail: { name: 'Bash', startedAt: 0, endedAt: 1, status: 'error', primaryArg: { key: 'command', value: 'false' } },
    sed: { name: 'Bash', startedAt: 0, endedAt: 1, status: 'done', primaryArg: { key: 'command', value: 'sed -i s/a/b/ /r/x.txt' }, diff: diff('/r/x.txt', 3) },
    touch: { name: 'Write', startedAt: 0, endedAt: 1, status: 'done', primaryArg: { key: 'file_path', value: '/r/empty.txt' }, diff: { path: '/r/empty.txt', added: 0, removed: 0, truncated: false, hunks: [] } },
    orphan: { name: 'Write', startedAt: 0, endedAt: 1, status: 'done', diff: diff('/r/o.txt', 2) },
  },
}

// The shapes the attacker verified by hand (PR #1492 review): a subagent inside
// a subagent, child frames whose Task is not in the list, and one call id used
// by two calls.
const edgeShapes: Fixture = {
  messages: [
    said('q1'),                                                                    // 0
    asst(call('t1', 'Task', { description: 'outer', subagent_type: 'A' })),        // 1
    child(asst({ type: 'text', text: 'outer child prose' }, call('t2', 'Task', { description: 'inner', subagent_type: 'B' })), 't1'), // 2
    child(asst({ type: 'text', text: 'inner child prose' }, call('g', 'Grep', { pattern: 'p', path: '/x' })), 't2'), // 3
    child(usr(res('g', lines(30, 'deep'))), 't2'),                                 // 4
    child(usr(res('t2', 'inner back')), 't1'),                                     // 5
    usr(res('t1', 'outer back')),                                                  // 6
    child(said('frame with missing task'), 'nope'),                                // 7
    child(asst({ type: 'text', text: 'orphan frame prose' }, { type: 'thinking', thinking: 'orphan thought' }), 'nope'), // 8
    asst(call('dup', 'Bash', { command: 'a' })),                                   // 9
    usr(res('dup', 'first dup')),                                                  // 10
    asst(call('dup', 'Bash', { command: 'b' })),                                   // 11
    usr(res('dup', lines(40, 'second dup'))),                                      // 12
    usr(res('lonely', 'no call')),                                                 // 13
  ],
  turnStarts: [0, 9],
  tools: {},
}

// A prelude above a short live list: a turn with a folded output, every note
// source, markers, and a second turn that opens with the human's line.
const note = (pos: string, source: string, text: string) =>
  ({ pos, at: 0, kind: 'prelude.note', source, text, truncated: false, totalBytes: null, stream: null }) as const
const withPrelude: Fixture = {
  messages: [said('live question'), asst({ type: 'text', text: 'live **answer**' })],
  turnStarts: [0],
  tools: {},
  prelude: derivePrelude([
    { pos: '1', at: 0, kind: 'user', msg: said('early question') },
    { pos: '2', at: 0, kind: 'assistant', msg: asst({ type: 'thinking', thinking: 'early thought' }, { type: 'text', text: 'early *answer*' }, { type: 'text', text: 'cut off', truncated: true, total_bytes: 9000 }, call('pb', 'Bash', { command: 'ls' })) },
    { pos: '3', at: 0, kind: 'user', msg: usr(res('pb', lines(60, 'pout'))) },
    { pos: '4', at: 0, kind: 'prelude.segment', entrypoint: 'cli' },
    note('5', 'bash_input', 'ls -la'),
    note('6', 'command_output', lines(40, 'cmd')),
    note('7', 'peer_message', 'peer **says** hi'),
    note('8', 'task_notification', 'task finished'),
    { ...note('12', 'bash_output', 'stderr text'), stream: 'stderr' },
    note('13', 'future_source', 'unknown source text'),
    { pos: '9', at: 0, kind: 'prelude.compaction', trigger: 'auto' },
    { pos: '10', at: 0, kind: 'user', msg: said('second early question') },
    { pos: '11', at: 0, kind: 'assistant', msg: asst({ type: 'text', text: 'second early answer' }) },
  ]),
}

// Nexen's early golden page above a short live list (spec §4.6): real wire
// shapes — N2 pairs, cut blocks, omitted media, every note source.
const goldenPrelude: Fixture = {
  messages: [said('live question'), asst({ type: 'text', text: 'live answer' })],
  turnStarts: [0],
  tools: {},
  prelude: derivePrelude(sanitizePreludePage(golden)!.items),
}

// U3: pasted text in the prelude — typed + a folded paste + typed, a short
// paste that starts with `/`, and a paste the daemon cut.
const pasted = (id: string, body: string) => `<pasted_content id="${id}">\n${body}\n</pasted_content>`
const withPaste: Fixture = {
  messages: [said('live question')],
  turnStarts: [0],
  tools: {},
  prelude: derivePrelude([
    { pos: '1', at: 0, kind: 'user', msg: said(`look at this:\n${pasted('a', lines(30, 'pasted'))}\nthanks`) },
    { pos: '2', at: 0, kind: 'assistant', msg: asst({ type: 'text', text: 'seen' }) },
    { pos: '3', at: 0, kind: 'user', msg: said(pasted('b', '/compact short')) },
    { pos: '4', at: 0, kind: 'user', msg: usr({ type: 'text', text: `cut <pasted_content id="c">\n${lines(50, 'cut')}`, truncated: true, total_bytes: 99999 }) },
  ]),
}

type View = 'room' | 'chat'

function Harness({ fixture, view, registered, onStore }: {
  fixture: Fixture; view: View; registered: Set<string>; onStore: (s: FoldStore) => void
}) {
  const base = useFoldMemory()
  const store = useMemo<FoldStore>(() => ({
    ...base,
    register: (turn, key) => { registered.add(key); base.register(turn, key) },
  }), [base, registered])
  useEffect(() => { onStore(store) }, [store, onStore])
  const Transcript = view === 'room' ? RoomTranscript : ChatTranscript
  return (
    <FoldContext.Provider value={store}>
      <Transcript messages={fixture.messages} keyPrefix="k" showThinking={false} showEmptyHint={false}
        turnStarts={fixture.turnStarts} tools={fixture.tools}
        {...(fixture.prelude ? { prelude: (
          <PreludeSection view={fixture.prelude} status="ok" done error={null} keyPrefix="k" mode={view} pages={1} onLoadOlder={() => {}} onRetry={() => {}} />
        ), preludeVersion: '1' } : {})} />
    </FoldContext.Provider>
  )
}

function mount(fixture: Fixture, view: View) {
  const registered = new Set<string>()
  const ref: { store: FoldStore | null } = { store: null }
  const utils = render(<Harness fixture={fixture} view={view} registered={registered} onStore={(s) => { ref.store = s }} />)
  const expand = (keys: string[]) => act(() => ref.store!.expand(keys))
  const anchors = (id?: string) => [...utils.container.querySelectorAll('[data-search-unit]')]
    .filter((el) => id === undefined || el.getAttribute('data-search-unit') === id)
  return { ...utils, registered, expand, anchors }
}

const unitsFor = (fixture: Fixture, view: View): SearchUnit[] => buildSearchUnits({
  messages: fixture.messages, index: indexOperations(fixture.messages), tools: fixture.tools,
  view, keyPrefix: 'k', turnStarts: fixture.turnStarts, prelude: fixture.prelude,
})

/** Every unit's text is exactly what its anchor draws: prose as proseText, the rest verbatim. */
const rendered = (el: Element) => el.textContent ?? ''

describe.each<View>(['room', 'chat'])('search anchors in %s', (view) => {
  const units = unitsFor(everyKind, view)

  it('covers every kind of unit the fixture holds', () => {
    const parts = new Set(units.map((u) => u.id.split(':')[2]))
    // Chat too has thinking: not at the top level, but inside the subagent,
    // which chat draws with the room's renderer.
    const expected = ['arg', 'diff', 'input', 'output', 'path', 'text', 'thinking']
    // Only chat draws an edited line, whose label names the file.
    if (view === 'chat') expected.splice(2, 0, 'file')
    expect([...parts].sort()).toEqual(expected)
    if (view === 'chat') expect(units.filter((u) => u.id.endsWith(':thinking')).map((u) => u.id)).toEqual(['5:0:thinking'])
  })

  it('draws a diff\'s path wherever the header does not already name the file', () => {
    const paths = units.filter((u) => u.id.endsWith(':path')).map((u) => [u.id, u.text])
    // Room: not the Edit (its header is the path), not the hunk-less Write.
    // Chat: every edited line with hunks draws the full path in its diff.
    expect(paths).toEqual(view === 'room'
      ? [['11:2:path', '/r/x.txt'], ['13:0:path', '/r/o.txt']]
      : [['11:0:path', '/r/notes.md'], ['11:2:path', '/r/x.txt'], ['13:0:path', '/r/o.txt']])
    if (view === 'chat') {
      expect(units.filter((u) => u.id.endsWith(':file')).map((u) => [u.id, u.text, u.reveal])).toEqual([
        ['11:0:file', 'notes.md', []],
        ['11:2:file', 'x.txt', []],
        ['11:3:file', 'empty.txt', []],
        ['13:0:file', 'o.txt', []],
      ])
    }
  })
})

describe.each<[string, Fixture]>([['every kind', everyKind], ['edge shapes', edgeShapes], ['with a prelude', withPrelude], ['golden prelude', goldenPrelude], ['pasted text', withPaste]])('%s fixture', (_, fixture) => {
  describe.each<View>(['room', 'chat'])('%s', (view) => {
    const units = unitsFor(fixture, view)

    it('every reveal key is a fold key the components register', () => {
      const { registered, expand } = mount(fixture, view)
      expand(units.flatMap((u) => u.reveal))
      const missing = [...new Set(units.flatMap((u) => u.reveal))].filter((k) => !registered.has(k))
      expect(missing).toEqual([])
    })

    it('expanding a unit\'s reveal keys draws its text under its anchor', () => {
      for (const unit of units) {
        const { expand, anchors, unmount } = mount(fixture, view)
        expand(unit.reveal)
        const found = anchors(unit.id)
        expect(found, unit.id).toHaveLength(1)
        expect(rendered(found[0]), unit.id).toBe(unit.text)
        unmount()
      }
    // One full mount per unit: the golden prelude takes ~1.5 s alone, past the
    // default 5 s under a loaded full-suite run.
    }, 20_000)

    it('draws no anchor the index does not know', () => {
      const { expand, anchors } = mount(fixture, view)
      expand(units.flatMap((u) => u.reveal))
      const drawn = anchors().map((el) => el.getAttribute('data-search-unit'))
      expect(drawn).toEqual(units.map((u) => u.id))
    })
  })
})

// The complete, ordered prelude units of Nexen's golden page, written out by
// hand against the fixture (jq over `items`), not computed from the walk:
// every user/assistant text block (329.1 … 378700.1; 3746.1 is thinking), the
// nine notes, and the four tool calls — an `arg` unit each, an `input` unit
// only where the call's input has more than one key (Bash, Edit, Write; Read
// has just file_path), and an `output` unit for each paired result.
const GOLDEN_CHAT_IDS = [
  'p329.1:0:text',
  'p6688.1:0:arg', 'p6688.1:0:input', 'p6688.1:0:output',
  'p9403.1:0:text',
  'p13008.1:0:text',
  'p13569.1:note:text', 'p14108.1:note:text', 'p14558.1:note:text', 'p14558.2:note:text',
  'p15108.1:0:text',
  'p16045.1:0:arg', 'p16045.1:0:output',
  'p18651.1:0:text',
  'p19422.1:0:text',
  'p20300.1:note:text', 'p21398.1:note:text',
  'p31886.1:0:arg', 'p31886.1:0:input', 'p31886.1:0:output',
  'p34358.1:note:text', 'p35280.1:note:text',
  'p39088.1:0:text',
  'p39688.1:0:text',
  'p111120.1:0:arg', 'p111120.1:0:input', 'p111120.1:0:output',
  'p373526.1:0:text',
  'p374088.1:0:text',
  'p375917.1:0:text',
  'p376513.1:0:text',
  'p378700.1:0:text',
  'p379249.1:note:text',
]
// The one room/chat difference: the room draws the assistant's thinking (3746.1), chat does not.
const GOLDEN_ROOM_IDS = [...GOLDEN_CHAT_IDS.slice(0, 1), 'p3746.1:0:thinking', ...GOLDEN_CHAT_IDS.slice(1)]

describe.each<View>(['room', 'chat'])('golden prelude units in %s', (view) => {
  it('indexes the whole prelude, in drawing order — no span, note or call dropped', () => {
    const ids = unitsFor(goldenPrelude, view).map((u) => u.id).filter((id) => id.startsWith('p'))
    expect(ids).toEqual(view === 'room' ? GOLDEN_ROOM_IDS : GOLDEN_CHAT_IDS)
  })
})
