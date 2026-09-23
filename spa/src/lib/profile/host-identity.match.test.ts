import { describe, expect, it } from 'vitest'
import { matchIncomingHosts, syncIdOfSync, type IdentityHost } from './host-identity'

const MLAB = 'mini-lab:278cbm'
const MLAB_WIRE = syncIdOfSync(MLAB)
const AIR = 'air-2026:k2k2k2'
const AIR_WIRE = syncIdOfSync(AIR)

function locals(...list: IdentityHost[]): Record<string, IdentityHost> {
  return Object.fromEntries(list.map((h) => [h.id, h]))
}

describe('matchIncomingHosts (spec §6, §11.5, §11.6)', () => {
  it('a canonical row matches the local host with that daemon; the local id is kept', () => {
    const m = matchIncomingHosts(locals({ id: 'loc001', daemonId: MLAB }), {
      [MLAB_WIRE]: { id: MLAB_WIRE, daemonId: MLAB },
    })
    expect(m.error).toBeUndefined()
    expect(m.byRow.get(MLAB_WIRE)).toBe('loc001')
    expect(m.removed).toEqual([])
  })

  it('a canonical key whose row lacks daemonId matches by syncIdOf(local.daemonId)', () => {
    const m = matchIncomingHosts(locals({ id: 'loc001', daemonId: MLAB }), { [MLAB_WIRE]: { id: MLAB_WIRE } })
    expect(m.byRow.get(MLAB_WIRE)).toBe('loc001')
  })

  it('a legacy row from ANOTHER device carrying daemonId matches by daemonId', () => {
    const m = matchIncomingHosts(locals({ id: 'loc001', daemonId: MLAB }), { frgn01: { id: 'frgn01', daemonId: MLAB } })
    expect(m.byRow.get('frgn01')).toBe('loc001')
    expect(m.removed).toEqual([])
  })

  it('a row carrying daemonId matches by daemonId ONLY: its key cannot capture another local host (§11.6)', () => {
    const m = matchIncomingHosts(locals({ id: 'loc001', daemonId: MLAB }, { id: 'loc002' }), {
      loc002: { id: 'loc002', daemonId: MLAB },
    })
    expect(m.byRow.get('loc002')).toBe('loc001')
    expect(m.removed).toEqual(['loc002'])
  })

  it('a row carrying an unknown daemonId is new even when its key equals a no-claim local id', () => {
    const m = matchIncomingHosts(locals({ id: 'loc002' }), { loc002: { id: 'loc002', daemonId: AIR } })
    expect(m.byRow.get('loc002')).toBe('new')
    expect(m.removed).toEqual(['loc002'])
  })

  it('a legacy row with no daemonId matches the no-claim local host of that id', () => {
    const m = matchIncomingHosts(locals({ id: 'loc002' }), { loc002: { id: 'loc002' } })
    expect(m.byRow.get('loc002')).toBe('loc002')
    expect(m.removed).toEqual([])
  })

  it('a legacy row with no daemonId does NOT match a local host of that id that has a daemonId', () => {
    const m = matchIncomingHosts(locals({ id: 'loc001', daemonId: MLAB }), { loc001: { id: 'loc001' } })
    expect(m.byRow.get('loc001')).toBe('new')
    expect(m.removed).toEqual(['loc001'])
  })

  it('an invalid daemonId on a row counts as none', () => {
    const m = matchIncomingHosts(locals({ id: 'loc002' }), { loc002: { id: 'loc002', daemonId: '' } })
    expect(m.byRow.get('loc002')).toBe('loc002')
  })

  it('a sync id of another version is unknown, never a legacy local id', () => {
    const m = matchIncomingHosts(locals({ id: 'd2_abc' }), { d2_abc: { id: 'd2_abc' } })
    expect(m.byRow.get('d2_abc')).toBe('new')
    expect(m.removed).toEqual(['d2_abc'])
  })

  it('a d1_ key matching no local daemon is new', () => {
    const m = matchIncomingHosts(locals({ id: 'loc001', daemonId: MLAB }), { [AIR_WIRE]: { id: AIR_WIRE, daemonId: AIR } })
    expect(m.byRow.get(AIR_WIRE)).toBe('new')
    expect(m.removed).toEqual(['loc001'])
  })

  it('local hosts matched by no row are removed, in record order', () => {
    const m = matchIncomingHosts(locals({ id: 'loc003' }, { id: 'loc001', daemonId: MLAB }, { id: 'loc002' }), {
      [MLAB_WIRE]: { id: MLAB_WIRE, daemonId: MLAB },
    })
    expect(m.removed).toEqual(['loc003', 'loc002'])
  })

  it('an empty payload removes every local host', () => {
    const m = matchIncomingHosts(locals({ id: 'loc001' }, { id: 'loc002' }), {})
    expect(m.byRow.size).toBe(0)
    expect(m.removed).toEqual(['loc001', 'loc002'])
  })

  it('garbage rows are total: no daemonId, matched by key rules only', () => {
    const m = matchIncomingHosts(locals({ id: 'loc002' }), { loc002: null, x: 42 } as unknown as Record<string, never>)
    expect(m.error).toBeUndefined()
    expect(m.byRow.get('loc002')).toBe('loc002')
    expect(m.byRow.get('x')).toBe('new')
  })

  describe('one-to-one (§11.5)', () => {
    it('two rows with the same daemonId → duplicate-host-identity', () => {
      const m = matchIncomingHosts(locals({ id: 'loc001', daemonId: MLAB }), {
        [MLAB_WIRE]: { id: MLAB_WIRE, daemonId: MLAB },
        frgn01: { id: 'frgn01', daemonId: MLAB },
      })
      expect(m.error).toBe('duplicate-host-identity')
      expect(m.byRow.size).toBe(0)
      expect(m.removed).toEqual([])
    })

    it('two rows with the same daemonId that no local host has → still duplicate', () => {
      const m = matchIncomingHosts({}, { a: { daemonId: AIR }, b: { daemonId: AIR } })
      expect(m.error).toBe('duplicate-host-identity')
    })

    it('a canonical key without daemonId and a legacy row resolving to the same daemon → duplicate', () => {
      const m = matchIncomingHosts(locals({ id: 'loc001', daemonId: MLAB }), {
        [MLAB_WIRE]: { id: MLAB_WIRE },
        frgn01: { id: 'frgn01', daemonId: MLAB },
      })
      expect(m.error).toBe('duplicate-host-identity')
    })

    it('two claims hashing to one sync id (injected hash) → duplicate', () => {
      const m = matchIncomingHosts({}, { a: { daemonId: 'one' }, b: { daemonId: 'two' } }, { hash: () => 'd1_same' })
      expect(m.error).toBe('duplicate-host-identity')
    })

    it('two distinct daemons are fine', () => {
      const m = matchIncomingHosts({}, { [MLAB_WIRE]: { daemonId: MLAB }, [AIR_WIRE]: { daemonId: AIR } })
      expect(m.error).toBeUndefined()
      expect([...m.byRow.values()]).toEqual(['new', 'new'])
    })
  })

  it('two LOCAL hosts claiming the row\'s daemon → host-identity-conflict, nothing matched', () => {
    const m = matchIncomingHosts(locals({ id: 'loc001', daemonId: MLAB }, { id: 'loc002', daemonId: MLAB }), {
      [MLAB_WIRE]: { id: MLAB_WIRE, daemonId: MLAB },
    })
    expect(m.error).toBe('host-identity-conflict')
    expect(m.byRow.size).toBe(0)
    expect(m.removed).toEqual([])
  })
})
