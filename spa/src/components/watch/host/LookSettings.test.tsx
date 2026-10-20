// spa/src/components/watch/host/LookSettings.test.tsx — the 設定 tab's 側欄顯示 section (MON-9a-3; spec M9, M51, W7): the colour, icon and indicator of the host's cell on the sidebar, with a preview that follows the writes.
// The two pickers are the Hosts page's own components and have their own tests; here they are replaced by one button each, so the test is about what LookSettings writes and where.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'
import { LookSettings } from './LookSettings'
import { usePoolHostsStore } from '../../../stores/usePoolHostsStore'
import { useHostLookStore } from '../../../stores/useHostLookStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { clearViewMemory, readLookMode } from '../../../lib/watch/view-memory'
import { usePoolHostLook } from '../../../lib/pool/host-looks'
import { poolHostState } from '../../../lib/pool/host-state'
import { parseFacts, parseHostsAnswer } from '../../../lib/pool/parse'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import darwinJson from '../../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../../lib/pool/__fixtures__/pool_hosts_response.json'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

vi.mock('../../hosts/HostColorPicker', () => ({
  HostColorPicker: (p: { mode: string; onModeChange: (m: 'console' | 'terminal' | 'execution') => void; onSetLayer: (m: 'console' | 'terminal' | 'execution', l: 'main', v: { color: string; alpha: number }) => void; onClearMode: (m: 'console' | 'terminal' | 'execution') => void }) => (
    <div data-testid="mock-color-picker" data-mode={p.mode}>
      <button data-testid="mock-color-set" onClick={() => p.onSetLayer(p.mode as 'terminal', 'main', { color: '#336699', alpha: 100 })}>set</button>
      <button data-testid="mock-color-clear" onClick={() => p.onClearMode(p.mode as 'terminal')}>clear</button>
      <button data-testid="mock-mode-console" onClick={() => p.onModeChange('console')}>console</button>
    </div>
  ),
}))
vi.mock('../../hosts/HostIconPicker', () => ({
  HostIconPicker: (p: { onChange: (icon: string | null, weight?: 'bold') => void }) => (
    <button data-testid="mock-icon-set" onClick={() => p.onChange('Cube', 'bold')}>icon</button>
  ),
}))
let listed: MonitorHost[] = []
vi.mock('../../../lib/pool/use-monitor-hosts', async (orig) => ({ ...(await orig<typeof import('../../../lib/pool/use-monitor-hosts')>()), useMonitorHosts: () => listed }))
vi.mock('../../../features/workspace/lib/icon-path-cache', () => ({ getIconPath: () => 'M0,0L10,10', isWeightLoaded: () => true, prefetchWeight: () => Promise.resolve() }))

const MLAB = '82b11c521df16c12'
const AIR = '0123456789abcdef'
const MSERV = 'aaaaaaaaaaaaaaaa'
const FACTS = parseFacts(darwinJson)!
const BASE = parseHostsAnswer(hostsJson)![0]
const NOW = FACTS.at + 5000
const monitorHost = (hostId: string, alias: string, daemonHostId: string | null): MonitorHost => {
  const pool = { ...BASE, host_id: hostId, alias, reachable: true, facts: FACTS }
  return { hostId, alias, daemonHostId, state: poolHostState(pool, null, NOW), look: { indicator: { shape: 'ring', data: ['load'] } }, poolFailure: null, count: null, local: false } as MonitorHost
}
/** The host as the page hands it: its look resolved from the stores, as `useMonitorHosts` does. */
function Page({ base }: { base: MonitorHost }) {
  const look = usePoolHostLook(base.hostId)
  return <LookSettings host={{ ...base, look }} />
}
const mount = (h: MonitorHost) => render(<Page base={h} />)
const ind = (id: string) => usePoolHostsStore.getState().looks[id]?.indicator

