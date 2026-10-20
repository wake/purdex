// spa/src/components/watch/WatchHostPane.test.tsx — the host page's shell (monitoring tabs spec M41 header, M22 banner; plan MON-6f-2): the header, the four sub-tabs, the range switch, and the banner of a host that is not ok on every sub-tab.
// The bodies of 監控 and 設定 are mocked (they have their own tests); the hosts are the pool's.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within, act } from '@testing-library/react'

vi.mock('../../lib/pool/jobs-driver-live', () => ({ watchJobs: vi.fn(() => () => {}), jobsDaemons: () => [] }))
const monitorProps = vi.fn()
vi.mock('./host/MonitorTab', () => ({ MonitorTab: (p: unknown) => { monitorProps(p); return <div data-testid="mock-monitor" /> } }))
const settingsProps = vi.fn()
vi.mock('./WatchSettings', () => ({ WatchSettings: (p: unknown) => { settingsProps(p); return <div data-testid="mock-settings" /> } }))
const fetchPoolHosts = vi.fn()
vi.mock('../../lib/pool/api', async (orig) => ({ ...(await orig<typeof import('../../lib/pool/api')>()), fetchPoolHosts: (...a: unknown[]) => fetchPoolHosts(...a) }))

import { WatchHostPane } from './WatchHostPane'
import { poolHostOf, seedPool, SEED_HOST_ID } from './host-pane-seed'
import { clearViewMemory, readHostRange, readHostTab, writeHostRange, writeHostTab } from '../../lib/watch/view-memory'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { fingerprintOf } from '../../lib/nex/nex-host-effects'
import { clock } from '../../lib/pool/words'
import type { PoolHost } from '../../lib/pool/types'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const NOW = Date.now()
const down = (liveness: 'wwd_down' | 'host_down' | 'unknown'): PoolHost =>
  poolHostOf({ liveness, liveness_at: NOW - 3000, liveness_since: NOW - 600_000, facts: null })

function mount(hosts: PoolHost[], content: Record<string, unknown> = { kind: 'watch-host', poolHostId: SEED_HOST_ID }) {
  fetchPoolHosts.mockResolvedValue(hosts)
  seedPool(hosts)
  return render(<WatchHostPane pane={{ id: 'p', content } as never} isActive />)
}
const sub = (k: string) => fireEvent.click(within(screen.getByTestId('page-switch')).getByRole('button', { name: t(`watch.host.page.${k}`) }))

beforeEach(() => {
  cleanup(); clearViewMemory(); monitorProps.mockReset(); settingsProps.mockReset(); fetchPoolHosts.mockReset()
  useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } }, hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: { 'cfg-a': { status: 'connected' } } } as never)
  useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', fetchedAt: Date.now(), daemonCapabilities: ['pool.v1'], wwd: { hostId: SEED_HOST_ID }, fingerprint: fingerprintOf('cfg-a') } } } as never)
})
afterEach(() => clearViewMemory())

describe('the header and the sub-tabs of a host that is ok', () => {
  it('shows alias, zone word, the wwd line, four sub-tabs; 監控 is the default and gets the range (30 分) and the pool host', () => {
    mount([poolHostOf()])
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('air26')
    expect(screen.getByTestId('zone-text').textContent).not.toBe('')
    expect(screen.getByTestId('measure-source')).toHaveTextContent('wwd 1.0.0-alpha.768')
    const names = within(screen.getByTestId('page-switch')).getAllByRole('button').map((b) => b.textContent)
    expect(names).toEqual(['monitor', 'status', 'procs', 'settings'].map((k) => t(`watch.host.page.${k}`)))
    expect(screen.queryByTestId('host-banner')).toBeNull()
    const p = monitorProps.mock.calls.at(-1)![0] as { host: { hostId: string }; range: string }
    expect(p.host.hostId).toBe(SEED_HOST_ID)
    expect(p.range).toBe('30m')
  })

  it('the range switch shows on 監控 and 狀態 only, and the choice goes to the view memory', () => {
    mount([poolHostOf()])
    fireEvent.click(within(screen.getByTestId('range-switch')).getByRole('button', { name: t('watch.host.range.6h') }))
    expect(readHostRange(SEED_HOST_ID)).toBe('6h')
    expect((monitorProps.mock.calls.at(-1)![0] as { range: string }).range).toBe('6h')
    sub('status'); expect(screen.getByTestId('range-switch')).toBeInTheDocument()
    sub('procs'); expect(screen.queryByTestId('range-switch')).toBeNull()
    sub('settings'); expect(screen.queryByTestId('range-switch')).toBeNull()
    expect(readHostTab(SEED_HOST_ID)).toBe('settings')
  })

  it('設定 gets the pool host\'s alias, the pool host id as the key, and the App host that fronts it', () => {
    mount([poolHostOf()])
    sub('settings')
    const p = settingsProps.mock.calls.at(-1)![0] as { hostId: string; poolAlias: string; hostConfigId?: string; offline: boolean }
    expect(p).toMatchObject({ hostId: SEED_HOST_ID, poolAlias: 'air26', hostConfigId: 'cfg-a', offline: false })
  })
})

