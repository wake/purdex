// The host page's 狀態 cards and facts table (monitoring tabs spec M44; plan MON-7b-2): six cards over the pool's facts, and the table of every field. A host that is not ok has no reading, so three cards say so and three stay.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, within, waitFor } from '@testing-library/react'

const fetchPauses = vi.fn()
vi.mock('../../../lib/pool/control-api', async (orig) => ({ ...(await orig<typeof import('../../../lib/pool/control-api')>()), fetchPauses: (...a: unknown[]) => fetchPauses(...a), controlSource: () => 'cfg-a' }))

import { StatusCards } from './StatusCards'
import { FactsTable } from './FactsTable'
import { poolHostOf, SEED_HOST_ID } from '../host-pane-seed'
import { poolHostState } from '../../../lib/pool/host-state'
import { usePoolStore } from '../../../lib/pool/store'
import { useAlertsStore } from '../../../lib/pool/alerts-store'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import type { PoolFacts, PoolHost } from '../../../lib/pool/types'

const t = (k: string, o?: Record<string, string | number>) => useI18nStore.getState().t(k, o)
const NOW = Date.now()

function monitorHost(over: Partial<PoolHost> = {}, mutate?: (f: PoolFacts) => void, host: Partial<MonitorHost> = {}): MonitorHost {
  const pool = poolHostOf(over)
  if (pool.facts && mutate) mutate(pool.facts)
  return { hostId: pool.host_id, alias: pool.alias, daemonHostId: null, state: poolHostState(pool, NOW, NOW), look: { indicator: { shape: 'ring', data: ['load'] } }, poolFailure: null, count: null, local: false, ...host }
}

beforeEach(() => {
  cleanup()
  fetchPauses.mockReset()
  fetchPauses.mockResolvedValue([])
  usePoolStore.getState().reset()
  useAlertsStore.getState().reset()
})
afterEach(() => cleanup())

