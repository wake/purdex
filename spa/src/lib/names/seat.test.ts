import { describe, it, expect, beforeEach } from 'vitest'
import { addressName, seatOf } from './seat'
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