describe('the banner of a host that is not ok (M22, #3785)', () => {
  it.each([
    ['wwd_down', 'border-status-warning'],
    ['host_down', 'border-status-error'],
    ['unknown', 'border-border-subtle'],
  ] as const)('%s: on every sub-tab, outside the sub-tab body, with the state\'s colour and the last reading and check times', (state, cls) => {
    mount([down(state)])
    for (const k of ['monitor', 'status', 'procs', 'settings']) {
      sub(k)
      const banner = screen.getByTestId('host-banner')
      expect(banner).toHaveAttribute('data-state', state)
      expect(banner.className).toContain(cls)
      expect(screen.getByTestId('host-banner-title')).toHaveTextContent('air26')
      expect(banner).toHaveTextContent(t('pool.liveness.checked', { time: clock(NOW - 3000) }))
      expect(banner).toHaveTextContent(t('watch.host.banner.chart_stops'))
      // Not inside any sub-tab's body.
      for (const id of ['mock-monitor', 'mock-settings', 'status-later', 'procs-later']) {
        const body = screen.queryByTestId(id)
        if (body) expect(banner.contains(body)).toBe(false)
      }
    }
  })

  it('wwd_down says the word of its state, host_down another', () => {
    mount([down('wwd_down')])
    expect(screen.getByTestId('host-banner-title')).toHaveTextContent(t('pool.liveness.wwd_down'))
    cleanup()
    mount([down('host_down')])
    expect(screen.getByTestId('host-banner-title')).toHaveTextContent(t('pool.liveness.host_down'))
  })

  it('a host that is ok but whose owning App host is not connected: the guardian settings are offline too (codex R1)', () => {
    mount([poolHostOf()])
    sub('settings')
    expect(screen.queryByTestId('host-banner')).toBeNull() // the pool still reads the host
    expect((settingsProps.mock.calls.at(-1)![0] as { offline: boolean }).offline).toBe(false)
    act(() => useHostStore.setState({ runtime: { 'cfg-a': { status: 'disconnected' } } } as never))
    expect(screen.queryByTestId('host-banner')).toBeNull()
    expect((settingsProps.mock.calls.at(-1)![0] as { offline: boolean }).offline).toBe(true)
  })

  it('the settings page is told the host is offline', () => {
    mount([down('host_down')])
    sub('settings')
    expect((settingsProps.mock.calls.at(-1)![0] as { offline: boolean }).offline).toBe(true)
  })
})

describe('a pane whose content changes host in place (codex attack)', () => {
  it('reads the new host\'s remembered sub-tab and range, not the old one\'s', () => {
    const other = '1234567890abcdef'
    writeHostTab(other, 'procs'); writeHostRange(other, '6h')
    const hosts = [poolHostOf(), poolHostOf({ host_id: other, alias: 'mserv' })]
    fetchPoolHosts.mockResolvedValue(hosts)
    seedPool(hosts)
    const pane = (id: string) => ({ id: 'p', content: { kind: 'watch-host', poolHostId: id } }) as never
    const { rerender } = render(<WatchHostPane pane={pane(SEED_HOST_ID)} isActive />)
    expect(screen.getByTestId('mock-monitor')).toBeInTheDocument()
    rerender(<WatchHostPane pane={pane(other)} isActive />)
    expect(within(screen.getByTestId('page-switch')).getByRole('button', { name: t('watch.host.page.procs') })).toHaveAttribute('aria-pressed', 'true')
    rerender(<WatchHostPane pane={pane(SEED_HOST_ID)} isActive />)
    expect(screen.getByTestId('mock-monitor')).toBeInTheDocument()
    expect((monitorProps.mock.calls.at(-1)![0] as { range: string }).range).toBe('30m')
  })
})

describe('a tab whose host the pool does not list', () => {
  it('says so (A7)', () => {
    mount([poolHostOf({ host_id: 'ffffffffffffffff', alias: 'other' })])
    expect(screen.getByTestId('host-not-listed')).toHaveTextContent(t('watch.host.pool_missing'))
  })
  it('an older tab (a fleet id only) has no data to show', () => {
    mount([poolHostOf()], { kind: 'watch-host', fleetHostId: 'd-a' })
    expect(screen.getByText(t('watch.host.no_host'))).toBeInTheDocument()
  })
})
