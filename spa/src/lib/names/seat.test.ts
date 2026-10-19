import { describe, it, expect, beforeEach } from 'vitest'
import { addressName, isVerifiedPeer, seatLabel, seatOf } from './seat'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { makeCollection, makePersona } from './__fixtures__/personas'

describe('addressName', () => {
  it('takes what follows the last slash', () => {
    expect(addressName('mlab/purdex-1f-hiro')).toBe('purdex-1f-hiro')
    expect(addressName('purdex-1f-hiro')).toBe('purdex-1f-hiro')
    expect(addressName('mlab/')).toBe('')
  })
})

describe('seatOf', () => {
  beforeEach(() => usePersonaStore.setState({ byHost: {} }))

  it('resolves the suffix after the last dash through the current collection', () => {
    const c = makeCollection(['hiro'])
    c.items[0] = makePersona('hiro', { name: 'Hiro', avatar_data: 'data:image/png;base64,AA==' })
    usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: c } } })
    expect(seatOf('mlab/purdex-1f-hiro')).toEqual({ code: 'hiro', name: 'Hiro', avatar: 'data:image/png;base64,AA==' })
    expect(seatOf('purdex-1f-hiro')?.code).toBe('hiro')
  })

  it('still resolves a retired persona (L5)', () => {
    const c = makeCollection(['hiro'])
    c.items[0] = makePersona('hiro', { retired: true })
    usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: c } } })
    expect(seatOf('mlab/x-hiro')?.code).toBe('hiro')
  })

  it('is null for a ref, an unknown suffix, or an empty store', () => {
    expect(seatOf('mlab/purdex-1f-hiro')).toBeNull()
    usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: makeCollection(['hiro']) } } })
    expect(seatOf('mlab/_q34psn')).toBeNull()
    expect(seatOf('mlab/purdex-1f-emma')).toBeNull()
    expect(seatOf('')).toBeNull()
  })
})

describe('seatLabel', () => {
  beforeEach(() => usePersonaStore.setState({ byHost: {} }))
  const seed = () => {
    const c = makeCollection(['hiro'])
    c.items[0] = makePersona('hiro', { name: 'Hiro' })
    usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: c } } })
  }

  it('is the persona\'s name with the title after it, or the name alone', () => {
    seed()
    expect(seatLabel('mlab/purdex-1f-hiro', 'review the plan')).toBe('Hiro · review the plan')
    expect(seatLabel('mlab/purdex-1f-hiro')).toBe('Hiro')
    expect(seatLabel('mlab/purdex-1f-hiro', '  ')).toBe('Hiro')
  })

  it('is null when the address names no persona, so the caller keeps its own label', () => {
    seed()
    expect(seatLabel('mlab/purdex-1f-ghost', 't')).toBeNull()
    expect(seatLabel('mlab/_q34psn')).toBeNull()
    expect(seatLabel('')).toBeNull()
  })
})

describe('isVerifiedPeer', () => {
  it('is true only for a peer whose identity the transport established', () => {
    expect(isVerifiedPeer({ from: { kind: 'peer', name: 'mlab/x-hiro' } })).toBe(true)
    expect(isVerifiedPeer({ from: { kind: 'peer', name: 'mlab/x-hiro', unverified: false } })).toBe(true)
  })
  it('is false for an unverified sender, a mod notice, any other kind, and no sender', () => {
    expect(isVerifiedPeer({ from: { kind: 'peer', name: 'mlab/x-hiro', unverified: true } })).toBe(false)
    expect(isVerifiedPeer({ from: { kind: 'plugin', name: 'hiro' } })).toBe(false)
    expect(isVerifiedPeer({ from: { kind: 'user' } })).toBe(false)
    expect(isVerifiedPeer({})).toBe(false)
  })
})
