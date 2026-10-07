// spa/src/lib/nex/tasks.test.ts
import { describe, it, expect } from 'vitest'
import { anyRunningSubagent, anyRunningTask, applyTaskEvent, applyTaskSnapshot, parseTask, runningTasks, subagentTasksByToolUse, type TaskTable } from './tasks'

// Payloads as the contract prints them (capability-matrix §3 "task_start／task_end 的 payload").
const start = (over: Record<string, unknown> = {}): Record<string, unknown> => ({
  task_id: 'bkdw11ap1',
  turn_id: 'turn_1',
  kind: 'shell',
  task_type: 'local_bash',
  tool_use_id: 'toolu_01',
  parent_tool_use_id: null,
  description: 'Run sleep 8',
  command: 'sleep 8; echo done',
  backgrounded: true,
  started_at: 1790500501000,
  ...over,
})

const end = (over: Record<string, unknown> = {}): Record<string, unknown> => ({
  task_id: 'bkdw11ap1',
  turn_id: 'turn_1',
  kind: 'shell',
  tool_use_id: 'toolu_01',
  status: 'completed',
  provider_status: 'completed',
  ended_at: 1790500509042,
  closed_by: 'provider',
  summary: 'Background command completed (exit code 0)',
  usage: { total_tokens: 26171, tool_uses: 0, duration_ms: 2149 },
  cost_usd: null,
  ...over,
})

describe('parseTask', () => {
  it('parses a task_start payload as a running row', () => {
    expect(parseTask(start(), 7)).toEqual({
      task_id: 'bkdw11ap1',
      turn_id: 'turn_1',
      kind: 'shell',
      task_type: 'local_bash',
      tool_use_id: 'toolu_01',
      parent_tool_use_id: null,
      description: 'Run sleep 8',
      command: 'sleep 8; echo done',
      backgrounded: true,
      status: 'running',
      provider_status: null,
      closed_by: null,
      started_at: 1790500501000,
      ended_at: null,
      startSeq: 7,
    })
  })

  it('rejects a payload without a usable task_id', () => {
    expect(parseTask(start({ task_id: undefined }), 1)).toBeNull()
    expect(parseTask(start({ task_id: '' }), 1)).toBeNull()
    expect(parseTask(start({ task_id: 3 }), 1)).toBeNull()
    expect(parseTask(null, 1)).toBeNull()
    expect(parseTask([start()], 1)).toBeNull()
  })

  it('unknown kind → other; unknown terminal status → failed (fail-closed)', () => {
    expect(parseTask(start({ kind: 'daemon_thing' }), 1)?.kind).toBe('other')
    expect(parseTask(start({ kind: 42 }), 1)?.kind).toBe('other')
    expect(parseTask(end({ status: 'exploded' }), 1)?.status).toBe('failed')
    expect(parseTask(end({ status: 7 }), 1)?.status).toBe('failed')
    expect(parseTask(end({ status: 'lost', closed_by: 'daemon', provider_status: null }), 1)).toMatchObject({ status: 'lost', closed_by: 'daemon', provider_status: null })
  })

  it('missing tool_use_id / parent_tool_use_id are null, never undefined', () => {
    const t = parseTask(start({ tool_use_id: undefined, parent_tool_use_id: undefined }), 1)
    expect(t?.tool_use_id).toBeNull()
    expect(t?.parent_tool_use_id).toBeNull()
    expect(parseTask(start({ tool_use_id: '' }), 1)?.tool_use_id).toBeNull()
  })

  it('never carries cost_usd (subagent_cost: false)', () => {
    const t = parseTask(end({ cost_usd: 1.23 }), 1) as unknown as Record<string, unknown>
    expect('cost_usd' in t).toBe(false)
  })

  it('guards optional fields: bad usage / summary / command / subagent_type are dropped', () => {
    const t = parseTask({ ...end({ summary: 5, usage: { total_tokens: 'x' } }), command: 9, subagent_type: false }, 1)
    expect(t).not.toBeNull()
    expect(t && 'summary' in t).toBe(false)
    expect(t && 'usage' in t).toBe(false)
    expect(t && 'command' in t).toBe(false)
    expect(t && 'subagent_type' in t).toBe(false)
    expect(parseTask(start({ kind: 'subagent', task_type: 'local_agent', subagent_type: 'general-purpose', command: undefined }), 1))
      .toMatchObject({ kind: 'subagent', subagent_type: 'general-purpose' })
  })

  it('non-finite timestamps become null; backgrounded is a real boolean only', () => {
    const t = parseTask(start({ started_at: 'soon', backgrounded: 'yes' }), 1)
    expect(t?.started_at).toBeNull()
    expect(t?.backgrounded).toBe(false)
  })

  it('parses a /tasks item (merged shape) with its own status', () => {
    const item = { ...start(), status: 'running', provider_status: null, closed_by: null, ended_at: null, cost_usd: null }
    expect(parseTask(item, 128)).toMatchObject({ status: 'running', startSeq: 128, command: 'sleep 8; echo done' })
    const closed = { ...start(), ...end() }
    expect(parseTask(closed, 128)).toMatchObject({ status: 'completed', description: 'Run sleep 8', ended_at: 1790500509042 })
  })
})

