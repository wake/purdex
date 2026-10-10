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

describe('resolvePanel inside subagents', () => {
  it('a step under a subagent opens its full output', () => {
    const v = resolvePanel({ kind: 'output', stepId: 'inner-out' }, turns())
    expect(v?.kind).toBe('output')
    expect(v?.kind === 'output' && v.step.id).toBe('inner-out')
  })

  it('a subagent under a subagent opens its own steps', () => {
    const v = resolvePanel({ kind: 'subagent', stepId: 'nested-task' }, turns())
    expect(v?.kind === 'subagent' && v.items.map((i) => i.id)).toEqual(['inner-out'])
  })
})
