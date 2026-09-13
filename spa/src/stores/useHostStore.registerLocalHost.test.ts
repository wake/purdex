import { beforeEach, describe, expect, it } from 'vitest'
import { useHostStore } from './useHostStore'

// reset() keeps the default 'mlab' host (100.64.0.2), so assertions filter
// by the endpoint under test instead of counting all hosts.
const at = (ip: string, port: number) => Object.values(useHostStore.getState().hosts).filter((h) => h.ip === ip && h.port === port)

describe('registerLocalHost', () => {
  beforeEach(() => { useHostStore.getState().reset() })

  it('adds a host named after the machine', () => {
    const id = useHostStore.getState().registerLocalHost({ url: 'http://100.64.0.9:7860', token: 'purdex_a', hostname: 'air-2026' })
    const h = useHostStore.getState().hosts[id]
    expect(h).toMatchObject({ name: 'air-2026', ip: '100.64.0.9', port: 7860, token: 'purdex_a' })
    expect(useHostStore.getState().hostOrder).toContain(id)
    expect(at('100.64.0.9', 7860)).toHaveLength(1)
  })

  it('is idempotent on the same ip:port and fills only an empty token', () => {
    const s = useHostStore.getState()
    const existing = s.addHost({ name: 'x', ip: '100.64.0.9', port: 7860, token: null })
    const id = s.registerLocalHost({ url: 'http://100.64.0.9:7860', token: 'purdex_b', hostname: 'air-2026' })
    expect(id).toBe(existing)
    expect(useHostStore.getState().hosts[existing].token).toBe('purdex_b')
    expect(useHostStore.getState().hosts[existing].name).toBe('x')
    expect(at('100.64.0.9', 7860)).toHaveLength(1)
  })

  it('an explicit :80 is normalised away by URL and must still register as 80', () => {
    const id = useHostStore.getState().registerLocalHost({ url: 'http://100.64.0.9:80', token: 'purdex_c', hostname: 'air-2026' })
    expect(useHostStore.getState().hosts[id].port).toBe(80)
  })

  it('never overwrites a live token', () => {
    const s = useHostStore.getState()
    const existing = s.addHost({ name: 'x', ip: '100.64.0.9', port: 7860, token: 'purdex_live' })
    s.registerLocalHost({ url: 'http://100.64.0.9:7860', token: 'purdex_new', hostname: 'air-2026' })
    expect(useHostStore.getState().hosts[existing].token).toBe('purdex_live')
  })
})
