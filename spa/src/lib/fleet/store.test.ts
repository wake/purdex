import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useFleetStore } from './store'
import type { FleetPayload } from './types'

vi.mock('./api', async (orig) => ({ ...(await orig<typeof import('./api')>()), fetchProbeHosts: vi.fn() }))
import { fetchProbeHosts } from './api'

const H = 'h1'
const nums = { cpu: 1, mem: 2, disk: 3, load1: 0, ncpu: 4, zone: 'normal', trend: 'flat', sampled_at: 1 } as const
const payload = (cpu: number): FleetPayload => ({ self: { ...nums, cpu }, probes: [] })
const s = () => useFleetStore.getState()
const entry = () => s().byHost[H]

beforeEach(() => { s().reset(); vi.mocked(fetchProbeHosts).mockReset() })

describe('events', () => {
  it('are written only once this generation has fleet.v1 confirmed', () => {
    const gen = s().begin(H)
    s().applyEvent(H, payload(5))
    expect(entry().self).toBeNull()
    s().setSupport(H, gen, 'yes')
    s().applyEvent(H, payload(6))
    expect(entry().self?.cpu).toBe(6)
  })

  it('are dropped after a disconnect, a re-point and a removal', () => {
    const gen = s().begin(H)
    s().setSupport(H, gen, 'yes')
    s().fence(H)
    s().applyEvent(H, payload(1))
    expect(entry().self).toBeNull()
    s().forgetHost(H)
    s().applyEvent(H, payload(2))
    expect(entry()).toBeUndefined()
  })

  it('an event that landed first is not overwritten by the older GET reply', () => {
    const gen = s().begin(H)
    s().setSupport(H, gen, 'yes')
    s().applyEvent(H, payload(10))
    s().applyFetched(H, gen, payload(1))
    expect(entry().self?.cpu).toBe(10)
  })
})

describe('the initial GET', () => {
  it('writes under the generation it began in', () => {
    const gen = s().begin(H)
    s().setSupport(H, gen, 'yes')
    s().applyFetched(H, gen, payload(3))
    expect(entry().self?.cpu).toBe(3)
    expect(entry().updatedAt).not.toBeNull()
  })

  it.each([
    ['a disconnect', () => s().fence(H)],
    ['a re-point', () => s().forgetHost(H)],
    ['a new connection', () => { s().begin(H) }],
  ])('is dropped after %s', (_n, move) => {
    const gen = s().begin(H)
    s().setSupport(H, gen, 'yes')
    move()
    s().applyFetched(H, gen, payload(3))
    expect(entry()?.self ?? null).toBeNull()
  })
})

describe('the initial GET across connections', () => {
  it('the old connection\'s reply is dropped even once the new connection confirmed fleet.v1', () => {
    const old = s().begin(H)
    s().setSupport(H, old, 'yes')
    const next = s().begin(H)
    s().setSupport(H, next, 'yes')
    s().applyFetched(H, old, payload(3))
    expect(entry().self).toBeNull()
    s().applyFetched(H, next, payload(4))
    expect(entry().self?.cpu).toBe(4)
  })
})

describe('offline', () => {
  it('a disconnect drops the numbers and remembers when', () => {
    const gen = s().begin(H)
    s().setSupport(H, gen, 'yes')
    s().applyFetched(H, gen, payload(3))
    s().fence(H)
    expect(entry().self).toBeNull()
    expect(entry().probes).toEqual([])
    expect(entry().support).toBe('unknown')
    expect(entry().offlineSince).toBeTypeOf('number')
  })
  it('a new connection clears offlineSince', () => {
    s().fence(H)
    s().begin(H)
    expect(entry().offlineSince).toBeNull()
  })
})

describe('probe host definitions', () => {
  it('load under the current generation', async () => {
    vi.mocked(fetchProbeHosts).mockResolvedValue({ items: [{ id: 'a', name: 'n', ssh_alias: 's' }], revision: 2 })
    s().begin(H)
    await s().loadDefs(H)
    expect(s().defs[H]?.revision).toBe(2)
  })
  it('a reply after the generation moved is dropped', async () => {
    let resolve!: (v: never) => void
    vi.mocked(fetchProbeHosts).mockReturnValue(new Promise((r) => { resolve = r as never }))
    s().begin(H)
    const p = s().loadDefs(H)
    s().fence(H)
    resolve({ items: [], revision: 7 } as never)
    await p
    expect(s().defs[H]).toBeUndefined()
  })
  it('a failed load keeps what was there', async () => {
    const gen = s().begin(H)
    s().applyDefs(H, gen, { items: [], revision: 1 })
    vi.mocked(fetchProbeHosts).mockRejectedValue(new Error('x'))
    await s().loadDefs(H)
    expect(s().defs[H]?.revision).toBe(1)
  })
})

describe('focusProbe', () => {
  it('is cleared when its prober is forgotten', () => {
    s().setFocusProbe({ proberHostId: H, probeId: 'p' })
    s().forgetHost('other')
    expect(s().focusProbe).not.toBeNull()
    s().forgetHost(H)
    expect(s().focusProbe).toBeNull()
  })
})
