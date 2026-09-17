// spa/src/lib/nex/exec-wire-replay.test.ts — golden replay of a real CC wire
// sample through the transient + durable reducers.
import { describe, it, expect } from 'vitest'
import { applyTransientFrame } from './partial'
import { applyDurableEvent, defaultExecutionState } from './event-reducer'
import wireSample from './__fixtures__/cc-2.1.275-sleep6.jsonl?raw'

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
