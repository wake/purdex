import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./persona-api', async (orig) => ({ ...(await orig<typeof import('./persona-api')>()), putPersonas: vi.fn(), fetchPersonas: vi.fn() }))
vi.mock('./persona-loader', () => ({ personasSupported: vi.fn() }))

import { putPersonas, fetchPersonas, PersonaApiError } from './persona-api'
import { personasSupported } from './persona-loader'
import { useHostStore } from '../../stores/useHostStore'
import { mirrorPersonas } from './persona-mirror'
import { makeCollection, makePersona } from './__fixtures__/personas'

const connected = (ids: string[]) =>
  useHostStore.setState({ runtime: Object.fromEntries(ids.map((id) => [id, { status: 'connected' }])) as never })

describe('mirrorPersonas', () => {
  const items = [makePersona('hiro')]

  beforeEach(() => {
    vi.mocked(putPersonas).mockReset().mockResolvedValue({ revision: 1 })
    vi.mocked(fetchPersonas).mockReset().mockImplementation(async () => makeCollection(['hiro'], { at: 1, host: 'x' }, 4))
    vi.mocked(personasSupported).mockReset().mockReturnValue(true)
    connected(['src', 'h2', 'h3'])
  })

  it('writes the same items to every other connected host that declares personas.v1', async () => {
    vi.mocked(personasSupported).mockImplementation((id) => id !== 'h3')
    const out = await mirrorPersonas('src', items, 5)
    expect(putPersonas).toHaveBeenCalledTimes(1)
    expect(putPersonas).toHaveBeenCalledWith('h2', items, 4)
    expect(out.report.mirrored).toEqual(['h2'])
  })

  it('leaves out a host that is not connected', async () => {
    connected(['src'])
    const out = await mirrorPersonas('src', items, 5)
    expect(putPersonas).not.toHaveBeenCalled()
    expect(out.report.mirrored).toEqual([])
  })

  it('takes a role the source lacks from a rejecting host, writes it to the source, then mirrors again', async () => {
    const extra = makePersona('olga')
    let h2Puts = 0
    vi.mocked(fetchPersonas).mockImplementation(async (id) => ({ ...makeCollection(['hiro'], { at: 1, host: id }, 4), items: id === 'h2' ? [...items, extra] : items }))
    vi.mocked(putPersonas).mockImplementation(async (id, put) => {
      if (id === 'h2' && h2Puts++ === 0) throw new PersonaApiError(400, 'a stored code was removed', { index: -1 })
      return { revision: put.length + 10 }
    })
    const out = await mirrorPersonas('src', items, 5)
    expect(out.report.pulled).toEqual(['olga'])
    expect(putPersonas).toHaveBeenCalledWith('src', [...items, extra], 5)
    expect(putPersonas).toHaveBeenCalledWith('h2', [...items, extra], 4)
    expect(putPersonas).toHaveBeenCalledWith('h3', [...items, extra], 4)
    expect(out.items.map((p) => p.code)).toEqual(['hiro', 'olga'])
    expect(out.report.mirrored.sort()).toEqual(['h2', 'h3'])
  })

  it('does not write a host when the caller aborts after the read', async () => {
    const out = await mirrorPersonas('src', items, 5, undefined, () => true)
    expect(putPersonas).not.toHaveBeenCalled()
    expect(out.report.mirrored).toEqual([])
    expect(out.report.failed.map((f) => f.hostId).sort()).toEqual(['h2', 'h3'])
  })

  it('reports a host that cannot be written, and still mirrors the others', async () => {
    vi.mocked(putPersonas).mockImplementation(async (id) => {
      if (id === 'h2') throw new PersonaApiError(500, 'boom')
      return { revision: 1 }
    })
    const out = await mirrorPersonas('src', items, 5)
    expect(out.report.failed).toEqual([{ hostId: 'h2', message: 'boom' }])
    expect(out.report.mirrored).toEqual(['h3'])
  })
})
