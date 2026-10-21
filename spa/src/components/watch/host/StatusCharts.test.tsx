// The 狀態 tab's full charts (monitoring tabs spec M42, M43, M44; plan MON-7c-2): the zone strip, six groups of charts and the two number cards, with one hovered time for all of them.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, within } from '@testing-library/react'

let current: { samples: HostSample[] } = { samples: [] }
vi.mock('../../../lib/pool/use-host-series', () => ({ useHostSeries: () => ({ samples: current.samples, load: 'ok', rev: 0 }) }))

import { StatusCharts } from './StatusCharts'
import { poolHostOf } from '../host-pane-seed'
import { poolHostState } from '../../../lib/pool/host-state'
import { sampleOfFacts, type HostSample } from '../../../lib/pool/host-history'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import type { PoolFacts, PoolHost } from '../../../lib/pool/types'

const t = (k: string, o?: Record<string, string | number>) => useI18nStore.getState().t(k, o)
const NOW = Date.now()

function monitorHost(over: Partial<PoolHost> = {}, host: Partial<MonitorHost> = {}, lastFactsAt = NOW): MonitorHost {
  const pool = poolHostOf(over)
  return { hostId: pool.host_id, alias: pool.alias, daemonHostId: null, state: poolHostState(pool, lastFactsAt, NOW), look: { indicator: { shape: 'ring', data: ['load'] } }, poolFailure: null, count: null, local: false, ...host }
}
/** Three 5 s samples ending now, the user share of the CPU 10, 20, 30. */
function threeSamples(facts: PoolFacts): HostSample[] {
  return [10, 20, 30].map((user, i) => sampleOfFacts({ ...facts, at: NOW - (2 - i) * 5000, cpu: { ...facts.cpu, user_pct: user, system_pct: 5 } }))
}
const groupIds = () => screen.getAllByTestId(/^chart-group-/).map((g) => g.getAttribute('data-testid')!.replace('chart-group-', ''))
const chartIds = (group: string) => within(screen.getByTestId(`chart-group-${group}`)).queryAllByTestId('mini-chart').map((c) => c.getAttribute('data-chart'))

function mount(host: MonitorHost, range: '5m' | '15m' = '5m') {
  return render(<StatusCharts host={host} range={range} now={NOW} sourceAlias="mlab" />)
}

