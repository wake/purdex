import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./persona-api', async (orig) => ({ ...(await orig<typeof import('./persona-api')>()), putPersonas: vi.fn(), fetchPersonas: vi.fn() }))

vi.mock('./persona-mirror', () => ({
  mirrorPersonas: vi.fn(async (_h: string, items: unknown[], revision: number) => ({ report: { mirrored: [], failed: [], pulled: [] }, items, revision })),
}))

import { putPersonas, PersonaConflictError } from './persona-api'
import { mirrorPersonas } from './persona-mirror'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { usePersonaSourceStore } from '../../stores/usePersonaSourceStore'
import { useHostStore } from '../../stores/useHostStore'
import { savePersonas, editBase, PersonaValidationError, PersonaStaleError, PersonaReadOnlyError } from './persona-save'
import { makeCollection, makePersona } from './__fixtures__/personas'

describe('savePersonas', () => {
  beforeEach(() => {
    vi.mocked(putPersonas).mockReset()
    // h2 is this device's source host
    useHostStore.setState({ hosts: { h1: {}, h2: {}, h3: {} } as never, hostOrder: ['h2', 'h1', 'h3'] })
    usePersonaSourceStore.setState({ hostId: 'h2' })
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
    const res = await savePersonas(items, editBase(), 'h1')
    expect(putPersonas).toHaveBeenCalledWith('h2', items, 7)
    expect(res.hostId).toBe('h2')
    expect(usePersonaStore.getState().load).toHaveBeenCalledWith('h2')
  })

  it('writes the source first, then hands the same items to the mirror and returns its report', async () => {
    vi.mocked(putPersonas).mockResolvedValue({ revision: 8 })
    vi.mocked(mirrorPersonas).mockResolvedValue({ report: { mirrored: ['h1'], failed: [], pulled: [] }, items: [], revision: 8 })
    const items = [makePersona('hiro')]
    const res = await savePersonas(items, editBase(), 'h1')
    expect(mirrorPersonas).toHaveBeenCalledWith('h2', items, 8)
    expect(res.mirror.mirrored).toEqual(['h1'])
  })

  it('uses the fallback host at revision 0 when no collection has loaded', async () => {
    usePersonaStore.setState({ byHost: {} })
    vi.mocked(putPersonas).mockResolvedValue({ revision: 1 })
    await savePersonas([makePersona('hiro')], editBase(), 'h1')
    expect(editBase()).toBeNull()
    expect(putPersonas).toHaveBeenCalledWith('h1', expect.anything(), 0)
  })

  it('is not moved by another host holding a newer stamp: the source decides', async () => {
    const base = editBase() // the form opened on h2's collection
    usePersonaStore.setState({
      byHost: {
        ...usePersonaStore.getState().byHost,
        h3: { status: 'ready', collection: makeCollection(['fresh'], { at: 50, host: 'h3' }, 2) },
      },
    })
    vi.mocked(putPersonas).mockResolvedValue({ revision: 8 })
    await savePersonas([makePersona('hiro')], base, 'h1')
    expect(putPersonas).toHaveBeenCalledWith('h2', expect.anything(), 7)
  })

  it('refuses to write a stand-in copy when the source is offline, even from a form opened before', async () => {
    const base = editBase() // opened while h2, the source, was up
    usePersonaStore.setState({
      byHost: { ...usePersonaStore.getState().byHost, h2: { status: 'error', collection: null } },
    })
    await expect(savePersonas([makePersona('hiro')], base, 'h1')).rejects.toBeInstanceOf(PersonaReadOnlyError)
    // the stale path would have re-based the form onto h1; a second submit must be refused too
    await expect(savePersonas([makePersona('hiro')], editBase(), 'h1')).rejects.toBeInstanceOf(PersonaReadOnlyError)
    expect(putPersonas).not.toHaveBeenCalled()
  })

  it('returns the refreshed base, so a second save from the same open form is not stale', async () => {
    vi.mocked(putPersonas).mockResolvedValue({ revision: 8 })
    // the real store would now hold the saved collection at revision 8 with a newer stamp
    const reload = vi.fn(async () => {
      usePersonaStore.setState({
        byHost: { ...usePersonaStore.getState().byHost, h2: { status: 'ready', collection: makeCollection(['hiro'], { at: 20, host: 'h2' }, 8) } },
      })
    })
    usePersonaStore.setState({ load: reload })
    const first = await savePersonas([makePersona('hiro')], editBase(), 'h1')
    expect(first.base).toMatchObject({ hostId: 'h2', revision: 8, stamp: { at: 20, host: 'h2' } })
    await savePersonas([makePersona('hiro', { name: 'Hiro Two' })], first.base, 'h1')
    expect(putPersonas).toHaveBeenLastCalledWith('h2', expect.anything(), 8)
  })

  it('hands back no base when the same host moved past the revision this save wrote', async () => {
    vi.mocked(putPersonas).mockResolvedValue({ revision: 8 })
    usePersonaStore.setState({
      load: vi.fn(async () => {
        // someone else wrote revision 9 on h2 before this save's reload ran
        usePersonaStore.setState({
          byHost: { ...usePersonaStore.getState().byHost, h2: { status: 'ready', collection: makeCollection(['theirs'], { at: 30, host: 'h2' }, 9) } },
        })
      }),
    })
    const res = await savePersonas([makePersona('hiro')], editBase(), 'h1')
    expect(res.base).toBeNull()
    // a null base cannot be used to save over what exists
    await expect(savePersonas([makePersona('hiro')], res.base, 'h1')).rejects.toBeInstanceOf(PersonaStaleError)
  })

  it('refuses when the same host moved to a newer revision under the form', async () => {
    const base = editBase()
    usePersonaStore.setState({
      byHost: { ...usePersonaStore.getState().byHost, h2: { status: 'ready', collection: makeCollection(['new'], { at: 12, host: 'h2' }, 8) } },
    })
    await expect(savePersonas([makePersona('hiro')], base, 'h1')).rejects.toBeInstanceOf(PersonaStaleError)
  })

  it('refuses an invalid persona before any request', async () => {
    await expect(savePersonas([makePersona('Bad!')], editBase(), 'h1')).rejects.toBeInstanceOf(PersonaValidationError)
    expect(putPersonas).not.toHaveBeenCalled()
  })

  it('refuses more than 64 personas', async () => {
    const many = Array.from({ length: 65 }, (_, i) => makePersona('abc' + String.fromCharCode(97 + (i % 26)).repeat(1 + Math.floor(i / 26))))
    await expect(savePersonas(many, editBase(), 'h1')).rejects.toBeInstanceOf(PersonaValidationError)
  })

  it('refuses a code twice in one collection', async () => {
    await expect(savePersonas([makePersona('hiro'), makePersona('hiro')], editBase(), 'h1')).rejects.toBeInstanceOf(PersonaValidationError)
  })

  it('on a conflict re-reads that host and rethrows', async () => {
    vi.mocked(putPersonas).mockRejectedValue(new PersonaConflictError(makeCollection(['x'], { at: 20, host: 'h2' }, 9)))
    await expect(savePersonas([makePersona('hiro')], editBase(), 'h1')).rejects.toBeInstanceOf(PersonaConflictError)
    expect(usePersonaStore.getState().load).toHaveBeenCalledWith('h2')
  })
})
