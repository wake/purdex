import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('../host-api', () => ({ hostFetch: vi.fn() }))
vi.mock('../../stores/useHostStore', () => ({
  useHostStore: { getState: () => ({ hosts: { h1: { id: 'h1' } } }) },
}))

import { hostFetch } from '../host-api'
import { fetchPersonas, putPersonas, PersonaConflictError, PersonaApiError } from './persona-api'
import { makeCollection, makePersona } from './__fixtures__/personas'

const json = (status: number, body: unknown) =>
  ({ ok: status >= 200 && status < 300, status, statusText: '', json: async () => body, text: async () => JSON.stringify(body) }) as Response

describe('persona-api', () => {
  beforeEach(() => vi.mocked(hostFetch).mockReset())

  it('GETs /api/hostconfig/personas', async () => {
    const c = makeCollection(['hiro'])
    vi.mocked(hostFetch).mockResolvedValue(json(200, c))
    expect(await fetchPersonas('h1')).toEqual(c)
    expect(vi.mocked(hostFetch).mock.calls[0][1]).toBe('/api/hostconfig/personas')
  })

  it('refuses an unknown host without touching the network', async () => {
    await expect(fetchPersonas('nope')).rejects.toBeInstanceOf(PersonaApiError)
    expect(hostFetch).not.toHaveBeenCalled()
  })

  it('PUTs {items, baseRevision}', async () => {
    vi.mocked(hostFetch).mockResolvedValue(json(200, { revision: 4 }))
    const items = [makePersona('hiro')]
    expect(await putPersonas('h1', items, 3)).toEqual({ revision: 4 })
    const init = vi.mocked(hostFetch).mock.calls[0][2]!
    expect(init.method).toBe('PUT')
    expect(JSON.parse(init.body as string)).toEqual({ items, baseRevision: 3 })
  })

  it('carries the current copy on 409', async () => {
    const current = makeCollection(['emma'], { at: 9, host: 'h2' }, 7)
    vi.mocked(hostFetch).mockResolvedValue(json(409, current))
    const err = await putPersonas('h1', [], 3).catch((e) => e)
    expect(err).toBeInstanceOf(PersonaConflictError)
    expect((err as PersonaConflictError).current).toEqual(current)
  })

  it('reports the status of a 400', async () => {
    vi.mocked(hostFetch).mockResolvedValue(json(400, { error: 'too long', index: 2, field: 'name' }))
    const err = await putPersonas('h1', [], 3).catch((e) => e)
    expect(err).toBeInstanceOf(PersonaApiError)
    expect((err as PersonaApiError).status).toBe(400)
  })
})
