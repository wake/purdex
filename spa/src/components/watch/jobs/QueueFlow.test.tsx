// spa/src/components/watch/jobs/QueueFlow.test.tsx — the queue flow and the last ended jobs (monitoring tabs spec M24, M26–M34): the summary, the queue pool, the worker pool, the reasons, and that it is static.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'
import { QueueFlow, type FlowMonitorHost } from './QueueFlow'
import { RecentEnded } from './RecentEnded'
import { queueEntries } from '../../../lib/pool/jobs-lanes'
import { poolHostState } from '../../../lib/pool/host-state'
import { parseFacts } from '../../../lib/pool/parse'
import { recentEndedOf } from '../../../lib/pool/queue-flow'
import type { PoolJob } from '../../../lib/pool/jobs-types'
import type { PoolFacts, PoolHost } from '../../../lib/pool/types'
import { useI18nStore } from '../../../stores/useI18nStore'
import darwinJson from '../../../lib/pool/__fixtures__/facts_darwin.json'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const NOW = new Date(2026, 9, 21, 16, 0).getTime()
const base = parseFacts(darwinJson)!

const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 'test', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: NOW - 5_000, state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})
const claim = (worker: string, since = NOW - 65_000) => ({ worker, attempt: 1, since })
const running = (id: string, worker: string, over: Partial<PoolJob> = {}) => job(id, { state: 'running', claim: claim(worker), ...over })

interface Spec { enabled?: boolean; slots?: { heavy: number; light: number }; reasons?: { heavy: string[]; light: string[] }; exceptions?: PoolFacts['exceptions']; liveness?: PoolHost['liveness']; labels?: string[] }
function host(alias: string, s: Spec = {}): FlowMonitorHost {
  const worker = { enabled: s.enabled ?? true, slots: s.slots ?? { heavy: 2, light: 2 }, owner_rule: '' }
  const facts: PoolFacts = { ...base, alias, host_id: `id-${alias}`, at: NOW, worker: { ...worker, running: { heavy: 0, light: 0 } }, exceptions: s.exceptions ?? [] }
  const pool: PoolHost = {
    host_id: `id-${alias}`, alias, version: '1', proto: 1, platform: 'darwin', arch: 'arm64', facts, reachable: true, labels: s.labels ?? ['os:darwin'], worker,
    ...(s.reasons ? { headroom_reasons: s.reasons } : {}), liveness: s.liveness ?? 'ok',
  }
  return { hostId: pool.host_id, alias, state: poolHostState(pool, NOW, NOW), look: { indicator: { shape: 'ring', data: ['load'] } } }
}

function mount(hosts: FlowMonitorHost[], jobs: PoolJob[], selected: string | null = null, onSelect: (k: string | null) => void = () => {}) {
  const entries = queueEntries(jobs, NOW, hosts.map((h) => h.state.pool))
  return render(<QueueFlow hosts={hosts} jobs={jobs} entries={entries} now={NOW} selected={selected} onSelect={onSelect} />)
}

beforeEach(() => cleanup())
afterEach(() => cleanup())

describe('the worker pool', () => {
  it('M27: a worker that is on has a cell with slots; one that is off is named on the line below, and appears once it is on', () => {
    mount([host('mlab'), host('mserv', { enabled: false })], [])
    expect(document.querySelectorAll('[data-wk]')).toHaveLength(1)
    expect(screen.getByTestId('flow-off')).toHaveTextContent(t('watch.jobs.flow.off_workers', { names: 'mserv' }))
  })

  it('two or more workers that are off are joined with the locale\'s separator', () => {
    mount([host('mlab'), host('mserv', { enabled: false }), host('air26', { enabled: false })], [])
    expect(screen.getByTestId('flow-off')).toHaveTextContent(t('watch.jobs.flow.off_workers', { names: `mserv${t('watch.jobs.flow.name_sep')}air26` }))
  })

  it('a job is a card in the heavy or light row of the worker holding it, free slots are 「空」, a running one has its time and a claimed one says 「準備中」', () => {
    mount([host('mlab')], [running('r', 'mlab'), job('p', { state: 'claimed', claim: claim('mlab') }), running('l', 'mlab', { slot: 'light' })])
    const cell = document.querySelector('[data-wk]') as HTMLElement
    const heavy = within(cell).getByTestId('flow-slot-heavy')
    expect(heavy.querySelectorAll('[data-j]')).toHaveLength(2)
    expect(within(heavy).queryAllByTestId('flow-free')).toHaveLength(0)
    expect(within(cell).getByTestId('flow-slot-light').querySelectorAll('[data-j]')).toHaveLength(1)
    expect(within(cell).getByTestId('flow-slot-light')).toHaveTextContent('1:05')
    expect(heavy).toHaveTextContent(t('watch.jobs.preparing'))
    expect(within(cell).getAllByTestId('flow-free')).toHaveLength(1)
  })

  it('M31: a paused host does not take work: 「不接」 and the reason with the time and the name', () => {
    const until = new Date(2026, 9, 21, 16, 21).getTime()
    mount([host('mlab', { reasons: { heavy: ['exception:watcher-766'], light: [] }, exceptions: [{ name: 'watcher-766', since: 1, until }] })], [])
    const heavy = screen.getByTestId('flow-slot-heavy')
    expect(within(heavy).getByTestId('flow-busy')).toHaveTextContent(t('watch.jobs.flow.busy'))
    expect(within(heavy).getByTestId('flow-busy-line')).toHaveTextContent(t('watch.jobs.flow.busy_line', { cls: t('watch.jobs.flow.class.heavy'), reason: t('watch.jobs.flow.paused_until', { time: '16:21', name: 'watcher-766' }) }))
    expect(screen.getByTestId('flow-slot-light')).not.toHaveAttribute('data-busy')
  })

  it('M24: a worker that cannot be read keeps the job it holds, its slots are 「—」 and it says why', () => {
    mount([host('mserv', { liveness: 'wwd_down' })], [running('r', 'mserv')])
    const cell = document.querySelector('[data-wk]') as HTMLElement
    expect(cell.querySelectorAll('[data-j]')).toHaveLength(1)
    expect(cell).toHaveTextContent(t('watch.jobs.flow.slots', { hu: 1, ht: '—', lu: 0, lt: '—' }))
    expect(cell).toHaveTextContent(t('pool.liveness.wwd_down'))
  })
})

