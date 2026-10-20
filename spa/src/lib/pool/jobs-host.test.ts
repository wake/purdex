// spa/src/lib/pool/jobs-host.test.ts — which jobs the host tab's 工作 section shows in which block (plan 7b1-4): on this host now (claimed or running here, any origin), queued from here, and the last five ended here.
import { describe, it, expect } from 'vitest'
import type { PoolJob } from './jobs-types'
import { endedShownOf, hostJobBlocks, HOST_ENDED_SHOWN } from './jobs-host'

const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 't', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: 1, state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})
const claim = (worker: string, since = 10) => ({ worker, attempt: 1, since })
const ended = (id: string, worker: string, finishedAt: number, over: Partial<PoolJob> = {}): PoolJob => job(id, {
  state: 'succeeded', claim: claim(worker),
  result: { exit: 0, summary: null, log: '', host: worker, started_at: finishedAt - 5, finished_at: finishedAt, cache_hit: false, reason: '', superseded_by: null }, ...over,
})
const ids = (jobs: PoolJob[]) => jobs.map((j) => j.id)

describe('the block of jobs on this host now', () => {
  it('a running and a claimed job of this worker, whichever host queued them; running first', () => {
    const blocks = hostJobBlocks([
      job('claimed-here', { origin: 'air26', state: 'claimed', claim: claim('mini', 20) }),
      job('running-here', { origin: 'mserv', state: 'running', claim: claim('mini', 30) }),
      job('running-there', { origin: 'mini', state: 'running', claim: claim('air26') }),
    ], 'mini')
    expect(ids(blocks.running)).toEqual(['running-here', 'claimed-here'])
  })

  it('a queued job of this origin is not on this host yet, and an ended one is no longer', () => {
    const blocks = hostJobBlocks([job('q', { origin: 'mini' }), ended('e', 'mini', 100)], 'mini')
    expect(blocks.running).toEqual([])
  })
})

describe('the block of jobs queued from this host', () => {
  it('only the waiting ones whose origin is this host, wherever they will run (a held one is not waiting: spec 在排)', () => {
    const blocks = hostJobBlocks([
      job('mine', { origin: 'mini' }),
      job('mine-held', { origin: 'mini', manual: 'hold' }),
      job('theirs', { origin: 'air26' }),
      job('mine-running', { origin: 'mini', state: 'running', claim: claim('air26') }),
      ended('mine-ended', 'air26', 100, { origin: 'mini' }),
    ], 'mini')
    expect(ids(blocks.queued)).toEqual(['mine'])
  })
})

describe('the block of the jobs that ended here', () => {
  it('those that ran on this host (not those queued from it), newest first, five at most', () => {
    const jobs = [
      ...[1, 2, 3, 4, 5, 6].map((n) => ended(`e${n}`, 'mini', n * 100, { origin: 'air26' })),
      ended('queued-here-ran-there', 'air26', 900, { origin: 'mini' }),
    ]
    const blocks = hostJobBlocks(jobs, 'mini')
    expect(HOST_ENDED_SHOWN).toBe(5)
    expect(ids(blocks.ended)).toEqual(['e6', 'e5', 'e4', 'e3', 'e2'])
  })

  it('a job that never ran (superseded in the queue) has no host and is in no block of any host', () => {
    const blocks = hostJobBlocks([job('never-ran', { origin: 'mini', state: 'superseded' })], 'mini')
    expect(blocks).toEqual({ running: [], queued: [], ended: [] })
  })

  it('no jobs, no blocks', () => {
    expect(hostJobBlocks([], 'mini')).toEqual({ running: [], queued: [], ended: [] })
  })
})

describe('endedShownOf — the column is about as tall as the process tables (spec M41-3)', () => {
  it.each([
    [0, 0, 5], // 9 → held to 5
    [2, 3, 4], // 9 − 2 − 3
    [1, 5, 4], // 9 − 1 − 3 − 1 (the 「還有」 row)
    [3, 5, 3], // 9 − 3 − 3 − 1 = 2 → 3
    [9, 9, 3], // far past full: still 3
    [0, 3, 5], // exactly three queued: no 「還有」 row: 9 − 0 − 3 = 6 → 5
    [4, 1, 4], // 9 − 4 − 1
  ])('%i running, %i waiting: %i ended', (running, queued, want) => {
    expect(endedShownOf(running, queued)).toBe(want)
  })
})
