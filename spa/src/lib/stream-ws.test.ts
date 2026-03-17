// spa/src/lib/stream-ws.test.ts
import { describe, it, expect, vi } from 'vitest'
import { parseStreamMessage, type StreamMessage } from './stream-ws'

describe('parseStreamMessage', () => {
  it('parses assistant text message', () => {
    const raw = JSON.stringify({
      type: 'assistant',
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'Hello world' }],
        stop_reason: 'end_turn',
      },
    })
    const msg = parseStreamMessage(raw)
    expect(msg?.type).toBe('assistant')
    if (msg?.type === 'assistant') {
      expect(msg.message.content[0]).toEqual({ type: 'text', text: 'Hello world' })
    }
  })

  it('parses result message', () => {
    const raw = JSON.stringify({
      type: 'result',
      subtype: 'success',
      total_cost_usd: 0.05,
      session_id: 'abc',
    })
    const msg = parseStreamMessage(raw)
    expect(msg?.type).toBe('result')
  })

  it('parses control_request', () => {
    const raw = JSON.stringify({
      type: 'control_request',
      request_id: 'uuid-1',
      request: {
        subtype: 'can_use_tool',
        tool_name: 'Bash',
        input: { command: 'ls' },
        tool_use_id: 'tool-1',
      },
    })
    const msg = parseStreamMessage(raw)
    expect(msg?.type).toBe('control_request')
  })

  it('returns null for invalid JSON', () => {
    expect(parseStreamMessage('not json')).toBeNull()
  })
})