describe('the queue pool', () => {
  it('a cell per host with the jobs it sent, in the display order, the longest wait yellow past two minutes', () => {
    mount([host('mlab'), host('mserv')], [job('old', { created_at: NOW - 125_000 }), job('new', { created_at: NOW - 5_000 }), job('other', { origin: 'mserv' })])
    const cells = document.querySelectorAll('[data-pool]')
    expect(cells).toHaveLength(2)
    expect(Array.from(cells[0].querySelectorAll('[data-q]')).map((e) => e.getAttribute('data-q'))).toEqual(['mlab/old', 'mlab/new'])
    const waits = within(cells[0] as HTMLElement).getAllByTestId('flow-wait')
    expect(waits[0].className).toContain('text-status-warning-text')
    expect(waits[1].className).not.toContain('text-status-warning-text')
    expect(cells[1]).toHaveTextContent('mserv')
  })

  it('M32: every host that could take it is busy: one line with the count and each host\'s reason', () => {
    const hosts = [host('mlab', { reasons: { heavy: ['exception:watcher-766'], light: [] } }), host('air26', { reasons: { heavy: ['gate_full'], light: [] } })]
    mount(hosts, [job('a'), job('b')])
    const line = screen.getByTestId('flow-why')
    expect(line).toHaveTextContent(t('watch.jobs.flow.why_prefix', { n: 2, why: `mlab ${t('pool.reason.exception', { name: 'watcher-766' })}・air26 ${t('pool.reason.gate_full')}` }))
    expect(line.className).not.toContain('text-status-error-text')
  })

  it('the red line is a red why line naming the labels; a hold says 「保留」 and is dimmed', () => {
    mount([host('mlab')], [job('php', { labels: ['os:linux'] }), job('h', { manual: 'hold' })])
    const lines = screen.getAllByTestId('flow-why')
    expect(lines.map((l) => l.textContent)).toEqual(expect.arrayContaining([expect.stringContaining('os:linux'), expect.stringContaining(t('watch.jobs.flow.why.held'))]))
    expect(lines.find((l) => l.textContent?.includes('os:linux'))!.className).toContain('text-status-error-text')
    expect(q('mlab/h').className).toContain('opacity-50')
    expect(q('mlab/h')).toHaveTextContent(t('watch.jobs.flow.held_tag'))
  })

  it('M24: a host that cannot be read draws no jobs, only 「讀不到」 and why, even when the store still has its jobs', () => {
    mount([host('mserv', { liveness: 'wwd_down' })], [job('old', { origin: 'mserv' })])
    expect(document.querySelectorAll('[data-q]')).toHaveLength(0)
    expect(document.querySelector('[data-pool]')).toHaveTextContent(t('watch.jobs.flow.unreadable', { state: t('pool.liveness.wwd_down') }))
  })

  it('what a host sent that runs elsewhere is counted on its cell', () => {
    mount([host('mlab'), host('air26')], [running('r', 'air26', { origin: 'mlab' })])
    expect(document.querySelector('[data-pool="id-mlab"]')).toHaveTextContent(t('watch.jobs.flow.away', { n: 1 }))
  })
})