beforeEach(() => {
  cleanup()
  current = { samples: [] }
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ left: 0, width: 600, top: 0, height: 90, right: 600, bottom: 90, x: 0, y: 0, toJSON: () => ({}) })
})
afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('StatusCharts — the groups', () => {
  it('the zone strip first, then six groups in order, with the charts of each (darwin)', () => {
    const host = monitorHost({ platform: 'darwin' })
    if (host.state.state !== 'ok') throw new Error('seed is ok')
    current = { samples: threeSamples(host.state.facts) }
    mount(host)
    expect(screen.getByTestId('zone-strip')).toBeInTheDocument()
    expect(groupIds()).toEqual(['cpu', 'mem', 'disk', 'pool', 'proc', 'host'])
    expect(chartIds('cpu')).toEqual(['percore', 'cpusplit', 'load15'])
    expect(chartIds('mem')).toEqual(['pressure', 'comp', 'decomp', 'swap'])
    expect(chartIds('disk')).toHaveLength(3)
    expect(chartIds('pool')).toEqual(['hr', 'q', 'sr'])
    expect(chartIds('proc')).toEqual(['np', 'nproc', 'resmem'])
    expect(chartIds('host')).toEqual(['wwdcpu', 'wwdrss'])
  })

  it('linux has the PSI in the place of the compressed memory', () => {
    const host = monitorHost({ platform: 'linux' })
    if (host.state.state !== 'ok') throw new Error('seed is ok')
    current = { samples: threeSamples(host.state.facts) }
    mount(host)
    expect(chartIds('mem')).toEqual(['pressure', 'psi', 'decomp', 'swap'])
  })

  it('the group names are words, and the strip comes before the first group', () => {
    const host = monitorHost()
    if (host.state.state !== 'ok') throw new Error('seed is ok')
    current = { samples: threeSamples(host.state.facts) }
    mount(host)
    expect(screen.getByTestId('chart-group-cpu')).toHaveTextContent(t('watch.host.full.group.cpu'))
    const strip = screen.getByTestId('zone-strip')
    expect(strip.compareDocumentPosition(screen.getByTestId('chart-group-cpu')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('the number cards close the last group: the uptime and the latency to the host', () => {
    const host = monitorHost({ rtt_ms: 42 })
    if (host.state.state !== 'ok') throw new Error('seed is ok')
    current = { samples: threeSamples(host.state.facts) }
    mount(host)
    const last = screen.getByTestId('chart-group-host')
    expect(within(last).getByTestId('tile-latency')).toHaveTextContent(t('watch.host.full.latency', { alias: 'mlab', ms: 42 }))
    expect(within(last).getByTestId('tile-uptime')).toBeInTheDocument()
  })

  it('the host the daemon reads directly says so, and a figure that is not there is a dash', () => {
    const host = monitorHost({ rtt_ms: undefined }, { local: true })
    if (host.state.state !== 'ok') throw new Error('seed is ok')
    current = { samples: threeSamples(host.state.facts) }
    const { unmount } = mount(host)
    expect(screen.getByTestId('tile-latency')).toHaveTextContent(t('watch.host.full.latency_local'))
    unmount()
    mount(monitorHost({ rtt_ms: undefined }))
    expect(screen.getByTestId('tile-latency')).toHaveTextContent('—')
  })
})

describe('StatusCharts — one hovered time for all of them (M42)', () => {
  const hoverFirst = () => fireEvent.mouseMove(within(screen.getByTestId('chart-group-cpu')).getAllByTestId('mini-plot')[1], { clientX: 0 })

  it('a hover on one chart moves the legend of every chart to that point', () => {
    const host = monitorHost()
    if (host.state.state !== 'ok') throw new Error('seed is ok')
    current = { samples: threeSamples(host.state.facts) }
    mount(host)
    const userLegend = () => within(screen.getByTestId('chart-group-cpu')).getByTestId('mini-legend-user')
    expect(userLegend()).toHaveTextContent('30%') // the last point, before any hover
    hoverFirst()
    expect(userLegend()).toHaveTextContent('10%')
    // another group's chart follows the same time
    const hoverLines = screen.getAllByTestId('mini-hover-line')
    expect(hoverLines.length).toBeGreaterThan(5)
  })

  it('another range drops the hover', () => {
    const host = monitorHost()
    if (host.state.state !== 'ok') throw new Error('seed is ok')
    current = { samples: threeSamples(host.state.facts) }
    const view = mount(host)
    hoverFirst()
    expect(screen.queryAllByTestId('mini-hover-line').length).toBeGreaterThan(0)
    view.rerender(<StatusCharts host={host} range="15m" now={NOW} sourceAlias="mlab" />)
    expect(screen.queryAllByTestId('mini-hover-line')).toHaveLength(0)
  })

  it('leaving the plot clears it', () => {
    const host = monitorHost()
    if (host.state.state !== 'ok') throw new Error('seed is ok')
    current = { samples: threeSamples(host.state.facts) }
    mount(host)
    hoverFirst()
    fireEvent.mouseLeave(within(screen.getByTestId('chart-group-cpu')).getAllByTestId('mini-plot')[1])
    expect(screen.queryAllByTestId('mini-hover-line')).toHaveLength(0)
  })
})

describe('StatusCharts — a host that is not ok', () => {
  it('the strip and the charts run slanted from the last reading to the right edge', () => {
    const host = monitorHost({ liveness: 'wwd_down', liveness_since: NOW - 60_000, liveness_at: NOW - 1000 }, {}, NOW - 60_000)
    current = { samples: [] }
    mount(host)
    expect(screen.getByTestId('zone-dead')).toHaveAttribute('data-tone', 'warning')
    expect(screen.getAllByTestId('mini-dead').length).toBeGreaterThan(0)
  })

  it('a host that is ok has no slanted part', () => {
    const host = monitorHost()
    if (host.state.state !== 'ok') throw new Error('seed is ok')
    current = { samples: threeSamples(host.state.facts) }
    mount(host)
    expect(screen.queryByTestId('zone-dead')).toBeNull()
    expect(screen.queryAllByTestId('mini-dead')).toHaveLength(0)
  })
})
