// spa/src/components/room/SubagentBlock.test.tsx — spec §4.5, #1263: a Task
// call's subagent is a nested rail inside the Task's block, not a second
// user and not a row of top-level operations. Driven through RoomTranscript,
// because what matters is where the child's frames end up in the transcript.
import { describe, it, expect } from 'vitest'
import { render, screen, within, fireEvent } from '@testing-library/react'
import RoomTranscript from './RoomTranscript'
import type { ContentBlock, StreamMessage } from '../../lib/nex/message-types'
import type { TaskUsage, WorkerTask } from '../../lib/nex/types'

const asst = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'assistant', message: { id: 'm', role: 'assistant', content: blocks, stop_reason: null } } as StreamMessage)
const usr = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'user', message: { role: 'user', content: blocks, stop_reason: null } } as StreamMessage)
const child = (m: StreamMessage, parent = 'T'): StreamMessage =>
  ({ ...m, parent_tool_use_id: parent }) as StreamMessage
const call = (id: string, name: string, input: Record<string, unknown> = {}): ContentBlock =>
  ({ type: 'tool_use', id, name, input })
const res = (id: string, content: unknown): ContentBlock =>
  ({ type: 'tool_result', tool_use_id: id, content, is_error: false } as ContentBlock)
const follows = (a: Element, b: Element) => !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)

const HAND_BACK = [{ type: 'text', text: 'Found three TODOs in notes.md' }]

/** said → Task → the subagent's prompt, a Read and a Grep, its summary → the hand-back. */
const delegation: StreamMessage[] = [
  usr({ type: 'text', text: 'please delegate' }),
  asst(call('T', 'Task', { description: 'analyse notes.md', subagent_type: 'general-purpose', prompt: 'read notes.md' })),
  child(usr({ type: 'text', text: 'read notes.md and list the TODOs' })),
  child(asst({ type: 'text', text: 'Reading the file.' }, call('r1', 'Read', { file_path: 'notes.md' }))),
  child(usr(res('r1', 'TODO a\nTODO b\nTODO c'))),
  child(asst(call('g1', 'Grep', { pattern: 'TODO' }))),
  child(usr(res('g1', '3 matches'))),
  child(asst({ type: 'text', text: 'CHILD SUMMARY' })),
  usr(res('T', HAND_BACK)),
]

const renderDelegation = (messages = delegation) =>
  render(<RoomTranscript messages={messages} keyPrefix="k" showThinking={false} showEmptyHint={false} />)

const expand = () => fireEvent.click(screen.getByTestId('subagent-toggle'))

