import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./persona-mirror', () => ({ mirrorPersonas: vi.fn() }))
vi.mock('./persona-draft-memory', () => ({ savePending: vi.fn(() => false), subscribeSave: vi.fn(() => () => {}) }))

import { mirrorPersonas } from './persona-mirror'
import { savePending, subscribeSave } from './persona-draft-memory'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { usePersonaSourceStore } from '../../stores/usePersonaSourceStore'
import { useHostStore } from '../../stores/useHostStore'
import { backfillHosts, resetBackfillMemory } from './persona-backfill'
import { makeCollection } from './__fixtures__/personas'

const source = makeCollection(['hiro', 'olga'], { at: 1, host: 'src' }, 5)
// a newer stamp than the source, but different roles: the content decides, not the stamp
const stale = makeCollection(['hiro'], { at: 99, host: 'h2' }, 3)

const done = (mirrored: string[]) => ({ report: { mirrored, failed: [], pulled: [] }, items: source.items, revision: 5 })

const setStore = (h2 = stale, srcStatus: 'ready' | 'error' = 'ready') =>
  usePersonaStore.setState({
    byHost: { src: { status: srcStatus, collection: source }, h2: { status: 'ready', collection: h2 } },
  })

describe('backfillHosts', () => {
  beforeEach(() => {
    resetBackfillMemory()
    vi.mocked(mirrorPersonas).mockReset().mockResolvedValue(done(['h2']))
    vi.mocked(savePending).mockReturnValue(false)
    useHostStore.setState({ hostOrder: ['src', 'h2'], hosts: { src: {}, h2: {} } as never })
    usePersonaSourceStore.setState({ hostId: 'src' })
    setStore()
  })

  it('writes the source items to a host whose items differ, whatever its stamp says', async () => {
    await backfillHosts()
    expect(mirrorPersonas).toHaveBeenCalledWith('src', source.items, 5, 'h2', expect.any(Function))
  })

  it('stops between the host read and write once a save has moved the source past the snapshot', async () => {
    let abort: (() => boolean) | undefined
    vi.mocked(mirrorPersonas).mockImplementation(async (_s, _i, _r, _o, a) => { abort = a; return done(['h2']) })
    await backfillHosts()
    expect(abort?.()).toBe(false)
    usePersonaStore.setState({ byHost: { ...usePersonaStore.getState().byHost, src: { status: 'ready', collection: { ...source, revision: 6 } } } })
    expect(abort?.()).toBe(true)
  })

  it('leaves a host that already holds the same items alone', async () => {
    setStore(makeCollection(['olga', 'hiro'], { at: 1, host: 'h2' }, 2))
    await backfillHosts()
    expect(mirrorPersonas).not.toHaveBeenCalled()
  })

  it('tries a given host and source content once, so a daemon that normalizes items cannot loop', async () => {
    await backfillHosts()
    await backfillHosts()
    expect(mirrorPersonas).toHaveBeenCalledTimes(1)
  })

  it('tries again on the next load when the copy did not go through', async () => {
    vi.mocked(mirrorPersonas).mockResolvedValueOnce(done([]))
    await backfillHosts()
    await backfillHosts()
    expect(mirrorPersonas).toHaveBeenCalledTimes(2)
  })

  it('does nothing while a save is in flight, then runs once when it ends', async () => {
    let onSaveChange: (() => void) | undefined
    vi.mocked(subscribeSave).mockImplementation((fn) => { onSaveChange = fn; return () => { onSaveChange = undefined } })
    vi.mocked(savePending).mockReturnValue(true)
    await backfillHosts()
    expect(mirrorPersonas).not.toHaveBeenCalled()
    vi.mocked(savePending).mockReturnValue(false)
    onSaveChange?.()
    await vi.waitFor(() => expect(mirrorPersonas).toHaveBeenCalledTimes(1))
  })

  it('runs again when a save moved the source while the write was out', async () => {
    vi.mocked(mirrorPersonas).mockImplementationOnce(async () => {
      // the save's reload lands while this write is in flight
      const saved = makeCollection(['hiro', 'olga', 'emma'], { at: 2, host: 'src' }, 6)
      usePersonaStore.setState({ byHost: { ...usePersonaStore.getState().byHost, src: { status: 'ready', collection: saved } } })
      return done(['h2'])
    })
    await backfillHosts()
    await vi.waitFor(() => expect(mirrorPersonas).toHaveBeenCalledTimes(2))
    expect(vi.mocked(mirrorPersonas).mock.calls[1][2]).toBe(6)
  })

  it('does nothing while the source is offline', async () => {
    setStore(stale, 'error')
    await backfillHosts()
    expect(mirrorPersonas).not.toHaveBeenCalled()
  })
})
