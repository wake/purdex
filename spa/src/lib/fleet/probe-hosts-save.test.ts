import { describe, it, expect, beforeEach, vi } from 'vitest'
import { saveProbeHosts, upsertProbe, removeProbe } from './probe-hosts-save'
import { useFleetStore } from './store'
import { useHostStore } from '../../stores/useHostStore'

vi.mock('./api', async (orig) => ({
  ...(await orig<typeof import('./api')>()),
  fetchProbeHosts: vi.fn(), putProbeHosts: vi.fn(),
}))
import { fetchProbeHosts, putProbeHosts } from './api'

const H = 'h1'
const A = { id: 'a', name: 'A', ssh_alias: 'a' }

beforeEach(() => {
  useFleetStore.getState().reset()
  vi.mocked(fetchProbeHosts).mockReset()
  vi.mocked(putProbeHosts).mockReset()
  useHostStore.setState({ hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } }, hostOrder: [H] } as never)
  const gen = useFleetStore.getState().begin(H)
  useFleetStore.getState().applyDefs(H, gen, { items: [A], revision: 2 })
})

describe('saveProbeHosts', () => {
  it('an action queued behind another does not run against the daemon a re-point put there meanwhile', async () => {
    let release!: () => void
    vi.mocked(putProbeHosts).mockImplementationOnce(() => new Promise((r) => { release = () => r({ kind: 'ok', current: { items: [A], revision: 3 } }) }))
    const first = saveProbeHosts(H, upsertProbe({ id: 'b', name: 'B', ssh_alias: 'b' }))
    const second = saveProbeHosts(H, removeProbe('a')) // queued behind the first
    await new Promise((r) => setTimeout(r, 0)) // the first task is out with its PUT
    // re-point: the new daemon's list is loaded before the second task gets its turn
    useFleetStore.getState().forgetHost(H)
    const gen = useFleetStore.getState().begin(H)
    useFleetStore.getState().applyDefs(H, gen, { items: [{ id: 'a', name: 'other daemon', ssh_alias: 'x' }], revision: 7 })
    release()
    await first
    await expect(second).resolves.toEqual({ kind: 'failed', reason: 'host changed' })
    expect(putProbeHosts).toHaveBeenCalledTimes(1)
  })

  it('a 409 whose copy cannot be read and cannot be fetched is a failure, not a reload', async () => {
    vi.mocked(putProbeHosts).mockResolvedValue({ kind: 'conflict', current: null })
    vi.mocked(fetchProbeHosts).mockRejectedValue(new Error('down'))
    await expect(saveProbeHosts(H, upsertProbe({ id: 'b', name: 'B', ssh_alias: 'b' }))).resolves.toEqual({ kind: 'failed', reason: 'reload failed' })
  })

  it('a 409 whose reload GET answers 200 without a readable collection is a failure too', async () => {
    vi.mocked(putProbeHosts).mockResolvedValue({ kind: 'conflict', current: null })
    vi.mocked(fetchProbeHosts).mockResolvedValue(null)
    await expect(saveProbeHosts(H, upsertProbe({ id: 'b', name: 'B', ssh_alias: 'b' }))).resolves.toEqual({ kind: 'failed', reason: 'reload failed' })
    expect(useFleetStore.getState().defs[H]?.revision).toBe(2)
  })

  it('a 409 without a readable copy reloads by GET and reports the conflict', async () => {
    vi.mocked(putProbeHosts).mockResolvedValue({ kind: 'conflict', current: null })
    vi.mocked(fetchProbeHosts).mockResolvedValue({ items: [A], revision: 9 })
    await expect(saveProbeHosts(H, upsertProbe({ id: 'b', name: 'B', ssh_alias: 'b' }))).resolves.toEqual({ kind: 'conflict' })
    expect(useFleetStore.getState().defs[H]?.revision).toBe(9)
  })

  it('removing a row that is already gone sends nothing', async () => {
    await expect(saveProbeHosts(H, removeProbe('ghost'))).resolves.toEqual({ kind: 'ok' })
    expect(putProbeHosts).not.toHaveBeenCalled()
  })

  it('refuses a 17th row', async () => {
    const gen = useFleetStore.getState().genOf(H)
    useFleetStore.getState().applyDefs(H, gen, { items: Array.from({ length: 16 }, (_, i) => ({ id: `i${i}`, name: 'n', ssh_alias: 'a' })), revision: 1 })
    await expect(saveProbeHosts(H, upsertProbe({ id: '', name: 'x', ssh_alias: 'a' }))).resolves.toEqual({ kind: 'limit' })
    expect(putProbeHosts).not.toHaveBeenCalled()
  })
})
