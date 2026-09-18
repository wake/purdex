import { describe, it, expect } from 'vitest'
import { NexApiError, nexErrorFromResponse, type ExecutionSummary, type NexCapabilities } from './types'

describe('nexErrorFromResponse', () => {
  it('parses the structured {error, code} body', async () => {
    const res = new Response(JSON.stringify({ error: 'somebody holds it', code: 'lease_held' }), { status: 409 })
    const err = await nexErrorFromResponse(res)
    expect(err).toBeInstanceOf(NexApiError)
    expect(err.status).toBe(409)
    expect(err.code).toBe('lease_held')
    expect(err.message).toBe('somebody holds it')
    expect(err.turnId).toBeUndefined()
  })

  it('carries turn_id when the body has one', async () => {
    const res = new Response(JSON.stringify({ error: 'x', code: 'turn_stalled', turn_id: 'trn_1' }), { status: 409 })
    const err = await nexErrorFromResponse(res)
    expect(err.turnId).toBe('trn_1')
  })

  it('falls back to http_<status> for a non-JSON body', async () => {
    const res = new Response('<html>nope</html>', { status: 502 })
    const err = await nexErrorFromResponse(res)
    expect(err.code).toBe('http_502')
    expect(err.message).toContain('502')
  })

  it('falls back to http_<status> for JSON without a code', async () => {
    const res = new Response(JSON.stringify({ message: 'legacy' }), { status: 400 })
    const err = await nexErrorFromResponse(res)
    expect(err.code).toBe('http_400')
  })
})

describe('NexCapabilities typed limit/delegate fields (F1)', () => {
  it('parses the capabilities JSON with brief/origin/labels/delegate into the typed shape', () => {
    const caps = JSON.parse(JSON.stringify({
      phase: 'P1a', host_id: 'mlab', verbs: [], providers: ['claude'], events: [], provider_events: [], transient_events: [],
      sandbox_profiles: ['readonly', 'standard'], sandbox_default_profile: 'standard', sandbox_max_profile: 'standard',
      roots: [{ path: '/w', kind: 'dev' }],
      lease: { ttl_seconds: 120, scope: 'execution', renew: { method: 'POST', path: '/attach/renew' }, release: { method: 'DELETE', path: '/attach' } },
      send: { delivery: ['queued'], max_text_bytes: 65536 },
      brief: { max_bytes: 65536 },
      origin: { max_bytes: 2048 },
      labels: { max_count: 32, max_key_bytes: 64, max_value_bytes: 256, max_total_bytes: 4096, reserved_prefix: 'nex.' },
      delegate: { resume_session_id: true },
      future_field: { x: 1 },
    })) as NexCapabilities
    expect(caps.brief?.max_bytes).toBe(65536)
    expect(caps.origin?.max_bytes).toBe(2048)
    expect(caps.labels).toEqual({ max_count: 32, max_key_bytes: 64, max_value_bytes: 256, max_total_bytes: 4096, reserved_prefix: 'nex.' })
    expect(caps.delegate?.resume_session_id).toBe(true)
    expect(caps.future_field).toEqual({ x: 1 })
  })

  it('leaves the new fields optional for an older daemon that omits them', () => {
    const caps = { phase: 'P1a' } as Partial<NexCapabilities>
    expect(caps.delegate?.resume_session_id).toBeUndefined()
    expect(caps.brief).toBeUndefined()
  })

  it('P-B3 §4.5: tool_events {output_max_bytes, diff_max_lines} type-checks and stays optional', () => {
    const withCaps = { phase: 'P1a', tool_events: { output_max_bytes: 8192, diff_max_lines: 2000 } } as Partial<NexCapabilities>
    expect(withCaps.tool_events).toEqual({ output_max_bytes: 8192, diff_max_lines: 2000 })
    const bytes: number | undefined = withCaps.tool_events?.output_max_bytes
    expect(bytes).toBe(8192)
    const older = { phase: 'P1a' } as Partial<NexCapabilities>
    expect(older.tool_events).toBeUndefined()
  })

  it('ExecutionSummary carries resume_session_id / requested_profile / effective_profile', () => {
    const row = { id: 'exc_1', state: 'idle', resume_session_id: 'sid-1', requested_profile: 'handoff', effective_profile: 'trusted' } as Partial<ExecutionSummary>
    expect(row.resume_session_id).toBe('sid-1')
    expect(row.requested_profile).toBe('handoff')
    expect(row.effective_profile).toBe('trusted')
  })
})
