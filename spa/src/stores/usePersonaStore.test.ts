import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('../lib/names/persona-api', () => ({ fetchPersonas: vi.fn() }))

import { fetchPersonas } from '../lib/names/persona-api'
import { usePersonaStore, currentCollection, currentSource, isNewerStamp } from './usePersonaStore'
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

describe('current source', () => {
  const mine = makeCollection(['hiro'], { at: 1, host: 'h1' })
  const newer = makeCollection(['emma'], { at: 9, host: 'h2' })

  it('prefers the source host even when another copy has a newer stamp', () => {
    const src = currentSource({ h1: { status: 'ready', collection: mine }, h2: { status: 'ready', collection: newer } }, 'h1')
    expect(src).toMatchObject({ hostId: 'h1', readOnly: false })
  })

  it('stands in with the newest other copy, read-only, while the source is in error or empty', () => {
    expect(currentSource({ h1: { status: 'error', collection: mine }, h2: { status: 'ready', collection: newer } }, 'h1'))
      .toMatchObject({ hostId: 'h2', readOnly: true })
    expect(currentSource({ h1: { status: 'idle', collection: null }, h2: { status: 'ready', collection: newer } }, 'h1'))
      .toMatchObject({ hostId: 'h2', readOnly: true })
  })
})

describe('markOffline', () => {
  it('keeps the cached copy but makes it a stand-in source, and leaves an empty entry alone', () => {
    const c = makeCollection(['hiro'], { at: 1, host: 'h1' })
    usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: c }, h2: { status: 'ready', collection: makeCollection(['emma'], { at: 9, host: 'h2' }) } } })
    usePersonaStore.getState().markOffline('h1')
    usePersonaStore.getState().markOffline('nobody')
    expect(usePersonaStore.getState().byHost.h1).toEqual({ status: 'error', collection: c })
    expect(currentSource(usePersonaStore.getState().byHost, 'h1')).toMatchObject({ hostId: 'h2', readOnly: true })
  })
})

describe('markOffline and a read in flight', () => {
  beforeEach(() => {
    usePersonaStore.setState({ byHost: {} })
    vi.mocked(fetchPersonas).mockReset()
  })

  it('a read that started before the drop does not make the host ready again', async () => {
    let answer!: (c: ReturnType<typeof makeCollection>) => void
    vi.mocked(fetchPersonas).mockImplementation(() => new Promise((r) => { answer = r }))
    const pending = usePersonaStore.getState().load('h1')
    usePersonaStore.setState({ byHost: { h1: { status: 'loading', collection: makeCollection(['hiro']) } } })
    usePersonaStore.getState().markOffline('h1')
    answer(makeCollection(['emma']))
    await pending
    expect(usePersonaStore.getState().byHost.h1.status).toBe('error')
    expect(usePersonaStore.getState().byHost.h1.collection?.items[0].code).toBe('hiro')
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

  it('drops an older response that lands after a newer one', async () => {
    const older = makeCollection(['old'], { at: 1, host: 'h1' })
    const newer = makeCollection(['new'], { at: 2, host: 'h1' })
    let finishOlder!: (c: typeof older) => void
    vi.mocked(fetchPersonas)
      .mockImplementationOnce(() => new Promise((r) => { finishOlder = r }))
      .mockResolvedValueOnce(newer)
    const first = usePersonaStore.getState().load('h1')
    await usePersonaStore.getState().load('h1')
    finishOlder(older)
    await first
    expect(usePersonaStore.getState().byHost.h1.collection).toEqual(newer)
  })

  it('does not bring a host back when forget ran while its load was in flight', async () => {
    let finish!: (c: ReturnType<typeof makeCollection>) => void
    vi.mocked(fetchPersonas).mockImplementationOnce(() => new Promise((r) => { finish = r }))
    const pending = usePersonaStore.getState().load('h1')
    usePersonaStore.getState().forget('h1')
    finish(makeCollection(['hiro']))
    await pending
    expect(usePersonaStore.getState().byHost.h1).toBeUndefined()
  })

  it('forget drops the host', () => {
    usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: makeCollection(['hiro']) } } })
    usePersonaStore.getState().forget('h1')
    expect(usePersonaStore.getState().byHost.h1).toBeUndefined()
  })
})
