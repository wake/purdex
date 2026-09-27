// spa/src/components/room/search-anchors.test.tsx — the search index and the
// transcripts must agree (R3 plan T3.1/T3.2): every unit's `reveal` names fold
// keys the components really register, expanding exactly those keys puts the
// unit's text on screen under `data-search-unit={id}`, and nothing on screen
// carries an anchor the index does not know. Rendered from the real room and
// chat transcripts so a renamed fold key or a moved anchor fails here.
import { useEffect, useMemo } from 'react'
import { describe, it, expect } from 'vitest'
import { act, render } from '@testing-library/react'
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

// Every kind of unit, folded several ways, across two turns.
const messages: StreamMessage[] = [
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
  asst(call('edit', 'Edit', { file_path: '/r/notes.md' }), call('fail', 'Bash', { command: 'false' })), // 11
  usr(res('edit', 'edited ok'), res('fail', lines(12, 'boom'), true)),           // 12
  usr(res('orphan', 'orphan body')),                                             // 13
]
const turnStarts = [0, 10]
const tools: Record<string, ToolActivity> = {
  bash: { name: 'Bash', startedAt: 0, endedAt: 1, status: 'done', primaryArg: { key: 'command', value: 'ls -la' } },
  edit: { name: 'Edit', startedAt: 0, endedAt: 1, status: 'done', primaryArg: { key: 'file_path', value: '/r/notes.md' }, diff: diff('/r/notes.md', 12) },
  fail: { name: 'Bash', startedAt: 0, endedAt: 1, status: 'error', primaryArg: { key: 'command', value: 'false' } },
  orphan: { name: 'Write', startedAt: 0, endedAt: 1, status: 'done', diff: diff('/r/o.txt', 2) },
}

type View = 'room' | 'chat'

function Harness({ view, registered, onStore }: { view: View; registered: Set<string>; onStore: (s: FoldStore) => void }) {
  const base = useFoldMemory()
  const store = useMemo<FoldStore>(() => ({
    ...base,
    register: (turn, key) => { registered.add(key); base.register(turn, key) },
  }), [base, registered])
  useEffect(() => { onStore(store) }, [store, onStore])
  const Transcript = view === 'room' ? RoomTranscript : ChatTranscript
  return (
    <FoldContext.Provider value={store}>
      <Transcript messages={messages} keyPrefix="k" showThinking={false} showEmptyHint={false}
        turnStarts={turnStarts} tools={tools} />
    </FoldContext.Provider>
  )
}

function mount(view: View) {
  const registered = new Set<string>()
  const ref: { store: FoldStore | null } = { store: null }
  const utils = render(<Harness view={view} registered={registered} onStore={(s) => { ref.store = s }} />)
  const expand = (keys: string[]) => act(() => ref.store!.expand(keys))
  const anchors = (id?: string) => [...utils.container.querySelectorAll('[data-search-unit]')]
    .filter((el) => id === undefined || el.getAttribute('data-search-unit') === id)
  return { ...utils, registered, expand, anchors }
}

const unitsFor = (view: View): SearchUnit[] =>
  buildSearchUnits({ messages, index: indexOperations(messages), tools, view, keyPrefix: 'k', turnStarts })

/** Every unit's text is exactly what its anchor draws: prose as proseText, the rest verbatim. */
const rendered = (el: Element) => el.textContent ?? ''

describe.each<View>(['room', 'chat'])('search anchors in %s', (view) => {
  const units = unitsFor(view)

  it('covers every kind of unit the fixture holds', () => {
    const parts = new Set(units.map((u) => u.id.split(':')[2]))
    // Chat too has thinking: not at the top level, but inside the subagent,
    // which chat draws with the room's renderer.
    expect([...parts].sort()).toEqual(['arg', 'diff', 'input', 'output', 'text', 'thinking'])
    if (view === 'chat') expect(units.filter((u) => u.id.endsWith(':thinking')).map((u) => u.id)).toEqual(['5:0:thinking'])
  })

  it('every reveal key is a fold key the components register', () => {
    const { registered, expand } = mount(view)
    expand(units.flatMap((u) => u.reveal))
    const missing = [...new Set(units.flatMap((u) => u.reveal))].filter((k) => !registered.has(k))
    expect(missing).toEqual([])
  })

  it('expanding a unit\'s reveal keys draws its text under its anchor', () => {
    for (const unit of units) {
      const { expand, anchors, unmount } = mount(view)
      expand(unit.reveal)
      const found = anchors(unit.id)
      expect(found, unit.id).toHaveLength(1)
      expect(rendered(found[0]), unit.id).toBe(unit.text)
      unmount()
    }
  })

  it('draws no anchor the index does not know', () => {
    const { expand, anchors } = mount(view)
    expand(units.flatMap((u) => u.reveal))
    const drawn = anchors().map((el) => el.getAttribute('data-search-unit'))
    expect(drawn).toEqual(units.map((u) => u.id))
  })
})
