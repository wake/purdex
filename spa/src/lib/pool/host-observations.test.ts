// spa/src/lib/pool/host-observations.test.ts — the 行程 tab's undeclared heavy commands, per host (monitoring tabs spec M45; plan MON-8c): read once per page, 7 days back, a late answer never writes, a refusal and a failure told apart.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const fetchUndeclared = vi.fn()
vi.mock('./observations-api', async (orig) => ({ ...(await orig<typeof import('./observations-api')>()), fetchUndeclared: (...a: unknown[]) => fetchUndeclared(...a) }))

import { clearHostObservations, holdObservations, loadUndeclared, observationKey, readUndeclared, readUndeclaredLoad } from './host-observations'
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
    expect(readUndeclared('h1', observationKey('cfg-a', 'mlab'))!.page.groups[0].signature).toBe('go test')
    expect(readUndeclared('h1', observationKey('cfg-a', 'mlab'))!.at).toBe(NOW)
  })

  it('a refresh replaces the page; a failure keeps the last good one and says so', async () => {
    fetchUndeclared.mockResolvedValueOnce(page('first'))
    await loadUndeclared('cfg-a', 'mlab', 'h1', NOW)
    fetchUndeclared.mockResolvedValueOnce(page('second'))
    await loadUndeclared('cfg-a', 'mlab', 'h1', NOW + 60_000)
    expect(readUndeclared('h1', observationKey('cfg-a', 'mlab'))!.page.groups[0].signature).toBe('second')
    fetchUndeclared.mockRejectedValueOnce(new Error('boom'))
    expect(await loadUndeclared('cfg-a', 'mlab', 'h1', NOW + 120_000)).toBe('error')
    expect(readUndeclared('h1', observationKey('cfg-a', 'mlab'))!.page.groups[0].signature).toBe('second')
    expect(readUndeclaredLoad('h1')).toBe('error')
  })

  it('a 403 is told apart from a failure', async () => {
    fetchUndeclared.mockRejectedValueOnce(new PoolReadError('/p', 403, { code: 'forbidden', message: '' } as never))
    expect(await loadUndeclared('cfg-a', 'mlab', 'h1', NOW)).toBe('forbidden')
    expect(readUndeclared('h1', observationKey('cfg-a', 'mlab'))).toBeNull()
  })

  it('an older answer that comes back after a newer read started writes nothing', async () => {
    let first: (v: unknown) => void = () => {}
    fetchUndeclared.mockReturnValueOnce(new Promise((r) => { first = r }))
    const a = loadUndeclared('cfg-a', 'mlab', 'h1', NOW)
    fetchUndeclared.mockResolvedValueOnce(page('newer'))
    await loadUndeclared('cfg-a', 'mlab-renamed', 'h1', NOW + 1000) // another ask: it takes over
    first(page('older'))
    await a
    expect(readUndeclared('h1', observationKey('cfg-a', 'mlab-renamed'))!.page.groups[0].signature).toBe('newer')
    expect(readUndeclared('h1', observationKey('cfg-a', 'mlab'))).toBeNull()
  })

  it('an answer that comes back after the last page left writes nothing', async () => {
    let answer: (v: unknown) => void = () => {}
    fetchUndeclared.mockReturnValueOnce(new Promise((r) => { answer = r }))
    const release = holdObservations('h1')
    const p = loadUndeclared('cfg-a', 'mlab', 'h1', NOW)
    release()
    answer(page('late'))
    await p
    expect(readUndeclared('h1', observationKey('cfg-a', 'mlab'))).toBeNull()
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

  it('a 403 after a good read drops the page: an admin-only reading is not shown once the token may not read it (codex attack)', async () => {
    fetchUndeclared.mockResolvedValueOnce(page('secret'))
    await loadUndeclared('cfg-a', 'mlab', 'h1', NOW)
    expect(readUndeclared('h1', observationKey('cfg-a', 'mlab'))).not.toBeNull()
    fetchUndeclared.mockRejectedValueOnce(new PoolReadError('/p', 403, { code: 'forbidden', message: '' } as never))
    expect(await loadUndeclared('cfg-a', 'mlab', 'h1', NOW + 60_000)).toBe('forbidden')
    expect(readUndeclared('h1', observationKey('cfg-a', 'mlab'))).toBeNull()
  })

  it('the page belongs to the source and alias it was read for: another alias or source never gets it, pending, failed or refused (codex attack)', async () => {
    fetchUndeclared.mockResolvedValueOnce(page('old-name'))
    await loadUndeclared('cfg-a', 'old', 'h1', NOW)
    const mine = observationKey('cfg-a', 'old')
    const renamed = observationKey('cfg-a', 'new')
    expect(readUndeclared('h1', mine)).not.toBeNull()
    expect(readUndeclared('h1', renamed)).toBeNull() // before any read of the new name
    expect(readUndeclared('h1', observationKey('cfg-b', 'old'))).toBeNull()
    for (const failing of [new Error('boom'), new PoolReadError('/p', 403, { code: 'forbidden', message: '' } as never), new Promise(() => {})]) {
      fetchUndeclared.mockResolvedValueOnce(page('old-name'))
      await loadUndeclared('cfg-a', 'old', 'h1', NOW)
      if (failing instanceof Promise) fetchUndeclared.mockReturnValueOnce(failing); else fetchUndeclared.mockRejectedValueOnce(failing)
      void loadUndeclared('cfg-a', 'new', 'h1', NOW + 1000)
      await Promise.resolve(); await Promise.resolve()
      expect(readUndeclared('h1', renamed)).toBeNull()
    }
  })

  it('known: false (an older wwd) is kept as that', async () => {
    fetchUndeclared.mockResolvedValue(page('x', false))
    await loadUndeclared('cfg-a', 'mlab', 'h1', NOW)
    expect(readUndeclared('h1', observationKey('cfg-a', 'mlab'))!.page.known).toBe(false)
  })
})
