// spa/src/hooks/useTabQueued.test.ts — WWD-7b1-6: the tab's hourglass is lit by a pool job of its session (queued, claimed or running). The old gate's waiters are not read any more (MON-12a-4).
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTabQueued } from './useTabQueued'
import { queueEntries } from '../lib/pool/jobs-lanes'

vi.mock('../lib/pool/jobs-lanes', async (orig) => {
  const m = await orig<typeof import('../lib/pool/jobs-lanes')>()
  return { ...m, queueEntries: vi.fn(m.queueEntries) }
})
import { useJobsStore } from '../lib/pool/jobs-store'
import { usePoolStore } from '../lib/pool/store'
import type { PoolJob } from '../lib/pool/jobs-types'
import type { TabAgentPane } from '../lib/agent-lights/tab-aggregate'

const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 'spa-tsc', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: 'SESS-1', ref: '', team: '', host: '' }, created_at: Date.now(), state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})
const seed = (jobs: PoolJob[]) => useJobsStore.setState({ byOrigin: { mlab: { jobs: Object.fromEntries(jobs.map((j) => [j.id, j])), cursor: '', epoch: '', via: 'd' } }, errors: {} } as never)
let queueEntriesCalls = 0
const pane = (key: string, sessionId?: string): TabAgentPane => ({ key, hostId: 'h1', sessionId } as unknown as TabAgentPane)

beforeEach(() => {
  useJobsStore.getState().reset()
  usePoolStore.setState({ hosts: [] } as never)
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

  it('a pane gets only the jobs of its own session, though another session\'s job comes first', () => {
    seed([
      job('theirs', { submitter: { session: 'SESS-2', ref: '', team: '', host: '' }, state: 'running', claim: { worker: 'air26', attempt: 1, since: 1 } }),
      job('mine'),
    ])
    const { result } = renderHook(() => useTabQueued([pane('k1', 'SESS-1')]))
    expect(result.current.jobs.map((j) => j.jobId)).toEqual(['mine'])
    const two = renderHook(() => useTabQueued([pane('k1', 'SESS-1'), pane('k2', 'SESS-2')])).result.current
    expect(two.jobs.map((j) => j.jobId).sort()).toEqual(['mine', 'theirs'])
    expect([...two.keys].sort()).toEqual(['k1', 'k2'])
  })

  // codex R1 (P2): the place reads each origin's zone, so a zone that moves (or hosts that load after the jobs) changes it with no jobs update
  it('the place follows a host\'s zone without any change in the jobs', () => {
    const host = (alias: string, zone: string) => ({ alias, labels: [], worker: { enabled: true }, facts: { derived: { zone } } }) as never
    // two queued dev jobs of different origins, queued 1 s apart; the older one (mlab) goes first while both zones are ok
    useJobsStore.setState({ byOrigin: {
      mlab: { jobs: { a: job('a', { origin: 'mlab', created_at: 1000 }) }, cursor: '', epoch: '', via: 'd' },
      mserv: { jobs: { b: job('b', { origin: 'mserv', created_at: 2000, submitter: { session: 'SESS-2', ref: '', team: '', host: '' } }) }, cursor: '', epoch: '', via: 'd' },
    }, errors: {} } as never)
    act(() => usePoolStore.setState({ hosts: [host('mlab', 'ok'), host('mserv', 'ok')] } as never))
    const { result } = renderHook(() => useTabQueued([pane('k2', 'SESS-2')]))
    expect(result.current.jobs[0].place).toBe(2)
    // mserv goes into danger (+400): its job now goes first, though no job changed
    act(() => usePoolStore.setState({ hosts: [host('mlab', 'ok'), host('mserv', 'danger')] } as never))
    expect(result.current.jobs[0].place).toBe(1)
  })

  // codex R2 (high): every tab runs this hook, so the whole queue's order must not be worked out once per tab
  it('many tabs on one jobs picture share one computation', () => {
    const jobs = Array.from({ length: 30 }, (_, i) => job(`j${i}`, { submitter: { session: `SESS-${i}`, ref: '', team: '', host: '' }, created_at: 1000 + i }))
    seed(jobs)
    vi.mocked(queueEntries).mockClear()
    const views = jobs.map((_, i) => renderHook(() => useTabQueued([pane(`k${i}`, `SESS-${i}`)])))
    expect(views.every((v) => v.result.current.keys.size === 1)).toBe(true)
    expect(queueEntries).toHaveBeenCalledTimes(1) // 30 tabs, one display order
  })

  // codex R2 (medium): the place counts whole minutes waited, so a tab with a job mark re-reads it when the minute turns; a tab without one holds no timer
  it('a tab with a job mark re-reads the picture when the minute turns, and one without holds no timer', () => {
    vi.useFakeTimers()
    try {
      vi.setSystemTime(10 * 60_000)
      seed([job('j1', { created_at: 10 * 60_000 })])
      const withMark = renderHook(() => useTabQueued([pane('k1', 'SESS-1')]))
      const without = renderHook(() => useTabQueued([pane('k2', 'SESS-9')]))
      expect(withMark.result.current.keys.size).toBe(1)
      expect(without.result.current.keys.size).toBe(0)
      queueEntriesCalls = vi.mocked(queueEntries).mock.calls.length
      act(() => { vi.advanceTimersByTime(60_000) })
      // the minute turned: only the tab that has a mark was re-rendered, and the picture was worked out again for the new minute
      expect(vi.mocked(queueEntries).mock.calls.length).toBe(queueEntriesCalls + 1)
      expect(vi.getTimerCount()).toBeGreaterThan(0)
      withMark.unmount()
      expect(vi.getTimerCount()).toBe(0) // the only subscriber was the tab with a mark
      without.unmount()
    } finally {
      vi.useRealTimers()
    }
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
