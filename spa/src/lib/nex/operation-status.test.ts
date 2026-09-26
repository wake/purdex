// spa/src/lib/nex/operation-status.test.ts — the one classifier room and chat
// share (R2 plan T2.0): a turn's top-level operations, in call order, as
// plain / edited / failed.
import { describe, it, expect } from 'vitest'
import { classifyTurnOperations } from './operation-status'
import { indexOperations } from './operations'
import type { ContentBlock, StreamMessage } from './message-types'
import type { ToolActivity } from './tool-activity'

const asst = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'assistant', message: { id: 'm', role: 'assistant', content: blocks, stop_reason: null } } as StreamMessage)
const usr = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'user', message: { role: 'user', content: blocks, stop_reason: null } } as StreamMessage)
const use = (id: string, name: string, input: Record<string, unknown> = {}): ContentBlock =>
  ({ type: 'tool_use', id, name, input })
const res = (id: string, content: string, isError = false): ContentBlock =>
  ({ type: 'tool_result', tool_use_id: id, content, is_error: isError })
const child = (m: StreamMessage, parent: string): StreamMessage =>
  ({ ...m, parent_tool_use_id: parent }) as StreamMessage

const entry = (status: ToolActivity['status'], over: Partial<ToolActivity> = {}): ToolActivity =>
  ({ name: 'x', startedAt: 1, endedAt: status === 'running' ? null : 2, status, ...over })
const diff = { path: '/w/notes.md', added: 3, removed: 0, hunks: [], truncated: false }

const classify = (messages: StreamMessage[], tools?: Record<string, ToolActivity>) =>
  classifyTurnOperations(messages, { start: 0, end: messages.length }, indexOperations(messages), tools)

describe('classifyTurnOperations', () => {
  it('classifies an error and a denied call as failed', () => {
    const messages = [
      asst(use('a', 'Bash', { command: 'false' }), use('b', 'Write', { file_path: '/x' })),
      usr(res('a', 'boom', true), res('b', 'denied')),
    ]
    // `a` fails by the raw frame alone; `b` by the N2 status, which outranks the frame.
    const ops = classify(messages, { b: entry('denied') })
    expect(ops.map((o) => [o.key, o.kind, o.status])).toEqual([
      ['0:0', 'failed', 'error'],
      ['0:1', 'failed', 'denied'],
    ])
  })

  it('classifies a diff as edited', () => {
    const messages = [asst(use('e', 'Edit', { file_path: '/w/notes.md' })), usr(res('e', 'ok'))]
    expect(classify(messages, { e: entry('done', { diff }) }).map((o) => o.kind)).toEqual(['edited'])
    // Without the N2 diff the same call is an ordinary tool.
    expect(classify(messages).map((o) => o.kind)).toEqual(['plain'])
  })

  it('a failed edit is failed, not edited', () => {
    const messages = [asst(use('e', 'Edit', { file_path: '/w/notes.md' })), usr(res('e', 'no match', true))]
    expect(classify(messages, { e: entry('error', { diff }) }).map((o) => o.kind)).toEqual(['failed'])
  })

  it("skips a subagent's own calls", () => {
    const messages = [
      asst(use('T', 'Task', { subagent_type: 'general-purpose' })),
      child(asst(use('c', 'Bash', { command: 'ls' })), 'T'),
      child(usr(res('c', 'boom', true)), 'T'),
      usr(res('T', 'handed back')),
    ]
    const ops = classify(messages)
    // Only the Task: its subagent's failed Bash is inside it, and a Task is plain.
    expect(ops.map((o) => [o.key, o.kind])).toEqual([['0:0', 'plain']])
  })

  it('keeps call order', () => {
    const messages = [
      asst(use('a', 'Read', { file_path: '/a' })),
      usr(res('a', 'x')),
      asst(use('b', 'Edit', { file_path: '/w/notes.md' }), use('c', 'Bash', { command: 'false' })),
      usr(res('b', 'ok'), res('c', 'boom', true)),
      asst(use('d', 'Grep', { pattern: 'q' })),
    ]
    const ops = classify(messages, { b: entry('done', { diff }) })
    expect(ops.map((o) => [o.key, o.msgIndex, o.blockIndex, o.kind])).toEqual([
      ['0:0', 0, 0, 'plain'],
      ['2:0', 2, 0, 'edited'],
      ['2:1', 2, 1, 'failed'],
      ['4:0', 4, 0, 'plain'],
    ])
  })

  it('a running call is plain', () => {
    const messages = [asst(use('r', 'Bash', { command: 'sleep 9' }))]
    const ops = classify(messages, { r: entry('running') })
    expect(ops.map((o) => [o.kind, o.status])).toEqual([['plain', 'running']])
    // Unanswered and unknown to N2: still a plain call, pending.
    expect(classify(messages).map((o) => [o.kind, o.status])).toEqual([['plain', 'pending']])
  })

  it('only walks the given turn, and counts an orphan result the room draws', () => {
    const messages = [
      asst(use('a', 'Read', { file_path: '/a' })),
      usr(res('a', 'x')),
      // A result whose call is off the list: the room draws it as its own block.
      usr(res('gone', 'boom', true)),
    ]
    const ops = classifyTurnOperations(messages, { start: 1, end: 3 }, indexOperations(messages), undefined)
    expect(ops.map((o) => [o.key, o.kind])).toEqual([['2:0', 'failed']])
  })
})
