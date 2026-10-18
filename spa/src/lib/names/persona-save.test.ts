import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./persona-api', async (orig) => ({ ...(await orig<typeof import('./persona-api')>()), putPersonas: vi.fn(), fetchPersonas: vi.fn() }))

import { putPersonas, PersonaConflictError } from './persona-api'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { savePersonas, PersonaValidationError } from './persona-save'
import { makeCollection, makePersona } from './__fixtures__/personas'

describe('savePersonas', () => {
  beforeEach(() => {
    vi.mocked(putPersonas).mockReset()
    usePersonaStore.setState({
      byHost: {
        h1: { status: 'ready', collection: makeCollection(['old'], { at: 1, host: 'h1' }, 3) },
        h2: { status: 'ready', collection: makeCollection(['new'], { at: 9, host: 'h2' }, 7) },
      },
      load: vi.fn(async () => {}),
    })
  })

  it('writes to the host whose collection is the one shown, with that host revision', async () => {
    vi.mocked(putPersonas).mockResolvedValue({ revision: 8 })
    const items = [makePersona('hiro')]
    const res = await savePersonas(items, 'h1')
    expect(putPersonas).toHaveBeenCalledWith('h2', items, 7)
    expect(res.hostId).toBe('h2')
    expect(usePersonaStore.getState().load).toHaveBeenCalledWith('h2')
  })

  it('uses the fallback host at revision 0 when no collection has loaded', async () => {
    usePersonaStore.setState({ byHost: {} })
    vi.mocked(putPersonas).mockResolvedValue({ revision: 1 })
    await savePersonas([makePersona('hiro')], 'h1')
    expect(putPersonas).toHaveBeenCalledWith('h1', expect.anything(), 0)
  })

  it('refuses an invalid persona before any request', async () => {
    await expect(savePersonas([makePersona('Bad!')], 'h1')).rejects.toBeInstanceOf(PersonaValidationError)
    expect(putPersonas).not.toHaveBeenCalled()
  })

  it('refuses more than 64 personas', async () => {
    const many = Array.from({ length: 65 }, (_, i) => makePersona('abc' + String.fromCharCode(97 + (i % 26)).repeat(1 + Math.floor(i / 26))))
    await expect(savePersonas(many, 'h1')).rejects.toBeInstanceOf(PersonaValidationError)
  })

  it('refuses a code twice in one collection', async () => {
    await expect(savePersonas([makePersona('hiro'), makePersona('hiro')], 'h1')).rejects.toBeInstanceOf(PersonaValidationError)
  })

  it('on a conflict re-reads that host and rethrows', async () => {
    vi.mocked(putPersonas).mockRejectedValue(new PersonaConflictError(makeCollection(['x'], { at: 20, host: 'h2' }, 9)))
    await expect(savePersonas([makePersona('hiro')], 'h1')).rejects.toBeInstanceOf(PersonaConflictError)
    expect(usePersonaStore.getState().load).toHaveBeenCalledWith('h2')
  })
})
