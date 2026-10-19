import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('../host-api', () => ({ hostFetch: vi.fn() }))

import { hostFetch } from '../host-api'
import { fetchRenamePreview } from './rename-preview'
import { makePersona } from './__fixtures__/personas'

const reply = (status: number, body: unknown) =>
  ({ ok: status >= 200 && status < 300, status, json: async () => body }) as Response

describe('fetchRenamePreview', () => {
  beforeEach(() => vi.mocked(hostFetch).mockReset())

  it('POSTs every code and the active ones as pickable, and returns would_rename', async () => {
    vi.mocked(hostFetch).mockResolvedValue(reply(200, { would_rename: 3, candidates: 5 }))
    const n = await fetchRenamePreview('h1', [makePersona('hiro'), makePersona('emma', { retired: true })])
    expect(n).toBe(3)
    const [host, path, init] = vi.mocked(hostFetch).mock.calls[0]
    expect([host, path, init!.method]).toEqual(['h1', '/api/peers/rename-preview', 'POST'])
    expect(JSON.parse(init!.body as string)).toEqual({ codes: ['hiro', 'emma'], pickable: ['hiro'] })
  })

  it('keeps 0, which the daemon knows exactly', async () => {
    vi.mocked(hostFetch).mockResolvedValue(reply(200, { would_rename: 0, candidates: 0 }))
    expect(await fetchRenamePreview('h1', [makePersona('hiro')])).toBe(0)
  })

  it.each([400, 404, 405, 500, 503])('is null for a %i, not only a 404', async (status) => {
    vi.mocked(hostFetch).mockResolvedValue(reply(status, { would_rename: 9 }))
    expect(await fetchRenamePreview('h1', [makePersona('hiro')])).toBeNull()
  })

  it('is null when the request fails or the body is not a count', async () => {
    vi.mocked(hostFetch).mockRejectedValueOnce(new Error('offline'))
    expect(await fetchRenamePreview('h1', [makePersona('hiro')])).toBeNull()
    for (const body of [{}, { would_rename: '3' }, { would_rename: -1 }, { would_rename: 1.5 }, null]) {
      vi.mocked(hostFetch).mockResolvedValueOnce(reply(200, body))
      expect(await fetchRenamePreview('h1', [makePersona('hiro')])).toBeNull()
    }
  })
})
