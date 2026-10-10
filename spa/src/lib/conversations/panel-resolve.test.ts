// Panel references must resolve inside a subagent's children too (nested output, nested subagent). Hand-made nesting: the
// goldens hold no subagent that itself spawns one.
import { describe, expect, it } from 'vitest'
import type { ConversationItem, StepItem } from './types'
import { resolvePanel, type PanelTurn } from './panel-resolve'

const step = (id: string, over: Partial<StepItem> = {}): StepItem =>
  ({ type: 'step', id, at: 1, index: 0, kind: 'execute', tool: 'Bash', status: 'done', summary: id, started_at: 1000, duration_ms: 1, input: {}, ...over })

const inner = step('inner-out', { output: { text: 'x\ny', total_lines: 2, total_bytes: 3, truncated: false } })
const nestedTask = step('nested-task', { kind: 'task', subagent: { agent_id: 'b' }, children: [inner] as StepItem['children'] })
const outerTask = step('outer-task', { kind: 'task', subagent: { agent_id: 'a' }, children: [nestedTask] as StepItem['children'] })
const turns = (): PanelTurn[] => [{ id: 't0', index: 0, items: [outerTask as ConversationItem] }]

describe('resolvePanel needs the turn and the step together', () => {
  const two = (): PanelTurn[] => [
    { id: 'tA', index: 0, items: [step('same', { summary: 'in A' }) as ConversationItem] },
    { id: 'tB', index: 1, items: [step('same', { summary: 'in B' }) as ConversationItem] },
  ]
  it('the same step id in two turns resolves to the named turn\'s step', () => {
    const v = resolvePanel({ kind: 'output', turnId: 'tB', stepId: 'same' }, two())
    expect(v?.kind === 'output' && v.step.summary).toBe('in B')
  })
  it('a step id that exists only in another turn does not resolve', () => {
    const only: PanelTurn[] = [{ id: 'tA', index: 0, items: [step('x') as ConversationItem] }, { id: 'tB', index: 1, items: [] }]
    expect(resolvePanel({ kind: 'output', turnId: 'tB', stepId: 'x' }, only)).toBeNull()
    expect(resolvePanel({ kind: 'subagent', turnId: 'gone', stepId: 'x' }, only)).toBeNull()
  })
})

describe('resolvePanel survives hostile children (it runs during render)', () => {
  const wrap = (s: StepItem): PanelTurn[] => [{ id: 't0', index: 0, items: [s as ConversationItem] }]

  it('a children cycle terminates and a missing step is null', () => {
    const a = step('a', { kind: 'task' })
    const b = step('b', { kind: 'task', children: [a] as StepItem['children'] })
    a.children = [b] as StepItem['children']
    expect(resolvePanel({ kind: 'output', turnId: 't0', stepId: 'nope' }, wrap(a))).toBeNull()
  })

  it('a cycle does not hide a step that is reachable', () => {
    const target = step('target')
    const a = step('a', { kind: 'task' })
    a.children = [a, target] as StepItem['children']
    const v = resolvePanel({ kind: 'output', turnId: 't0', stepId: 'target' }, wrap(a))
    expect(v?.kind === 'output' && v.step.id).toBe('target')
  })

  it('a 20000-deep chain neither throws nor resolves (past the node limit it is gone)', () => {
    let leaf = step('leaf')
    for (let i = 0; i < 20000; i++) leaf = step(`n${i}`, { kind: 'task', children: [leaf] as StepItem['children'] })
    expect(() => resolvePanel({ kind: 'output', turnId: 't0', stepId: 'leaf' }, wrap(leaf))).not.toThrow()
    expect(resolvePanel({ kind: 'output', turnId: 't0', stepId: 'leaf' }, wrap(leaf))).toBeNull()
  })

  it('an ordinary nested chain well under the limit still resolves', () => {
    let leaf = step('leaf')
    for (let i = 0; i < 200; i++) leaf = step(`n${i}`, { kind: 'task', children: [leaf] as StepItem['children'] })
    expect(resolvePanel({ kind: 'output', turnId: 't0', stepId: 'leaf' }, wrap(leaf))?.kind).toBe('output')
  })
})

describe('resolvePanel inside subagents', () => {
  it('a step under a subagent opens its full output', () => {
    const v = resolvePanel({ kind: 'output', turnId: 't0', stepId: 'inner-out' }, turns())
    expect(v?.kind).toBe('output')
    expect(v?.kind === 'output' && v.step.id).toBe('inner-out')
  })

  it('a subagent under a subagent opens its own steps', () => {
    const v = resolvePanel({ kind: 'subagent', turnId: 't0', stepId: 'nested-task' }, turns())
    expect(v?.kind === 'subagent' && v.items.map((i) => i.id)).toEqual(['inner-out'])
  })
})
