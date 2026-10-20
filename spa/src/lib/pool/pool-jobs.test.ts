// spa/src/lib/pool/pool-jobs.test.ts — the pool's jobs for the screens that show them, and the gate "can the pool be read" (monitoring tabs MON-3a, M25).
import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { poolJobsOf, poolReadable, usePoolJobs, usePoolReadable } from './pool-jobs'
import { usePoolStore } from './store'
import { useJobsStore } from './jobs-store'
import type { PoolHost } from './types'
import type { PoolJob } from './jobs-types'

const job = (id: string, origin: string): PoolJob => ({ id, origin }) as PoolJob
const byOrigin = {
  mlab: { jobs: { a: job('a', 'mlab'), b: job('b', 'mlab') }, cursor: '', epoch: '', via: '' },
  air26: { jobs: { c: job('c', 'air26') }, cursor: '', epoch: '', via: '' },
  gone: { jobs: { d: job('d', 'gone') }, cursor: '', epoch: '', via: '' },
}
const host = (alias: string) => ({ alias }) as PoolHost
const ids = (jobs: PoolJob[]) => jobs.map((j) => j.id).sort()

describe('poolJobsOf', () => {
  it('a list: the jobs of the origins it names, no others', () => {
    expect(ids(poolJobsOf(byOrigin, [host('mlab'), host('air26')], true, false))).toEqual(['a', 'b', 'c'])
  })
  it('an answered empty list: the pool really has no host, so no job', () => {
    expect(poolJobsOf(byOrigin, [], true, false)).toEqual([])
  })
  it('not answered yet and not an old daemon: what was read is kept (the jobs page wants that)', () => {
    expect(ids(poolJobsOf(byOrigin, [], false, false))).toEqual(['a', 'b', 'c', 'd'])
  })
  it('not answered and an old daemon: none', () => {
    expect(poolJobsOf(byOrigin, [], false, true)).toEqual([])
  })
})

describe('poolReadable', () => {
  it.each([
    ['a source answered', { sourceHostId: 'cfg-a' }, true],
    ['every source failed or none is connected', { sourceHostId: null }, false],
  ])('%s', (_name, s, want) => {
    expect(poolReadable(s)).toBe(want)
  })
})

describe('the hooks', () => {
  beforeEach(() => {
    usePoolStore.setState({ hosts: [], sourceHostId: null, oldDaemon: false } as never)
    useJobsStore.setState({ byOrigin } as never)
  })
  it('usePoolJobs follows the pool store and the jobs store', () => {
    usePoolStore.setState({ hosts: [host('mlab')], sourceHostId: 'cfg-a' } as never)
    expect(ids(renderHook(() => usePoolJobs()).result.current)).toEqual(['a', 'b'])
  })
  it('usePoolReadable is the gate: false until a source answers', () => {
    expect(renderHook(() => usePoolReadable()).result.current).toBe(false)
    usePoolStore.setState({ sourceHostId: 'cfg-a' } as never)
    expect(renderHook(() => usePoolReadable()).result.current).toBe(true)
  })
})