describe('applyTaskEvent', () => {
  it('a replay that changes nothing returns the same table (running start, closed end with usage)', () => {
    const running = applyTaskEvent({}, 'task_start', start(), 5)
    expect(applyTaskEvent(running, 'task_start', start(), 5)).toBe(running)
    expect(applyTaskEvent(running, 'task_start', start(), 7)).toBe(running)
    const closed = applyTaskEvent(running, 'task_end', end(), 9)
    expect(applyTaskEvent(closed, 'task_end', end(), 9)).toBe(closed)
    expect(applyTaskEvent(closed, 'task_start', start(), 5)).toBe(closed)
    // A differing end still replaces the end state wholesale.
    const changed = applyTaskEvent(closed, 'task_end', end({ usage: { total_tokens: 1, tool_uses: 0, duration_ms: 2149 } }), 9)
    expect(changed).not.toBe(closed)
    expect(changed.bkdw11ap1.usage?.total_tokens).toBe(1)
    const noSummary = applyTaskEvent(closed, 'task_end', end({ summary: undefined }), 9)
    expect(noSummary).not.toBe(closed)
    expect(noSummary.bkdw11ap1.summary).toBeUndefined()
  })

  it('start then end: one row, closed with the end facts and the start facts kept', () => {
    let t: TaskTable = {}
    t = applyTaskEvent(t, 'task_start', start(), 5)
    expect(t.bkdw11ap1.status).toBe('running')
    t = applyTaskEvent(t, 'task_end', end(), 9)
    expect(Object.keys(t)).toEqual(['bkdw11ap1'])
    expect(t.bkdw11ap1).toMatchObject({
      status: 'completed',
      provider_status: 'completed',
      closed_by: 'provider',
      ended_at: 1790500509042,
      summary: 'Background command completed (exit code 0)',
      usage: { total_tokens: 26171, tool_uses: 0, duration_ms: 2149 },
      // task_end does not carry these; the row keeps what task_start said.
      description: 'Run sleep 8',
      command: 'sleep 8; echo done',
      task_type: 'local_bash',
      started_at: 1790500501000,
      startSeq: 5,
    })
  })

  it('end then a replayed start: the row stays closed', () => {
    let t: TaskTable = {}
    t = applyTaskEvent(t, 'task_start', start(), 5)
    t = applyTaskEvent(t, 'task_end', end(), 9)
    const closed = t
    t = applyTaskEvent(t, 'task_start', start(), 12)
    expect(t).toBe(closed)
    expect(t.bkdw11ap1.status).toBe('completed')
  })

  it('an end with no prior start still yields a closed row', () => {
    const t = applyTaskEvent({}, 'task_end', end({ status: 'killed', provider_status: 'stopped' }), 3)
    expect(t.bkdw11ap1).toMatchObject({ status: 'killed', provider_status: 'stopped', description: '', started_at: null, startSeq: 3 })
  })

  it('a duplicate end is idempotent', () => {
    let t: TaskTable = {}
    t = applyTaskEvent(t, 'task_start', start(), 5)
    t = applyTaskEvent(t, 'task_end', end(), 9)
    const once = t
    t = applyTaskEvent(t, 'task_end', end(), 9)
    expect(t).toEqual(once)
  })

  it('task_end replaces the end state wholesale: a second end without summary/usage drops them', () => {
    let t: TaskTable = {}
    t = applyTaskEvent(t, 'task_start', start(), 5)
    t = applyTaskEvent(t, 'task_end', end(), 9)
    t = applyTaskEvent(t, 'task_end', end({ summary: undefined, usage: undefined, status: 'failed' }), 10)
    expect(t.bkdw11ap1.status).toBe('failed')
    expect('summary' in t.bkdw11ap1).toBe(false)
    expect('usage' in t.bkdw11ap1).toBe(false)
  })

  it('an end that claims running is still a close (failed)', () => {
    const t = applyTaskEvent({}, 'task_end', end({ status: 'running' }), 3)
    expect(t.bkdw11ap1.status).toBe('failed')
  })

  it('a duplicate start while running keeps the first startSeq', () => {
    let t: TaskTable = {}
    t = applyTaskEvent(t, 'task_start', start(), 5)
    t = applyTaskEvent(t, 'task_start', start(), 8)
    expect(t.bkdw11ap1.startSeq).toBe(5)
  })

  it('invalid payloads and other kinds leave the table untouched', () => {
    const t: TaskTable = {}
    expect(applyTaskEvent(t, 'task_start', { nope: 1 }, 1)).toBe(t)
    expect(applyTaskEvent(t, 'task_end', { task_id: '' }, 1)).toBe(t)
    expect(applyTaskEvent(t, 'assistant', start(), 1)).toBe(t)
  })
})

