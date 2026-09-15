import { describe, it, expect } from 'vitest'
import { NexApiError, nexErrorFromResponse } from './types'

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
