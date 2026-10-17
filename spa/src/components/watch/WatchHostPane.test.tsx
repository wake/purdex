import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { WatchHostPane } from './WatchHostPane'
import { useFleetStore } from '../../lib/fleet/store'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { fast, host, primaryAnswer } from '../../lib/fleet/__fixtures__/builders'
import type { FleetProc } from '../../lib/fleet/types'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const st = () => useFleetStore.getState()

function load(hosts: ReturnType<typeof host>[], primaryCfg = 'cfg-a') {
  const gen = st().begin(primaryCfg)
  st().setSupport(primaryCfg, gen, 'yes')
  st().applyFetched(primaryCfg, gen, primaryAnswer(1, hosts))
}

const proc = (pid: number, name: string, cpu: number, rss_mb: number): FleetProc => ({ pid, name, cpu, rss_mb })
const slow = (cpu: FleetProc[], mem: FleetProc[]) => ({ at: 1, top_cpu: cpu, top_mem: mem })

function renderPane(fleetHostId: string) {
  return render(<WatchHostPane pane={{ id: `watch-host-${fleetHostId}.p`, content: { kind: 'watch-host', fleetHostId } }} isActive />)
}

beforeEach(() => {
  st().reset()
  useHostStore.setState({
    hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } },
    hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: {},
  } as never)
})

describe('WatchHostPane — header', () => {
  it.each([
    ['normal', 'fleet.zone.normal', 'text-status-success-text'],
    ['warning', 'fleet.zone.warning', 'text-status-warning-text'],
    ['danger', 'fleet.zone.danger', 'text-status-error-text'],
  ] as const)('shows the %s zone word in its colour', (zone, key, cls) => {
    load([host('d-a', { alias: 'air26', fast: fast({ zone }) })])
    renderPane('d-a')
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('air26')
    const word = screen.getByTestId('zone-text')
    expect(word).toHaveTextContent(t(key))
    expect(word.className).toContain(cls)
    expect(screen.getByTestId('host-badge')).toBeInTheDocument()
  })

  it('offline: the word 離線 and the last successful time', () => {
    load([host('d-a', { alias: 'air26' }), host('p1', { kind: 'probe', alias: 'air19', status: 'offline', error: 'unreachable', fast: null, last_ok_at: Date.UTC(2026, 9, 16, 3, 4, 5) })])
    renderPane('p1')
    expect(screen.getByTestId('zone-text')).toHaveTextContent(t('fleet.zone.offline'))
    expect(screen.getByTestId('measure-source').textContent).toContain(new Date(Date.UTC(2026, 9, 16, 3, 4, 5)).toLocaleString())
  })

  it('names the primary as the measuring host', () => {
    load([host('d-a', { alias: 'air26' }), host('p1', { kind: 'probe', alias: 'air19' })])
    renderPane('p1')
    expect(screen.getByTestId('measure-source')).toHaveTextContent(t('watch.host.measured', { alias: 'air26' }))
  })
})

describe('WatchHostPane — processes', () => {
  it('two tables, CPU top and memory top, from slow.top_cpu / top_mem', () => {
    load([host('d-a', { alias: 'air26', slow: slow([proc(11, 'node', 91.5, 100), proc(12, 'go', 40, 200)], [proc(21, 'Chrome', 5, 4096), proc(22, 'vite', 3, 2048)]) })])
    renderPane('d-a')
    const cpu = screen.getByTestId('procs-cpu')
    expect(within(cpu).getAllByRole('row').slice(1).map((r) => r.textContent)).toEqual(['11node91.5%', '12go40%'])
    const mem = screen.getByTestId('procs-mem')
    expect(within(mem).getAllByRole('row').slice(1).map((r) => r.textContent)).toEqual(['21Chrome4,096 MB', '22vite2,048 MB'])
  })

  it('offline: both tables say there is no data', () => {
    load([host('d-a', { alias: 'air26' }), host('p1', { kind: 'probe', alias: 'air19', status: 'offline', fast: null, slow: slow([proc(1, 'x', 1, 1)], []) })])
    renderPane('p1')
    expect(screen.getAllByText(t('watch.host.offline_no_data'))).toHaveLength(2)
    expect(screen.queryByText('x')).toBeNull()
  })
})

describe('WatchHostPane — empty states', () => {
  it('no snapshot at all', () => {
    renderPane('d-a')
    expect(screen.getByText(t('watch.host.no_fleet'))).toBeInTheDocument()
  })

  it('a host the snapshot does not list (a tab that came by sync)', () => {
    load([host('d-a', { alias: 'air26' })])
    renderPane('gone')
    expect(screen.getByText(t('watch.host.no_host'))).toBeInTheDocument()
    expect(screen.queryByTestId('zone-text')).toBeNull()
  })
})