describe('applyTaskSnapshot', () => {
  const run = (id: string, seq: number) => parseTask(start({ task_id: id }), seq)!

  it('a running row the snapshot omits is dropped only if it started at or before the cursor', () => {
    const t: TaskTable = { old: run('old', 90), atCursor: run('atCursor', 100), fresh: run('fresh', 105) }
    const next = applyTaskSnapshot(t, [], 100)
    expect(Object.keys(next).sort()).toEqual(['fresh'])
  })

  it('closed rows the snapshot omits are kept', () => {
    let t: TaskTable = applyTaskEvent({}, 'task_start', start(), 5)
    t = applyTaskEvent(t, 'task_end', end(), 9)
    expect(applyTaskSnapshot(t, [], 100).bkdw11ap1.status).toBe('completed')
  })

  it('a snapshot that still says running never reopens a closed row', () => {
    let t: TaskTable = applyTaskEvent({}, 'task_start', start(), 5)
    t = applyTaskEvent(t, 'task_end', end(), 120)
    const next = applyTaskSnapshot(t, [run('bkdw11ap1', 100)], 100)
    expect(next.bkdw11ap1.status).toBe('completed')
  })

  it('a snapshot row that closed a running row wins, keeping the row’s startSeq', () => {
    const t: TaskTable = applyTaskEvent({}, 'task_start', start(), 5)
    const closed = parseTask({ ...start(), ...end({ status: 'lost', closed_by: 'daemon', provider_status: null }) }, 100)!
    const next = applyTaskSnapshot(t, [closed], 100)
    expect(next.bkdw11ap1).toMatchObject({ status: 'lost', closed_by: 'daemon', startSeq: 5 })
  })

  it('rows first seen in the snapshot get startSeq = cursor', () => {
    const next = applyTaskSnapshot({}, [run('new', 0)], 77)
    expect(next.new).toMatchObject({ status: 'running', startSeq: 77 })
  })
})

describe('runningTasks', () => {
  it('lists running rows in start order', () => {
    let t: TaskTable = {}
    t = applyTaskEvent(t, 'task_start', start({ task_id: 'b', started_at: 20 }), 2)
    t = applyTaskEvent(t, 'task_start', start({ task_id: 'a', started_at: 10 }), 1)
    t = applyTaskEvent(t, 'task_start', start({ task_id: 'c', started_at: 30 }), 3)
    t = applyTaskEvent(t, 'task_end', end({ task_id: 'c' }), 4)
    expect(runningTasks(t).map((r) => r.task_id)).toEqual(['a', 'b'])
    expect(runningTasks({})).toEqual([])
  })
})

