import { describe, expect, it } from 'vitest'
import { identityOf, identityOfSync, syncIdOfSync, type HostIdentity, type IdentityHost } from './host-identity'

const MLAB = 'mini-lab:278cbm'
const MLAB_WIRE = 'd1_2u8ajsho6ji7nk6h'

function hosts(...list: IdentityHost[]): Record<string, IdentityHost> {
  return Object.fromEntries(list.map((h) => [h.id, h]))
}

/** Run a case through BOTH builders and require identical results. */
async function both(input: Record<string, IdentityHost>, opts?: { hash?: (d: string) => string }): Promise<HostIdentity> {
  const sync = identityOfSync(input, opts)
  const asyncResult = await identityOf(input, opts)
  expect(asyncResult).toEqual(sync)
  expect(asyncResult.signature).toBe(sync.signature)
  return sync
}

describe('identityOf / identityOfSync (spec §4)', () => {
  it('a valid claim travels as syncIdOf(daemonId)', async () => {
    const id = await both(hosts({ id: 'abc123', daemonId: MLAB }))
    expect(id.toWire.get('abc123')).toBe(MLAB_WIRE)
    expect(id.toLocal.get(MLAB_WIRE)).toBe('abc123')
    expect(id.conflict).toBeNull()
  })

  it('a host with no claim travels under its local id', async () => {
    const id = await both(hosts({ id: 'abc123' }))
    expect(id.toWire.get('abc123')).toBe('abc123')
    expect(id.toLocal.get('abc123')).toBe('abc123')
    expect(id.conflict).toBeNull()
  })

  it('an invalid claim (empty, control char, too long) counts as no claim', async () => {
    const id = await both(
      hosts({ id: 'aaaaa1', daemonId: '' }, { id: 'aaaaa2', daemonId: 'x\ny' }, { id: 'aaaaa3', daemonId: 'a'.repeat(513) }),
    )
    for (const local of ['aaaaa1', 'aaaaa2', 'aaaaa3']) expect(id.toWire.get(local)).toBe(local)
    expect(id.conflict).toBeNull()
  })

  it('keys by the record key (the local id)', async () => {
    const id = await both({ k1abcd: { id: 'k1abcd', daemonId: MLAB } })
    expect([...id.toWire.keys()]).toEqual(['k1abcd'])
  })

  it('two local hosts claiming one daemon → conflict listing both, sorted', async () => {
    const id = await both(hosts({ id: 'zzz111', daemonId: MLAB }, { id: 'aaa111', daemonId: MLAB }, { id: 'mmm111' }))
    expect(id.conflict).toEqual(['aaa111', 'zzz111'])
    // The unrelated host still maps.
    expect(id.toWire.get('mmm111')).toBe('mmm111')
    // Conflicting hosts have no wire id: it would be ambiguous.
    expect(id.toWire.has('aaa111')).toBe(false)
    expect(id.toWire.has('zzz111')).toBe(false)
    expect(id.toLocal.has(MLAB_WIRE)).toBe(false)
  })

  it('two different claims hashing to one sync id → conflict (forced by an injected hash)', async () => {
    const id = await both(hosts({ id: 'aaa111', daemonId: 'one:1' }, { id: 'bbb111', daemonId: 'two:2' }), {
      hash: () => 'd1_0000000000000000',
    })
    expect(id.conflict).toEqual(['aaa111', 'bbb111'])
  })

  it('the injected hash is what builds wire ids', async () => {
    const id = await both(hosts({ id: 'aaa111', daemonId: 'one:1' }), { hash: (d) => `d1_${d.length}` })
    expect(id.toWire.get('aaa111')).toBe('d1_5')
  })

  it('a no-claim local id that looks like a sync id → conflict (it would be read as one on the wire)', async () => {
    const id = await both(hosts({ id: 'd1_abc' }, { id: 'ok1234' }))
    expect(id.conflict).toEqual(['d1_abc'])
    expect(id.toWire.get('ok1234')).toBe('ok1234')
  })

  it('a no-claim local id equal to another host\'s sync id → conflict', async () => {
    const id = await both(hosts({ id: 'aaa111', daemonId: 'x' }, { id: syncIdOfSync('x') }), {})
    expect(id.conflict).toEqual(['aaa111', syncIdOfSync('x')].sort())
  })

  it('no hosts → empty maps, no conflict', async () => {
    const id = await both({})
    expect(id.toWire.size).toBe(0)
    expect(id.conflict).toBeNull()
  })

  describe('signature', () => {
    it('is stable under record order', async () => {
      const a = await both(hosts({ id: 'aaa111', daemonId: MLAB }, { id: 'bbb111' }))
      const b = await both(hosts({ id: 'bbb111' }, { id: 'aaa111', daemonId: MLAB }))
      expect(a.signature).toBe(b.signature)
    })

    it('changes when a host learns its daemon (the re-key trigger, spec §11.3)', async () => {
      const before = await both(hosts({ id: 'aaa111' }))
      const after = await both(hosts({ id: 'aaa111', daemonId: MLAB }))
      expect(after.signature).not.toBe(before.signature)
    })

    it('changes when a conflict appears even if the pairs would not tell', async () => {
      const ok = await both(hosts({ id: 'aaa111', daemonId: 'one' }, { id: 'bbb111', daemonId: 'two' }), {
        hash: (d) => `d1_${d}`,
      })
      const clash = await both(hosts({ id: 'aaa111', daemonId: 'one' }, { id: 'bbb111', daemonId: 'two' }), {
        hash: () => 'd1_same',
      })
      expect(clash.signature).not.toBe(ok.signature)
      expect(clash.signature).toContain('aaa111')
    })

    it('does not change for fields other than id/daemonId', async () => {
      const a = await both({ aaa111: { id: 'aaa111', daemonId: MLAB, name: 'one' } as IdentityHost })
      const b = await both({ aaa111: { id: 'aaa111', daemonId: MLAB, name: 'two' } as IdentityHost })
      expect(a.signature).toBe(b.signature)
    })
  })
})