describe('StatusCards, a host that is ok', () => {
  it('draws the six cards, each with its title', () => {
    render(<StatusCards host={monitorHost()} />)
    for (const id of ['judge', 'worker', 'conn', 'pause', 'ident', 'tags']) expect(screen.getByTestId(`status-card-${id}`)).toBeInTheDocument()
    expect(screen.getAllByTestId(/^status-card-/)).toHaveLength(6)
  })

  it('判斷: the zone word and the pressure and paging it rests on, the sessions room, the composite load and its driver', () => {
    const host = monitorHost({ load: { score: 91, driver: 'disk' } }, (f) => { f.derived.zone = 'normal'; f.mem.pressure = 1; f.mem.page_out_rate = 3; f.derived.sessions_room = 4 })
    render(<StatusCards host={host} />)
    const card = screen.getByTestId('status-card-judge')
    expect(within(card).getByTestId('judge-zone')).toHaveTextContent(t('fleet.zone.normal'))
    expect(card).toHaveTextContent(t('watch.host.status.judge.from', { level: t('pool.level.1'), rate: 3 }))
    expect(card).toHaveTextContent(t('watch.host.status.judge.sessions', { n: 4 }))
    expect(within(card).getByTestId('judge-load')).toHaveTextContent('91')
    expect(within(card).getByTestId('judge-load')).toHaveTextContent(t('pool.driver.disk'))
  })

  it('M8: the composite load held up by the disk marks the disk bar, and only that one', () => {
    const host = monitorHost({ load: { score: 91, driver: 'disk' } }, (f) => { f.disk = [{ mount: '/', free_gb: 5, used_pct: 91, total_bytes: 1, avail_bytes: 1 }] })
    render(<StatusCards host={host} />)
    expect(screen.getByTestId('load-part-disk')).toHaveAttribute('data-driver', 'true')
    for (const id of ['cpu', 'mem', 'pressure', 'paging']) expect(screen.getByTestId(`load-part-${id}`)).toHaveAttribute('data-driver', 'false')
    expect(screen.getByTestId('load-part-disk')).toHaveAttribute('data-counted', 'true')
  })

  it('the main driver is the daemon\'s word, not the largest bar', () => {
    const host = monitorHost({ load: { score: 91, driver: 'cpu' } }, (f) => { f.cpu.idle_pct = 80; f.disk = [{ mount: '/', free_gb: 5, used_pct: 91, total_bytes: 1, avail_bytes: 1 }] })
    render(<StatusCards host={host} />)
    expect(screen.getByTestId('load-part-cpu')).toHaveAttribute('data-driver', 'true')
    expect(screen.getByTestId('load-part-disk')).toHaveAttribute('data-driver', 'false')
  })

  it('a disk under 85 % is drawn faint: it does not count', () => {
    const host = monitorHost({}, (f) => { f.disk = [{ mount: '/', free_gb: 50, used_pct: 60, total_bytes: 1, avail_bytes: 1 }] })
    render(<StatusCards host={host} />)
    expect(screen.getByTestId('load-part-disk')).toHaveAttribute('data-counted', 'false')
  })

  it('a class at 0 says why when the daemon sent the reasons; with none sent, the plain 0 and no reason', () => {
    const withReasons = monitorHost({ headroom_reasons: { heavy: ['gate_full'], light: [] } }, (f) => { f.derived.headroom = { heavy: 0, light: 0 } })
    const { unmount } = render(<StatusCards host={withReasons} />)
    expect(screen.getByTestId('judge-heavy')).toHaveTextContent(t('pool.reason.gate_full'))
    expect(screen.getByTestId('judge-light')).toHaveTextContent(t('pool.reason.unknown'))
    unmount()
    render(<StatusCards host={monitorHost({ headroom_reasons: undefined }, (f) => { f.derived.headroom = { heavy: 0, light: 0 } })} />)
    expect(screen.getByTestId('judge-heavy')).not.toHaveTextContent(t('pool.reason.unknown'))
  })

  it('Worker 與主人: switch, slots, owner rule and the owner reading; no reading (Linux) is said', () => {
    const host = monitorHost({}, (f) => { f.worker = { enabled: true, slots: { heavy: 2, light: 5 }, running: { heavy: 1, light: 0 }, owner_rule: 'idle>=5m' }; f.owner = { active: true, idle_s: 3, reason: 'hid' } })
    const { unmount } = render(<StatusCards host={host} />)
    const card = screen.getByTestId('status-card-worker')
    expect(card).toHaveTextContent(t('watch.host.status.worker.slots', { heavy: 2, light: 5 }))
    expect(card).toHaveTextContent('hid')
    unmount()
    render(<StatusCards host={monitorHost({}, (f) => { f.owner = null })} />)
    expect(screen.getByTestId('status-card-worker')).toHaveTextContent(t('watch.host.status.worker.owner_none'))
  })

  it('連線: the local host says the daemon reads its wwd directly', () => {
    render(<StatusCards host={monitorHost({ liveness: undefined }, undefined, { local: true })} />)
    expect(screen.getByTestId('status-card-conn')).toHaveTextContent(t('watch.host.status.conn.local'))
  })

  it('連線: a peer says its state, since when and the last check', () => {
    const host = monitorHost({ liveness: 'ok', liveness_since: NOW - 3_600_000, liveness_at: NOW - 2000 })
    render(<StatusCards host={host} />)
    const card = screen.getByTestId('status-card-conn')
    expect(card).toHaveTextContent(t('pool.liveness.ok'))
    expect(card).not.toHaveTextContent(t('watch.host.status.conn.local'))
    expect(card).toHaveTextContent(t('watch.host.status.conn.outages_none'))
  })

  it('版本與身分: the version, the wwd host id, and whether a Purdex daemon fronts the host', () => {
    const { unmount } = render(<StatusCards host={monitorHost()} />)
    const card = screen.getByTestId('status-card-ident')
    expect(card).toHaveTextContent('1.0.0-alpha.768')
    expect(card).toHaveTextContent(SEED_HOST_ID)
    expect(screen.getByTestId('ident-daemon')).toHaveTextContent(t('watch.host.status.ident.daemon_no'))
    unmount()
    render(<StatusCards host={monitorHost({}, undefined, { daemonHostId: 'cfg-a' })} />)
    expect(screen.getByTestId('ident-daemon')).not.toHaveTextContent(t('watch.host.status.ident.daemon_no'))
  })

  it('標籤、工具與 fingerprint: labels by group, a tool with no value says so', () => {
    const host = monitorHost({ labels: ['task:build', 'repo:purdex', 'gpu'] }, (f) => { f.tools = { node: '24.1', go: null } })
    render(<StatusCards host={host} />)
    const card = screen.getByTestId('status-card-tags')
    expect(within(card).getByText('task:build')).toHaveAttribute('data-group', 'task')
    expect(within(card).getByText('repo:purdex')).toHaveAttribute('data-group', 'repo')
    expect(within(card).getByText('gpu')).toHaveAttribute('data-group', 'other')
    expect(card).toHaveTextContent('node')
    expect(within(card).getByTestId('tool-go')).toHaveTextContent(t('watch.host.status.tags.none'))
  })
})