const q = (key: string) => document.querySelector(`[data-q="${key}"]`) as HTMLElement

describe('the summary and the selection', () => {
  it('M34: queued with the heavy and light split, running, preparing, the longest wait and holds', () => {
    mount([host('mlab')], [job('a', { created_at: NOW - 130_000 }), job('b', { slot: 'light' }), job('h', { manual: 'hold' }), running('r', 'mlab'), job('p', { state: 'claimed', claim: claim('mlab') })])
    const s = screen.getByTestId('flow-summary')
    expect(s).toHaveTextContent(t('watch.jobs.flow.queued', { n: 2 }))
    expect(s).toHaveTextContent(t('watch.jobs.flow.split', { heavy: 1, light: 1 }))
    expect(s).toHaveTextContent(t('watch.jobs.flow.running', { n: 1 }))
    expect(s).toHaveTextContent(t('watch.jobs.flow.preparing', { n: 1 }))
    expect(s).toHaveTextContent(t('watch.jobs.flow.held', { n: 1 }))
    expect(screen.getByTestId('flow-longest')).toHaveTextContent('2:10')
    expect(screen.getByTestId('flow-longest').className).toContain('text-status-warning-text')
    expect(s.querySelectorAll('[data-testid^="queue-seg-"]')).toHaveLength(10)
  })

  it('nothing waiting: the longest wait is 「—」 and not yellow', () => {
    mount([host('mlab')], [])
    expect(screen.getByTestId('flow-longest')).toHaveTextContent('—')
    expect(screen.getByTestId('flow-longest').className).not.toContain('text-status-warning-text')
  })

  it('a card and a row select the job (aria-pressed), the same press lets go', () => {
    const onSelect = vi.fn()
    const { rerender } = mount([host('mlab')], [job('a'), running('r', 'mlab')], null, onSelect)
    fireEvent.click(q('mlab/a'))
    expect(onSelect).toHaveBeenLastCalledWith('mlab/a')
    fireEvent.click(document.querySelector('[data-j="mlab/r"]')!)
    expect(onSelect).toHaveBeenLastCalledWith('mlab/r')
    const hosts = [host('mlab')]
    const jobs = [job('a'), running('r', 'mlab')]
    rerender(<QueueFlow hosts={hosts} jobs={jobs} entries={queueEntries(jobs, NOW, hosts.map((h) => h.state.pool))} now={NOW} selected="mlab/a" onSelect={onSelect} />)
    expect(q('mlab/a')).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(q('mlab/a'))
    expect(onSelect).toHaveBeenLastCalledWith(null)
  })

  it('M28: static — no animation or transition class anywhere in the tree', () => {
    mount([host('mlab'), host('mserv', { enabled: false })], [running('r', 'mlab'), job('p', { state: 'claimed', claim: claim('mlab') }), job('q')])
    const html = document.body.innerHTML
    expect(html).not.toMatch(/animate-/)
    expect(html).not.toMatch(/transition/)
  })
})

describe('the last ended jobs', () => {
  const ended = (id: string, finished: number, over: Partial<PoolJob> = {}): PoolJob => job(id, {
    state: 'succeeded', created_at: 1,
    result: { exit: 0, summary: null, log: '', host: 'mserv', started_at: finished - 125_000, finished_at: finished, cache_hit: false, reason: '', superseded_by: null }, ...over,
  })
  const view = (jobs: PoolJob[], selected: string | null = null, onSelect = vi.fn()) => render(<RecentEnded jobs={recentEndedOf(jobs)} now={NOW} looks={{}} selected={selected} onSelect={onSelect} />)

  it('five rows, newest first, with the result, where it ran, how long it took and its class and slot', () => {
    view(Array.from({ length: 7 }, (_, i) => ended(`e${i}`, NOW - 600_000 + i * 1000)))
    const rows = screen.getAllByTestId('flow-recent-row')
    expect(rows.map((r) => r.getAttribute('data-job-id'))).toEqual(['e6', 'e5', 'e4', 'e3', 'e2'])
    expect(rows[0]).toHaveTextContent('mserv')
    expect(within(rows[0]).getByTestId('flow-recent-took')).toHaveTextContent('2:05')
    expect(rows[0]).toHaveTextContent(t('watch.jobs.flow.recent_class', { cls: t('watch.jobs.class.dev'), slot: t('watch.jobs.flow.class.heavy') }))
  })

  it('none ended: it says so', () => {
    view([])
    expect(screen.getByTestId('flow-recent-none')).toBeInTheDocument()
  })

  it('a row selects the job', () => {
    const onSelect = vi.fn()
    view([ended('e1', NOW - 1000)], null, onSelect)
    fireEvent.click(screen.getByTestId('flow-recent-row'))
    expect(onSelect).toHaveBeenCalledWith('mlab/e1')
  })
})
