import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./persona-mirror', () => ({ mirrorPersonas: vi.fn() }))
vi.mock('./persona-draft-memory', () => ({ savePending: vi.fn(() => false) }))

import { mirrorPersonas } from './persona-mirror'
import { savePending } from './persona-draft-memory'
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
    expect(mirrorPersonas).toHaveBeenCalledWith('src', source.items, 5, 'h2')
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

  it('does nothing while a save is in flight', async () => {
    vi.mocked(savePending).mockReturnValue(true)
    await backfillHosts()
    expect(mirrorPersonas).not.toHaveBeenCalled()
  })

  it('does nothing while the source is offline', async () => {
    setStore(stale, 'error')
    await backfillHosts()
    expect(mirrorPersonas).not.toHaveBeenCalled()
  })
})