describe('prototype-named task ids (A3)', () => {
  const ids = ['__proto__', 'constructor', 'toString']

  it('applyTaskEvent treats them as ordinary own rows', () => {
    for (const id of ids) {
      let t: TaskTable = {}
      t = applyTaskEvent(t, 'task_start', start({ task_id: id }), 3)
      expect(Object.hasOwn(t, id)).toBe(true)
      expect(Object.getPrototypeOf(t)).toBe(Object.prototype)
      expect(t[id]).toMatchObject({ task_id: id, status: 'running', startSeq: 3 })
      expect(runningTasks(t).map((r) => r.task_id)).toEqual([id])
      t = applyTaskEvent(t, 'task_end', end({ task_id: id }), 4)
      expect(t[id]).toMatchObject({ status: 'completed', description: 'Run sleep 8', startSeq: 3 })
      expect(runningTasks(t)).toEqual([])
    }
  })

  it('a task_end for a prototype-named id with no start does not read inherited props', () => {
    const t = applyTaskEvent({}, 'task_end', end({ task_id: 'constructor' }), 4)
    expect(Object.hasOwn(t, 'constructor')).toBe(true)
    expect(t.constructor).toMatchObject({ task_id: 'constructor', status: 'completed', startSeq: 4 })
  })

  it('applyTaskSnapshot creates own rows and keeps the table an ordinary object', () => {
    const items = ids.map((id, i) => parseTask(start({ task_id: id, started_at: 100 - i }), 0)!)
    const t = applyTaskSnapshot({}, items, 20)
    expect(Object.getPrototypeOf(t)).toBe(Object.prototype)
    for (const id of ids) {
      expect(Object.hasOwn(t, id)).toBe(true)
      expect(t[id]).toMatchObject({ task_id: id, status: 'running', startSeq: 20 })
    }
    expect(runningTasks(t).map((r) => r.task_id)).toEqual(['toString', 'constructor', '__proto__'])
    // A closed prototype-named row stays closed through a later snapshot.
    const closed = applyTaskEvent(t, 'task_end', end({ task_id: '__proto__' }), 30)
    const again = applyTaskSnapshot(closed, [parseTask(start({ task_id: '__proto__' }), 0)!], 25)
    expect(Object.hasOwn(again, '__proto__')).toBe(true)
    expect(again['__proto__']).toMatchObject({ status: 'completed' })
    expect(Object.getPrototypeOf(again)).toBe(Object.prototype)
  })
})

describe('subagentTasksByToolUse (R4 T3.3)', () => {
  it('maps subagent rows by the Task call id; shells and id-less rows are left out', () => {
    let t: TaskTable = {}
    t = applyTaskEvent(t, 'task_start', start({ task_id: 'a', kind: 'subagent', tool_use_id: 'T1' }), 1)
    t = applyTaskEvent(t, 'task_start', start({ task_id: 'b', kind: 'shell', tool_use_id: 'B1' }), 2)
    t = applyTaskEvent(t, 'task_start', start({ task_id: 'c', kind: 'subagent', tool_use_id: null }), 3)
    const m = subagentTasksByToolUse(t)
    expect([...m.keys()]).toEqual(['T1'])
    expect(m.get('T1')?.task_id).toBe('a')
  })

  it('anyRunningSubagent says whether a subagent row is still running', () => {
    let t: TaskTable = {}
    expect(anyRunningSubagent(t)).toBe(false)
    t = applyTaskEvent(t, 'task_start', start({ task_id: 'b', kind: 'shell' }), 1)
    expect(anyRunningSubagent(t)).toBe(false)
    t = applyTaskEvent(t, 'task_start', start({ task_id: 'a', kind: 'subagent', tool_use_id: 'T1' }), 2)
    expect(anyRunningSubagent(t)).toBe(true)
  })
})

describe('anyRunningTask', () => {
  it('true for any running kind, false once all closed', () => {
    let t: TaskTable = {}
    expect(anyRunningTask(t)).toBe(false)
    t = applyTaskEvent(t, 'task_start', start({ task_id: 'a', kind: 'shell' }), 1)
    expect(anyRunningTask(t)).toBe(true)
    t = applyTaskEvent(t, 'task_end', { task_id: 'a', status: 'completed' }, 2)
    expect(anyRunningTask(t)).toBe(false)
  })
})