describe('SubagentBlock', () => {
  it('folds a subagent to one line', () => {
    renderDelegation()
    const block = screen.getByTestId('subagent-block')
    expect(screen.getByTestId('subagent-toggle')).toHaveTextContent('general-purpose · 2 tools')
    expect(screen.getByTestId('subagent-toggle')).toHaveAttribute('aria-expanded', 'false')
    expect(within(block).queryByTestId('subagent-rail')).toBeNull()
    expect(screen.queryByText('Reading the file.')).toBeNull()
    expect(screen.queryByText('CHILD SUMMARY')).toBeNull()
    // Only the Task is an operation at the top level; its child calls are not.
    expect(screen.getAllByTestId('operation-block')).toHaveLength(1)
  })

  it("expands into the child's own blocks", () => {
    renderDelegation()
    expand()
    const rail = screen.getByTestId('subagent-rail')
    expect(within(rail).getByText('Reading the file.')).toBeInTheDocument()
    expect(within(rail).getByText('CHILD SUMMARY')).toBeInTheDocument()
    const names = within(rail).getAllByTestId('op-name').map((n) => n.textContent)
    expect(names).toEqual(['Read', 'Grep'])
    // The child's own call and result are paired inside the rail, like the top level.
    expect(within(rail).getByText('3 matches')).toBeInTheDocument()
  })

  // A4 (spec §5.5): the rail reads the worker theme's rail colour, brighter
  // than the app's generic subtle border.
  it('rail uses the worker theme rail colour', () => {
    renderDelegation()
    expand()
    expect(screen.getByTestId('subagent-rail').className).toContain('border-[var(--wt-rail-color)]')
  })

  it("renders the child's tools one indent deeper", () => {
    renderDelegation()
    expand()
    const [task, read] = screen.getAllByTestId('operation-block')
    const taskRail = within(task).getAllByTestId('op-rail')[0]
    const subRail = within(taskRail).getByTestId('subagent-rail')
    expect(subRail).toHaveAttribute('data-depth', '1')
    // A second rail: the child's block sits inside it, which sits inside the Task's own.
    expect(subRail.contains(read)).toBe(true)
    expect(subRail.className).toMatch(/border-l/)
  })

  it('renders the hand-back as text, not JSON', () => {
    renderDelegation()
    const [task] = screen.getAllByTestId('operation-block')
    expect(within(task).getByText('Found three TODOs in notes.md')).toBeInTheDocument()
    expect(task.textContent).not.toContain('"type"')
    expect(task.textContent).not.toContain('[{')
  })

  it("renders the child's prompt as a subagent line, not the user's", () => {
    renderDelegation()
    expand()
    const line = screen.getByTestId('room-subagent-line')
    expect(line).toHaveTextContent('read notes.md and list the TODOs')
    expect(within(line).queryByTestId('room-user-prefix')).toBeNull()
    // The human's line is still the only user line in the transcript.
    const userLines = screen.getAllByTestId('room-user-line')
    expect(userLines).toHaveLength(1)
    expect(userLines[0]).toHaveTextContent('please delegate')
  })

  it("puts the hand-back after the child's own output", () => {
    renderDelegation()
    expand()
    const summary = screen.getByText('CHILD SUMMARY')
    const handBack = screen.getByText('Found three TODOs in notes.md')
    expect(follows(summary, handBack)).toBe(true)
  })

  it("keeps the subagent's expansion in the pane's fold memory, where expand-all reaches it", () => {
    const { rerender } = renderDelegation()
    fireEvent.click(screen.getByTestId('turn-expand-all'))
    expect(screen.getByTestId('subagent-toggle')).toHaveAttribute('aria-expanded', 'true')
    // A new list (the next frame arriving) re-renders every row; the fold survives.
    rerender(<RoomTranscript messages={[...delegation]} keyPrefix="k" showThinking={false} showEmptyHint={false} />)
    expect(screen.getByTestId('subagent-rail')).toBeInTheDocument()
  })

  it("does not hand a child block's fold to the top-level block at the same position", () => {
    renderDelegation()
    expand()
    // Folds are keyed by message position, so the child's Read (message 3)
    // and the Task (message 1) cannot share one — expanding the child's
    // input must leave the Task's alone.
    const [task, read] = screen.getAllByTestId('operation-block')
    fireEvent.click(within(read).getByTestId('op-input-toggle'))
    expect(within(read).getByTestId('op-input-toggle')).toHaveAttribute('aria-expanded', 'true')
    expect(within(task).getAllByTestId('op-input-toggle')[0]).toHaveAttribute('aria-expanded', 'false')
  })

  it('nests a subagent started by a subagent one rail deeper again', () => {
    renderDelegation([
      asst(call('T', 'Task', { subagent_type: 'outer' })),
      child(asst(call('U', 'Task', { subagent_type: 'inner' }))),
      child(asst({ type: 'text', text: 'INNER PROSE' }), 'U'),
      child(usr(res('U', 'inner done'))),
      usr(res('T', 'outer done')),
    ])
    fireEvent.click(screen.getByTestId('subagent-toggle'))
    const toggles = screen.getAllByTestId('subagent-toggle')
    expect(toggles.map((b) => b.textContent)).toEqual(['outer · 1 tools', 'inner · 0 tools'])
    fireEvent.click(toggles[1])
    const rails = screen.getAllByTestId('subagent-rail')
    expect(rails.map((r) => r.getAttribute('data-depth'))).toEqual(['1', '2'])
    expect(within(rails[1]).getByText('INNER PROSE')).toBeInTheDocument()
  })

  it("does not draw one same-id Task's subagent under the other", () => {
    // Two calls reusing the id `T`: each Task's rail holds its own frames only.
    renderDelegation([
      asst(call('T', 'Task', { subagent_type: 'first' })),
      child(asst({ type: 'text', text: 'FIRST CHILD' })),
      usr(res('T', 'first done')),
      asst(call('T', 'Task', { subagent_type: 'second' })),
      child(asst({ type: 'text', text: 'SECOND CHILD' })),
      usr(res('T', 'second done')),
    ])
    const toggles = screen.getAllByTestId('subagent-toggle')
    expect(toggles).toHaveLength(2)
    toggles.forEach((toggle) => fireEvent.click(toggle))
    const rails = screen.getAllByTestId('subagent-rail')
    expect(rails).toHaveLength(2)
    expect(within(rails[0]).getByText('FIRST CHILD')).toBeInTheDocument()
    expect(within(rails[0]).queryByText('SECOND CHILD')).toBeNull()
    expect(within(rails[1]).getByText('SECOND CHILD')).toBeInTheDocument()
    expect(within(rails[1]).queryByText('FIRST CHILD')).toBeNull()
    expect(screen.getAllByText('FIRST CHILD')).toHaveLength(1)
    expect(screen.getAllByText('SECOND CHILD')).toHaveLength(1)
  })
})

