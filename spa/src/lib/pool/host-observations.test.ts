// spa/src/lib/pool/host-observations.test.ts — the 行程 tab's undeclared heavy commands, per host (monitoring tabs spec M45; plan MON-8c): read once per page, 7 days back, a late answer never writes, a refusal and a failure told apart.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const fetchUndeclared = vi.fn()
vi.mock('./observations-api', async (orig) => ({ ...(await orig<typeof import('./observations-api')>()), fetchUndeclared: (...a: unknown[]) => fetchUndeclared(...a) }))

import { clearHostObservations, holdObservations, loadUndeclared, readUndeclared, readUndeclaredLoad } from './host-observations'
import { PoolReadError } from './api'
import type { UndeclaredPage } from './observations-api'

const NOW = 1_800_000_000_000
const page = (signature: string, known = true): UndeclaredPage => ({ host: 'mlab', known, truncated: false, groups: [{ signature, count: 1, cpuSeconds: 1, maxMemMb: 1, lastAt: NOW, lastSession: 's', lastVia: 'session', lastCommand: signature }] })

beforeEach(() => { fetchUndeclared.mockReset(); clearHostObservations() })

describe('loadUndeclared', () => {
  it('asks 7 days back from now, and keeps the page', async () => {
    fetchUndeclared.mockResolvedValue(page('go test'))
    expect(await loadUndeclared('cfg-a', 'mlab', 'h1', NOW)).toBe('ok')
    const [source, alias, since] = fetchUndeclared.mock.calls[0]
    expect([source, alias]).toEqual(['cfg-a', 'mlab'])
    expect(Date.parse(since as string)).toBe(NOW - 7 * 24 * 3600_000)
    expect(readUndeclared('h1')!.page.groups[0].signature).toBe('go test')
    expect(readUndeclared('h1')!.at).toBe(NOW)
  })

  it('a refresh replaces the page; a failure keeps the last good one and says so', async () => {
    fetchUndeclared.mockResolvedValueOnce(page('first'))
    await loadUndeclared('cfg-a', 'mlab', 'h1', NOW)
    fetchUndeclared.mockResolvedValueOnce(page('second'))
    await loadUndeclared('cfg-a', 'mlab', 'h1', NOW + 60_000)
    expect(readUndeclared('h1')!.page.groups[0].signature).toBe('second')
    fetchUndeclared.mockRejectedValueOnce(new Error('boom'))
    expect(await loadUndeclared('cfg-a', 'mlab', 'h1', NOW + 120_000)).toBe('error')
    expect(readUndeclared('h1')!.page.groups[0].signature).toBe('second')
    expect(readUndeclaredLoad('h1')).toBe('error')
  })

  it('a 403 is told apart from a failure', async () => {
    fetchUndeclared.mockRejectedValueOnce(new PoolReadError('/p', 403, { code: 'forbidden', message: '' } as never))
    expect(await loadUndeclared('cfg-a', 'mlab', 'h1', NOW)).toBe('forbidden')
    expect(readUndeclared('h1')).toBeNull()
  })

  it('an older answer that comes back after a newer read started writes nothing', async () => {
    let first: (v: unknown) => void = () => {}
    fetchUndeclared.mockReturnValueOnce(new Promise((r) => { first = r }))
    const a = loadUndeclared('cfg-a', 'mlab', 'h1', NOW)
    fetchUndeclared.mockResolvedValueOnce(page('newer'))
    await loadUndeclared('cfg-a', 'mlab-renamed', 'h1', NOW + 1000) // another ask: it takes over
    first(page('older'))
    await a
    expect(readUndeclared('h1')!.page.groups[0].signature).toBe('newer')
  })

  it('an answer that comes back after the last page left writes nothing', async () => {
    let answer: (v: unknown) => void = () => {}
    fetchUndeclared.mockReturnValueOnce(new Promise((r) => { answer = r }))
    const release = holdObservations('h1')
    const p = loadUndeclared('cfg-a', 'mlab', 'h1', NOW)
    release()
    answer(page('late'))
    await p
    expect(readUndeclared('h1')).toBeNull()
  })

  it('the same ask while one is out shares it: one request', async () => {
    let answer: (v: unknown) => void = () => {}
    fetchUndeclared.mockReturnValueOnce(new Promise((r) => { answer = r }))
    fetchUndeclared.mockRejectedValue(new Error('boom'))
    const a = loadUndeclared('cfg-a', 'mlab', 'h1', NOW)
    const b = loadUndeclared('cfg-a', 'mlab', 'h1', NOW)
    expect(fetchUndeclared).toHaveBeenCalledTimes(1)
    answer(page('shared'))
    expect([await a, await b]).toEqual(['ok', 'ok'])
  })

  it('known: false (an older wwd) is kept as that', async () => {
    fetchUndeclared.mockResolvedValue(page('x', false))
    await loadUndeclared('cfg-a', 'mlab', 'h1', NOW)
    expect(readUndeclared('h1')!.page.known).toBe(false)
  })
})
