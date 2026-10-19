// spa/src/hooks/useTabQueued.test.ts — WWD-7b1-6: the tab's hourglass is lit by a pool job of its session (queued, claimed or running) as well as by a gate waiter, until WWD-9.
import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTabQueued } from './useTabQueued'
import { useJobsStore } from '../lib/pool/jobs-store'
import { usePoolStore } from '../lib/pool/store'
import { useFleetStore } from '../lib/fleet/store'
import type { PoolJob } from '../lib/pool/jobs-types'
import type { TabAgentPane } from '../lib/agent-lights/tab-aggregate'

const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 'spa-tsc', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: 'SESS-1', ref: '', team: '', host: '' }, created_at: Date.now(), state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})
const seed = (jobs: PoolJob[]) => useJobsStore.setState({ byOrigin: { mlab: { jobs: Object.fromEntries(jobs.map((j) => [j.id, j])), cursor: '', epoch: '', via: 'd' } }, errors: {} } as never)
const pane = (key: string, sessionId?: string): TabAgentPane => ({ key, hostId: 'h1', sessionId } as unknown as TabAgentPane)

beforeEach(() => {
  useJobsStore.getState().reset()
  usePoolStore.setState({ hosts: [] } as never)
  useFleetStore.setState({ snapshot: null, primaryHostId: null } as never)
})

describe('useTabQueued with pool jobs', () => {
  it.each([
    ['queued', { state: 'queued' }],
    ['claimed', { state: 'claimed', claim: { worker: 'air26', attempt: 1, since: 1 } }],
    ['running elsewhere', { state: 'running', claim: { worker: 'air26', attempt: 1, since: 1 } }],
  ])('a %s job of the pane\'s session lights it', (_n, over) => {
    seed([job('j1', over as Partial<PoolJob>)])
    const { result } = renderHook(() => useTabQueued([pane('k1', 'sess-1')])) // the session id matches whatever the case
    expect([...result.current.keys]).toEqual(['k1'])
    expect(result.current.jobs.map((j) => j.jobId)).toEqual(['j1'])
    expect(result.current.waiters).toEqual([])
  })

  it('two jobs of one session: both are in the mark', () => {
    seed([job('a'), job('b', { state: 'running', claim: { worker: 'air26', attempt: 1, since: 1 } })])
    const { result } = renderHook(() => useTabQueued([pane('k1', 'SESS-1')]))
    expect(result.current.jobs.map((j) => j.jobId).sort()).toEqual(['a', 'b'])
  })

  it('an empty submitter.session lights nothing (every job from before #3477), and neither does a pane with no session', () => {
    seed([job('old', { submitter: { session: '', ref: '', team: '', host: '' } })])
    expect([...renderHook(() => useTabQueued([pane('k1', 'SESS-1')])).result.current.keys]).toEqual([])
    seed([job('j')])
    expect([...renderHook(() => useTabQueued([pane('k1', undefined)])).result.current.keys]).toEqual([])
  })

  it('another session\'s job and an ended job light nothing', () => {
    seed([job('other', { submitter: { session: 'SESS-2', ref: '', team: '', host: '' } }), job('done', { state: 'succeeded' })])
    expect([...renderHook(() => useTabQueued([pane('k1', 'SESS-1')])).result.current.keys]).toEqual([])
  })

  it('lights up when the job arrives and goes out when it ends', () => {
    const { result } = renderHook(() => useTabQueued([pane('k1', 'SESS-1')]))
    expect(result.current.keys.size).toBe(0)
    act(() => seed([job('j1')]))
    expect([...result.current.keys]).toEqual(['k1'])
    act(() => seed([job('j1', { state: 'succeeded' })]))
    expect(result.current.keys.size).toBe(0)
  })
})
