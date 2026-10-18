import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('../lib/names/persona-api', () => ({ fetchPersonas: vi.fn() }))

import { fetchPersonas } from '../lib/names/persona-api'
import { usePersonaStore, currentCollection, isNewerStamp } from './usePersonaStore'
import { makeCollection } from '../lib/names/__fixtures__/personas'

describe('isNewerStamp', () => {
  it('compares at first, then host', () => {
    expect(isNewerStamp({ at: 2, host: 'a' }, { at: 1, host: 'z' })).toBe(true)
    expect(isNewerStamp({ at: 1, host: 'b' }, { at: 1, host: 'a' })).toBe(true)
    expect(isNewerStamp({ at: 1, host: 'a' }, { at: 1, host: 'a' })).toBe(false)
    expect(isNewerStamp({ at: 1, host: 'z' }, { at: 2, host: 'a' })).toBe(false)
  })
})

describe('current collection', () => {
  it('is the whole collection with the highest stamp, never a union by code', () => {
    const old = makeCollection(['hiro', 'olga'], { at: 1, host: 'h1' })
    const fresh = makeCollection(['emma'], { at: 5, host: 'h2' })
    const cur = currentCollection({ h1: { status: 'ready', collection: old }, h2: { status: 'ready', collection: fresh } })
    expect(cur?.items.map((p) => p.code)).toEqual(['emma'])
  })

  it('breaks an equal stamp by host', () => {
    const a = makeCollection(['aaa'], { at: 5, host: 'h1' })
    const b = makeCollection(['bbb'], { at: 5, host: 'h2' })
    expect(currentCollection({ h1: { status: 'ready', collection: a }, h2: { status: 'ready', collection: b } })?.items[0].code).toBe('bbb')
  })

  it('is null when no host has loaded one', () => {
    expect(currentCollection({ h1: { status: 'idle', collection: null } })).toBeNull()
  })
})

describe('usePersonaStore', () => {
  beforeEach(() => {
    usePersonaStore.setState({ byHost: {} })
    vi.mocked(fetchPersonas).mockReset()
  })

  it('load stores the collection and marks ready', async () => {
    const c = makeCollection(['hiro'])
    vi.mocked(fetchPersonas).mockResolvedValue(c)
    await usePersonaStore.getState().load('h1')
    expect(usePersonaStore.getState().byHost.h1).toEqual({ status: 'ready', collection: c })
  })

  it('a failed load keeps the previous collection and marks error', async () => {
    const c = makeCollection(['hiro'])
    usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: c } } })
    vi.mocked(fetchPersonas).mockRejectedValue(new Error('boom'))
    await usePersonaStore.getState().load('h1')
    expect(usePersonaStore.getState().byHost.h1).toEqual({ status: 'error', collection: c })
  })

  it('forget drops the host', () => {
    usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: makeCollection(['hiro']) } } })
    usePersonaStore.getState().forget('h1')
    expect(usePersonaStore.getState().byHost.h1).toBeUndefined()
  })
})