beforeEach(() => {
  cleanup(); clearViewMemory()
  usePoolHostsStore.setState({ order: [], looks: {} })
  useHostLookStore.setState({ looks: {} } as never)
  useUISettingsStore.setState({ fleetCellIndicator: 'ring' })
  useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: ['cfg-a'], runtime: {} } as never)
  useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', fetchedAt: Date.now(), wwd: { hostId: MLAB, alias: 'mlab' } } } } as never)
  listed = [monitorHost(MLAB, 'mlab', 'cfg-a'), monitorHost(AIR, 'air26', null), monitorHost(MSERV, 'mserv', null), monitorHost('bbbbbbbbbbbbbbbb', 'fourth', null)]
})
afterEach(() => cleanup())

describe('colour and icon — written where the host\'s look lives', () => {
  it('a host with a daemon: the colour goes into the App host\'s look (the tab badge reads the same one), not into the pool store', () => {
    mount(monitorHost(MLAB, 'mlab', 'cfg-a'))
    fireEvent.click(screen.getByTestId('mock-color-set'))
    const stored = Object.values(useHostLookStore.getState().looks)
    expect(stored.some((l) => l.colors?.terminal?.main?.color === '#336699')).toBe(true)
    expect(usePoolHostsStore.getState().looks[MLAB]?.colors).toBeUndefined()
    expect(screen.getByTestId('look-daemon-note')).toHaveTextContent(t('watch.look.daemon_note'))
  })

  it('a host with a daemon: the icon goes into the App host\'s look too', () => {
    mount(monitorHost(MLAB, 'mlab', 'cfg-a'))
    fireEvent.click(screen.getByTestId('mock-icon-set'))
    expect(Object.values(useHostLookStore.getState().looks).some((l) => l.icon === 'Cube' && l.iconWeight === 'bold')).toBe(true)
  })

  it('a host with no daemon (mserv): colour and icon go into the pool store, and there is no daemon note', () => {
    mount(monitorHost(MSERV, 'mserv', null))
    fireEvent.click(screen.getByTestId('mock-color-set'))
    fireEvent.click(screen.getByTestId('mock-icon-set'))
    expect(usePoolHostsStore.getState().looks[MSERV]).toMatchObject({ colors: { terminal: { main: { color: '#336699' } } }, icon: 'Cube', iconWeight: 'bold' })
    expect(Object.keys(useHostLookStore.getState().looks)).toHaveLength(0)
    expect(screen.queryByTestId('look-daemon-note')).toBeNull()
  })

  it('clearing a mode drops that mode\'s colours', () => {
    mount(monitorHost(MSERV, 'mserv', null))
    fireEvent.click(screen.getByTestId('mock-color-set'))
    fireEvent.click(screen.getByTestId('mock-color-clear'))
    expect(usePoolHostsStore.getState().looks[MSERV]?.colors).toBeUndefined()
  })
})

describe('the colour mode of the editor (tab-hosted)', () => {
  it('starts on the terminal mode, the one the sidebar draws, and a change is remembered per host', () => {
    mount(monitorHost(MSERV, 'mserv', null))
    expect(screen.getByTestId('mock-color-picker')).toHaveAttribute('data-mode', 'terminal')
    fireEvent.click(screen.getByTestId('mock-mode-console'))
    expect(screen.getByTestId('mock-color-picker')).toHaveAttribute('data-mode', 'console')
    expect(readLookMode(MSERV)).toBe('console')
    expect(readLookMode(MLAB)).toBe('terminal')
  })
})

