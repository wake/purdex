import { describe, it, expect, vi, beforeEach } from 'vitest'

const send = vi.fn()
vi.mock('./approval-api', () => ({ send: (...a: unknown[]) => send(...a) }))
import { getCompaction, putCompaction } from './compaction-api'

beforeEach(() => { send.mockReset(); send.mockResolvedValue({}) })

describe('compaction-api', () => {
  it('GET goes to the session path, encoded', async () => {
    await getCompaction('h1', 'a/b')
    expect(send).toHaveBeenCalledWith('h1', '/api/team/compaction/a%2Fb', { method: 'GET' })
  })
  it('PUT sends the whole override as JSON', async () => {
    await putCompaction('h1', 'S1', { auto: null, threshold: 50, idle: false })
    const [host, p, init] = send.mock.calls[0]
    expect(host).toBe('h1'); expect(p).toBe('/api/team/compaction/S1')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(init.body)).toEqual({ auto: null, threshold: 50, idle: false })
  })
})
