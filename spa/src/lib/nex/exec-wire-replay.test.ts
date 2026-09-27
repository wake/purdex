// spa/src/lib/nex/exec-wire-replay.test.ts — golden replay of a real CC wire
// sample through the transient + durable reducers.
import { describe, it, expect } from 'vitest'
import { applyTransientFrame } from './partial'
import { applyDurableEvent, defaultExecutionState } from './event-reducer'
import wireSample from './__fixtures__/cc-2.1.275-sleep6.jsonl?raw'
import wireWithTasks from './__fixtures__/cc-2.1.275-sleep6-tasks.jsonl?raw'
import { groupTurns } from './turns'

describe('exec wire replay', () => {
  const MSG = 'msg_011Cf9fLxdWgh5jkA2b9MjXt'
  const TOOL = 'toolu_01CyoH2VpvKrWq9hjjeBX6uM'

  it('golden: replaying the CC 2.1.275 sleep-6 wire sample ends with 10 messages, no partial, Bash done', () => {
    const lines = wireSample.split('\n').filter((l) => l.trim() !== '')
    expect(lines).toHaveLength(29)
    const frames = lines.map((l) => JSON.parse(l) as Record<string, unknown>)
    let s = defaultExecutionState()
    let seq = 0
    let expectedJson = ''
    frames.forEach((frame, i) => {
      const line = i + 1
      if (frame.type === 'stream_event') {
        const event = frame.event as { type: string; delta?: { type: string; partial_json?: string } }
        if (event.delta?.type === 'input_json_delta') expectedJson += event.delta.partial_json ?? ''
        s = applyTransientFrame(s, 'stream_event', frame)
      } else {
        seq += 1
        s = applyDurableEvent(s, { seq, execution_id: 'exc_1', kind: frame.type as string, payload: frame, created_at: line * 100 })
      }
      if (line === 13) {
        expect(expectedJson).toBe('{"command": "sleep 6 && echo ok", "description": "Sleep 6 seconds then echo ok"}')
        expect(s.partial?.blocks[0].partialJson).toBe(expectedJson)
        expect(s.partial?.blocks[0].toolName).toBe('Bash')
        expect(s.turnLive).toBe(true)
      }
      if (line === 14) expect(s.partial).toEqual({ messageId: MSG, finalized: 1, blocks: {} })
      if (line === 20) expect(s.tools[TOOL]).toMatchObject({ status: 'done', startedAt: 1400, endedAt: 2000 })
      if (line === 24) expect(s.partial?.blocks[0]).toMatchObject({ type: 'text', text: 'done' })
    })
    expect(seq).toBe(10)
    expect(s.messages).toHaveLength(10)
    const counts: Record<string, number> = {}
    for (const m of s.messages) counts[m.type] = (counts[m.type] ?? 0) + 1
    expect(counts).toEqual({ system: 5, assistant: 2, user: 1, result: 1, rate_limit_event: 1 })
    expect(s.partial).toBeNull()
    expect(s.turnLive).toBe(false)
    expect(s.lastSeq).toBe(10)
    expect(s.pendingSend).toBe(false)
    const bash = s.tools[TOOL]
    expect(bash.status).toBe('done')
    expect(bash.name).toBe('Bash')
    expect(bash.endedAt).not.toBeNull()
    expect(bash.endedAt as number).toBeGreaterThan(bash.startedAt)
  })
})

// The same wire with nexen v0.13's `task_start` / `task_end` interleaved
// where the daemon writes them (same transaction as the raw task_started /
// task_notification system frames). Hand-written from the contract payloads
// (capability-matrix §3); `type` carries the kind like every other line and
// is stripped from the payload for the two task kinds.
describe('exec wire replay with task events (nexen v0.13)', () => {
  function replay(raw: string): { s: ReturnType<typeof defaultExecutionState>; durable: number; lastNonTaskSeq: number } {
    const frames = raw.split('\n').filter((l) => l.trim() !== '').map((l) => JSON.parse(l) as Record<string, unknown>)
    // One daemon-declared turn in front, so the turn grouping has a boundary to keep.
    let s = applyDurableEvent(defaultExecutionState(), { seq: 1, execution_id: 'exc_1', kind: 'execution.delegated', payload: { brief: 'sleep 6' }, created_at: 50 })
    let seq = 1
    let lastNonTaskSeq = 1
    // Timestamps follow the original wire's line numbers, so both replays
    // stamp the shared frames identically.
    let line = 0
    frames.forEach((frame) => {
      const kind = frame.type as string
      const isTask = kind === 'task_start' || kind === 'task_end'
      if (!isTask) line += 1
      if (kind === 'stream_event') {
        s = applyTransientFrame(s, 'stream_event', frame)
        return
      }
      seq += 1
      if (!isTask) lastNonTaskSeq = seq
      const { type: _t, ...rest } = frame
      s = applyDurableEvent(s, { seq, execution_id: 'exc_1', kind, payload: isTask ? rest : frame, created_at: line * 100 })
    })
    return { s, durable: seq, lastNonTaskSeq }
  }

  it('task events never reach messages, turns or the partial; the table ends with one completed row', () => {
    const plain = replay(wireSample)
    const withTasks = replay(wireWithTasks)
    expect(withTasks.durable).toBe(plain.durable + 2)
    // lastSeq tracks non-task events only (task events never move it).
    expect(withTasks.s.lastSeq).toBe(withTasks.lastNonTaskSeq)
    expect(withTasks.s.lastSeq).toBe(plain.s.lastSeq + 2)
    expect(withTasks.s.messages).toEqual(plain.s.messages)
    expect(withTasks.s.turnStarts).toEqual(plain.s.turnStarts)
    const turnsWith = groupTurns(withTasks.s.messages, withTasks.s.turnStarts)
    expect(turnsWith).toHaveLength(groupTurns(plain.s.messages, plain.s.turnStarts).length)
    expect(turnsWith).toHaveLength(1)
    expect(withTasks.s.partial).toEqual(plain.s.partial)
    expect(withTasks.s.turnLive).toBe(plain.s.turnLive)
    expect(withTasks.s.pendingSend).toBe(plain.s.pendingSend)
    expect(withTasks.s.tools).toEqual(plain.s.tools)
    expect(plain.s.tasks).toEqual({})
    expect(Object.values(withTasks.s.tasks)).toHaveLength(1)
    expect(withTasks.s.tasks.bdynj1509).toMatchObject({
      status: 'completed',
      kind: 'shell',
      tool_use_id: 'toolu_01CyoH2VpvKrWq9hjjeBX6uM',
      description: 'Sleep 6 seconds then echo ok',
      command: 'sleep 6 && echo ok',
      closed_by: 'provider',
      // seq 1 delegated, 2–4 the opening system / rate-limit frames, 5 the
      // Bash assistant, 6 the raw task_started, 7 our task_start.
      startSeq: 7,
    })
  })
})