describe('the indicator', () => {
  const pick = (name: string) => fireEvent.click(screen.getByRole('button', { name }))
  it('three shapes; the ring is the default and reads the overall load', () => {
    mount(monitorHost(MSERV, 'mserv', null))
    expect(screen.getByRole('button', { name: t('watch.look.shape.ring') })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('look-data-0')).toHaveValue('load')
    expect(screen.queryByTestId('look-data-1')).toBeNull()
  })

  it('one bar: one item; the choice is written to the pool store', () => {
    mount(monitorHost(MSERV, 'mserv', null))
    pick(t('watch.look.shape.bar'))
    fireEvent.change(screen.getByTestId('look-data-0'), { target: { value: 'disk' } })
    expect(ind(MSERV)).toEqual({ shape: 'bar', data: ['disk'] })
  })

  it('two bars from a ring on the overall load: left becomes CPU, right memory pressure (the design\'s wireSettings)', () => {
    mount(monitorHost(MSERV, 'mserv', null))
    pick(t('watch.look.shape.bars'))
    expect(ind(MSERV)).toEqual({ shape: 'bars', data: ['cpu', 'pressure'] })
    expect(screen.getByTestId('look-data-0')).toHaveValue('cpu')
    expect(screen.getByTestId('look-data-1')).toHaveValue('pressure')
  })

  it('two bars from another item keeps it on the left; a left that is the pressure gets CPU on the right', () => {
    usePoolHostsStore.setState({ order: [], looks: { [MSERV]: { indicator: { shape: 'ring', data: ['mem'] } } } })
    mount(monitorHost(MSERV, 'mserv', null))
    pick(t('watch.look.shape.bars'))
    expect(ind(MSERV)).toEqual({ shape: 'bars', data: ['mem', 'pressure'] })
    fireEvent.change(screen.getByTestId('look-data-0'), { target: { value: 'pressure' } })
    expect(ind(MSERV)?.data[1]).not.toBe('pressure')
  })

  it('each bar has its own item; going back to a ring keeps the left one', () => {
    mount(monitorHost(MSERV, 'mserv', null))
    pick(t('watch.look.shape.bars'))
    fireEvent.change(screen.getByTestId('look-data-1'), { target: { value: 'mem' } })
    expect(ind(MSERV)).toEqual({ shape: 'bars', data: ['cpu', 'mem'] })
    pick(t('watch.look.shape.ring'))
    expect(ind(MSERV)).toEqual({ shape: 'ring', data: ['cpu'] })
  })

  it('the preview is the sidebar cell, and follows the writes', () => {
    mount(monitorHost(MSERV, 'mserv', null))
    expect(within(screen.getByTestId('look-preview')).getByTestId(`load-ring-${MSERV}`)).toBeInTheDocument()
    pick(t('watch.look.shape.bars'))
    expect(within(screen.getByTestId('look-preview')).getByTestId(`load-bars-${MSERV}`)).toBeInTheDocument()
  })
})

describe('apply to all, and back to the default', () => {
  it('"to all four" sets the shape and data of every listed host and leaves their colours and icons alone', () => {
    usePoolHostsStore.setState({ order: [], looks: { [AIR]: { icon: 'Laptop', colors: { terminal: { main: { color: '#112233', alpha: 100 } } } } } })
    mount(monitorHost(MSERV, 'mserv', null))
    pick2()
    fireEvent.click(screen.getByTestId('look-apply-all'))
    for (const id of [MLAB, AIR, MSERV, 'bbbbbbbbbbbbbbbb']) expect(ind(id)).toEqual({ shape: 'bars', data: ['cpu', 'pressure'] })
    expect(usePoolHostsStore.getState().looks[AIR]).toMatchObject({ icon: 'Laptop', colors: { terminal: { main: { color: '#112233' } } } })
    expect(usePoolHostsStore.getState().looks[MSERV]?.colors).toBeUndefined()
  })

  it('"back to the default" writes the ring on the overall load for this host only, and does not touch its colour', () => {
    usePoolHostsStore.setState({ order: [], looks: { [MSERV]: { icon: 'Cube', indicator: { shape: 'bars', data: ['cpu', 'mem'] } }, [AIR]: { indicator: { shape: 'bar', data: ['disk'] } } } })
    mount(monitorHost(MSERV, 'mserv', null))
    fireEvent.click(screen.getByTestId('look-reset'))
    expect(ind(MSERV)).toEqual({ shape: 'ring', data: ['load'] })
    expect(usePoolHostsStore.getState().looks[MSERV]?.icon).toBe('Cube')
    expect(ind(AIR)).toEqual({ shape: 'bar', data: ['disk'] })
  })

  function pick2() { fireEvent.click(screen.getByRole('button', { name: t('watch.look.shape.bars') })) }
})