// R4 T3.3 (Q2): a subagent's close-out from its task row — tokens, tools,
// duration, and a status word; never a cost, never a placeholder.
describe('SubagentBlock — task close-out (R4 T3.3)', () => {
  const NOW = 1_800_000_000_000
  const task = (extra: Partial<WorkerTask> = {}): WorkerTask => ({
    task_id: 'k1', turn_id: 't1', kind: 'subagent', task_type: 'local_agent', tool_use_id: 'T', parent_tool_use_id: null,
    description: 'analyse notes.md', backgrounded: false, status: 'running', provider_status: null, closed_by: null,
    started_at: NOW - 12_000, ended_at: null, startSeq: 1, ...extra,
  })
  const done = (status: WorkerTask['status'], usage?: Partial<TaskUsage>) =>
    task({ status, ended_at: NOW, ...(usage ? { usage: usage as TaskUsage } : {}) })
  const withTask = (t: WorkerTask | undefined, messages = delegation, now = NOW) =>
    render(<RoomTranscript messages={messages} keyPrefix="k" showThinking={false} showEmptyHint={false} now={now}
      subagentTasks={t ? new Map([[t.tool_use_id!, t]]) : undefined} />)
  const toggle = () => screen.getByTestId('subagent-toggle')

  it('no task row: exactly as before', () => {
    withTask(undefined)
    expect(toggle()).toHaveTextContent(/^general-purpose · 2 tools$/)
  })

  it('running: the summary adds the elapsed time', () => {
    withTask(task())
    expect(toggle()).toHaveTextContent(/^general-purpose · 2 tools · 12s$/)
    expect(screen.queryByTestId('subagent-status')).toBeNull()
  })

  it('completed: tokens, tools and duration from usage — no cost, no status word', () => {
    withTask(done('completed', { total_tokens: 26_400, tool_uses: 8, duration_ms: 12_300 }))
    expect(toggle()).toHaveTextContent(/^general-purpose · 26k tokens · 8 tools · 12s$/)
    expect(toggle().textContent).not.toMatch(/\$/)
    expect(screen.queryByTestId('subagent-status')).toBeNull()
  })

  it('failed: the same plus a red failed', () => {
    withTask(done('failed', { total_tokens: 900, tool_uses: 1, duration_ms: 3_000 }))
    expect(toggle()).toHaveTextContent(/^general-purpose · 900 tokens · 1 tool · 3s · failed$/)
    expect(screen.getByTestId('subagent-status').className).toMatch(/\btext-status-error\b/)
  })

  it('killed → neutral stopped; lost → neutral interrupted (never red)', () => {
    const { unmount } = withTask(done('killed'))
    expect(toggle()).toHaveTextContent(/^general-purpose · 2 tools · stopped$/)
    expect(screen.getByTestId('subagent-status').className).not.toMatch(/status-error/)
    unmount()
    withTask(done('lost'))
    expect(toggle()).toHaveTextContent(/^general-purpose · 2 tools · interrupted$/)
    expect(screen.getByTestId('subagent-status').className).not.toMatch(/status-error/)
  })

  it('usage partly present: only the parts it has, nothing in place of the rest', () => {
    withTask(done('completed', { total_tokens: 1_500_000 }))
    expect(toggle()).toHaveTextContent(/^general-purpose · 1.5M tokens$/)
  })

  it('a subagent task whose call has no children: the suffix goes on the call\'s own header', () => {
    const noChildren = [usr({ type: 'text', text: 'go' }), asst(call('T', 'Task', { description: 'bg explore', subagent_type: 'Explore' }))]
    withTask(done('completed', { total_tokens: 2_000, tool_uses: 4, duration_ms: 65_000 }), noChildren)
    expect(screen.queryByTestId('subagent-block')).toBeNull()
    const op = screen.getByTestId('operation-block')
    expect(within(op).getByTestId('subagent-task-suffix')).toHaveTextContent(/^· 2k tokens · 4 tools · 1m$/)
  })
})