describe('StatusCards, 暫停', () => {
  const paused = (f: PoolFacts) => { f.exceptions = [{ name: 'deploy', since: f.at - 60_000, until: f.at + 3_600_000 }] }

  it('says "reading" until the answer is in, then what the pause holds and the pid it follows', async () => {
    let answer: (v: unknown) => void = () => {}
    fetchPauses.mockReturnValue(new Promise((r) => { answer = r }))
    render(<StatusCards host={monitorHost({}, paused)} />)
    expect(screen.getByTestId('pause-loading')).toHaveTextContent(t('watch.host.status.pause.loading'))
    answer([{ name: 'deploy', until: null, pid: 4242, holds: ['heavy'] }])
    await waitFor(() => expect(screen.queryByTestId('pause-loading')).toBeNull())
    const row = screen.getByTestId('pause-deploy')
    expect(row).toHaveTextContent('4242')
    expect(row).toHaveTextContent(t('pool.card.class.heavy'))
  })

  it('what it holds is "not known" when the read gave nothing, never "holds nothing"', async () => {
    fetchPauses.mockResolvedValue(null)
    render(<StatusCards host={monitorHost({}, paused)} />)
    await waitFor(() => expect(screen.getByTestId('pause-deploy')).toHaveTextContent(t('watch.host.status.pause.holds_unknown')))
  })

  it('no pause in force: says none, and reads nothing', () => {
    render(<StatusCards host={monitorHost({}, (f) => { f.exceptions = [] })} />)
    expect(screen.getByTestId('status-card-pause')).toHaveTextContent(t('watch.host.status.pause.none'))
    expect(fetchPauses).not.toHaveBeenCalled()
  })
})

describe('StatusCards, a host that is not ok', () => {
  // The row may still carry the last facts it was given; a state that is not ok does not draw them.
  const down = () => monitorHost({ liveness: 'wwd_down', liveness_since: NOW - 60_000, liveness_at: NOW - 1000, labels: ['task:build'] })

  it('判斷, Worker 與主人 and 暫停 say there is no reading, with the state\'s word', () => {
    render(<StatusCards host={down()} />)
    for (const id of ['judge', 'worker', 'pause']) expect(screen.getByTestId(`status-card-${id}`)).toHaveTextContent(t('watch.host.status.no_reading', { state: t('pool.liveness.wwd_down') }))
    expect(screen.queryByTestId('judge-zone')).toBeNull()
    expect(fetchPauses).not.toHaveBeenCalled()
  })

  it('連線, 版本與身分 and 標籤 are drawn from the pool\'s row', () => {
    render(<StatusCards host={down()} />)
    expect(screen.getByTestId('status-card-conn')).toHaveTextContent(t('pool.liveness.wwd_down'))
    expect(screen.getByTestId('status-card-ident')).toHaveTextContent('1.0.0-alpha.768')
    expect(screen.getByTestId('status-card-tags')).toHaveTextContent('task:build')
  })
})

describe('FactsTable', () => {
  it('every field of the record in two columns, titled with the time of that record', () => {
    const host = monitorHost()
    if (host.state.state !== 'ok') throw new Error('seed is ok')
    render(<FactsTable pool={host.state.pool} facts={host.state.facts} />)
    expect(screen.getByTestId('facts-table')).toBeInTheDocument()
    expect(screen.getAllByTestId('facts-col')).toHaveLength(2)
    expect(screen.getByTestId('facts-title')).toHaveTextContent(/\d\d:\d\d:\d\d/)
    expect(screen.getByText('cpu.idle_pct')).toBeInTheDocument()
  })

  it('a field that is not there is a faint null, not 0', () => {
    const host = monitorHost({}, (f) => { f.mem.swap_total_mb = undefined })
    if (host.state.state !== 'ok') throw new Error('seed is ok')
    render(<FactsTable pool={host.state.pool} facts={host.state.facts} />)
    expect(screen.getByTestId('fact-mem.swap_total_mb')).toHaveTextContent('null')
    expect(screen.getByTestId('fact-mem.swap_total_mb')).toHaveAttribute('data-null', 'true')
  })

  it('the pressure level is followed by its word', () => {
    const host = monitorHost({}, (f) => { f.mem.pressure = 2 })
    if (host.state.state !== 'ok') throw new Error('seed is ok')
    render(<FactsTable pool={host.state.pool} facts={host.state.facts} />)
    expect(screen.getByTestId('fact-mem.pressure')).toHaveTextContent(t('pool.level.2'))
  })
})
