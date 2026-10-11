// U4-4a: what the worker's Nexen events say runs now: the tool, and the background work.
import { describe, it, expect } from 'vitest'
import { runningToolName, workerBackground } from './worker-live'
import type { ToolActivity } from './tool-activity'
import type { WorkerTask } from './types'

const tool = (name: string, startedAt: number, status: ToolActivity['status'] = 'running'): ToolActivity => ({ name, startedAt, endedAt: status === 'running' ? null : startedAt + 1, status })
const task = (id: string, kind: WorkerTask['kind'], over: Partial<WorkerTask> = {}): WorkerTask => ({
  task_id: id, turn_id: 't', kind, task_type: 'x', tool_use_id: null, parent_tool_use_id: null, description: '', backgrounded: true,
  status: 'running', provider_status: null, closed_by: null, started_at: 1, ended_at: null, startSeq: 1, ...over,
}) as WorkerTask

describe('runningToolName', () => {
  it('is null when nothing runs (the model is thinking)', () => {
    expect(runningToolName({})).toBeNull()
    expect(runningToolName({ a: tool('Bash', 1, 'done'), b: tool('Edit', 2, 'error') })).toBeNull()
  })
  it('is the tool that started last among those still running', () => {
    expect(runningToolName({ a: tool('Bash', 1), b: tool('Read', 5), c: tool('Edit', 3, 'done') })).toBe('Read')
  })
  it('does not name the tool that starts a subagent (the deck counts subagents)', () => {
    expect(runningToolName({ a: tool('Task', 9), b: tool('Bash', 2) })).toBe('Bash')
    expect(runningToolName({ a: tool('Task', 9) })).toBeNull()
  })
})

describe('workerBackground', () => {
  it('is undefined when no background task runs', () => {
    expect(workerBackground({})).toBeUndefined()
    expect(workerBackground({ a: task('a', 'shell', { status: 'completed' }) })).toBeUndefined()
  })
  it('counts the running background tasks by kind, in the header\'s shape; a kind with none is left out', () => {
    expect(workerBackground({ a: task('a', 'shell'), b: task('b', 'shell'), c: task('c', 'subagent'), d: task('d', 'other', { status: 'killed' }) })).toEqual({ shell: 2, subagent: 1 })
  })
  it('a task that never went to the background (a foreground command) is not background work', () => {
    expect(workerBackground({ a: task('a', 'shell', { backgrounded: false }) })).toBeUndefined()
  })
})
