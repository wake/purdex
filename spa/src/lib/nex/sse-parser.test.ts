import { describe, it, expect } from 'vitest'
import { SseParser } from './sse-parser'

describe('SseParser', () => {
  it('parses a durable frame with id/event/data', () => {
    const p = new SseParser()
    const frames = p.push('id: 42\nevent: assistant\ndata: {"type":"assistant"}\n\n')
    expect(frames).toEqual([{ id: '42', event: 'assistant', data: '{"type":"assistant"}' }])
  })

  it('reports a transient frame (no id line) with id: null', () => {
    const p = new SseParser()
    const frames = p.push('event: stream_event\ndata: {"x":1}\n\n')
    expect(frames).toEqual([{ id: null, event: 'stream_event', data: '{"x":1}' }])
  })

  it('reassembles frames split across chunks and joins multi-line data', () => {
    const p = new SseParser()
    expect(p.push('id: 1\nev')).toEqual([])
    expect(p.push('ent: user\ndata: a\ndata: b\n')).toEqual([])
    expect(p.push('\n')).toEqual([{ id: '1', event: 'user', data: 'a\nb' }])
  })

  it('ignores comment keepalives and tolerates CRLF', () => {
    const p = new SseParser()
    expect(p.push(': keepalive\r\n\r\n')).toEqual([])
    expect(p.push('id: 2\r\nevent: result\r\ndata: {}\r\n\r\n')).toEqual([{ id: '2', event: 'result', data: '{}' }])
  })

  it('does not carry the previous frame id into the next frame', () => {
    const p = new SseParser()
    p.push('id: 5\nevent: a\ndata: 1\n\n')
    expect(p.push('event: b\ndata: 2\n\n')).toEqual([{ id: null, event: 'b', data: '2' }])
  })

  it('defaults event to "message" and skips frames with no data', () => {
    const p = new SseParser()
    expect(p.push('data: hi\n\n')).toEqual([{ id: null, event: 'message', data: 'hi' }])
    expect(p.push('event: nothing\n\n')).toEqual([])
  })
})
