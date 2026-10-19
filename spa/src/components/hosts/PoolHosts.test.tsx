// 7a-3a: the Hosts page on the resource pool — the sidebar's 「資源池主機」 group and the overview's table.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within, act } from '@testing-library/react'
import { HostSidebar } from './HostSidebar'
import { PoolHostsSection } from './PoolHostsSection'
import { ProbeHostsSection } from './ProbeHostsSection'
import { ProbeHostPage } from './ProbeHostPage'
import { PoolHostRoute } from './PoolHostRoute'
import { useFleetDraftStore } from '../../lib/fleet/config-draft'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useFleetConfigStore } from '../../lib/fleet/config-store'
import { useFleetStore } from '../../lib/fleet/store'
import { usePoolStore } from '../../lib/pool/store'
import { clearContributions } from '../../lib/settings-contribution-registry'
import { clearModuleRegistry } from '../../lib/module-registry'
import { registerBuiltinModules } from '../../lib/register-modules'
import { parseFacts, parseHostsAnswer } from '../../lib/pool/parse'
import darwinJson from '../../lib/pool/__fixtures__/facts_darwin.json'
import linuxJson from '../../lib/pool/__fixtures__/facts_linux.json'
import hostsJson from '../../lib/pool/__fixtures__/pool_hosts_response.json'
import type { PoolHost } from '../../lib/pool/types'

vi.mock('../../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, listSessions: vi.fn().mockResolvedValue([]) }
})
vi.mock('../../lib/fleet/config-api', () => ({
  fetchFleetConfig: async () => ({
    probe: { items: [], revision: 1, dropped: null, invalid: false },
    fleet: { items: { order: [], peer_ssh_alias: '', enabled: true }, revision: 1, dropped: null, invalid: false },
  }),
  fetchSshAliases: async () => [],
  putProbeHosts: vi.fn(),
  putFleetRow: vi.fn(),
}))
const fetchPoolHosts = vi.fn()
vi.mock('../../lib/pool/api', () => ({ fetchPoolHosts: (...a: unknown[]) => fetchPoolHosts(...a) }))

const HOST_ID = 'cfg-mlab'
const daemon = (id: string, alias: string, over: Record<string, unknown> = {}) => ({
  id, kind: 'daemon', alias, name: alias, icon: 'Desktop', os: '', status: 'ok', error: null, last_ok_at: Date.now(), fast: { at: Date.now(), cpu: 5, mem: 40, level: 1, page_rate: 0, disk: 30, disk_free_gb: 100, load1: 1, ncpu: 8, zone: 'normal', load: 20 }, slow: null, ...over,
})
const probe = (id: string, alias: string, over: Record<string, unknown> = {}) => ({ ...daemon(id, alias, over), kind: 'probe' })
const darwin = parseFacts(darwinJson)!
const linux = parseFacts(linuxJson)!
const base = parseHostsAnswer(hostsJson)![0]
const ph = (alias: string, over: Partial<PoolHost> = {}): PoolHost => ({ ...base, alias, host_id: `h-${alias}`, version: '1.0.0-alpha.755', facts: { ...darwin, alias, at: Date.now() }, reachable: true, ...over })
const offWorker = { ...darwin.worker, enabled: false }
const props = (extra: Record<string, unknown> = {}) => ({ selectedHostId: HOST_ID, selectedSubPage: 'overview' as const, onSelect: vi.fn(), onAddHost: vi.fn(), ...extra })

beforeEach(() => {
  cleanup(); clearContributions(); clearModuleRegistry(); registerBuiltinModules()
  vi.useFakeTimers()
  fetchPoolHosts.mockReset()
  usePoolStore.getState().stop()
  usePoolStore.getState().reset()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0, daemonId: 'd-mlab' } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } } as never,
  })
  useNexHostStore.setState({ byHost: { [HOST_ID]: { phase: 'ready', daemonCapabilities: ['pool.v1'] } } } as never)
  useFleetConfigStore.getState().reset()
  useFleetStore.setState({
    byHost: { [HOST_ID]: { gen: 1, support: 'yes', term: 0 } } as never,
    snapshot: {
      hostId: HOST_ID, term: 1, order: ['d-mlab', 'd-air26', 'p-mserv', 'p-air19'],
      hosts: {
        'd-mlab': daemon('d-mlab', 'mlab'), 'd-air26': daemon('d-air26', 'air26'),
        'p-mserv': probe('p-mserv', 'mserv'), 'p-air19': probe('p-air19', 'air19', { status: 'offline', fast: null, last_ok_at: 1000 }),
      },
    } as never,
  })
})
afterEach(() => { usePoolStore.getState().stop(); useFleetStore.getState().reset(); clearContributions(); clearModuleRegistry(); vi.useRealTimers() })

const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0) })
function livePool() {
  fetchPoolHosts.mockResolvedValue([
    ph('mlab'),
    ph('air26', { facts: { ...darwin, alias: 'air26', at: Date.now(), worker: offWorker } }),
    ph('mserv', { version: '1.0.0-alpha.754', facts: { ...linux, at: Date.now() } }),
  ])
}

describe('the sidebar group', () => {
  it('on the pool: 「資源池主機」 lists all four hosts in order, with a light each, and no plus', async () => {
    livePool()
    render(<HostSidebar {...props()} />)
    await flush()
    const group = screen.getByTestId('pool-group')
    expect(group.textContent).toContain('Pool hosts')
    expect(screen.queryByTestId('probe-group')).toBeNull()
    expect(screen.queryByTestId('probe-group-add')).toBeNull()
    const ids = within(group).getAllByRole('button').map((b) => b.getAttribute('data-testid')).filter((x) => x?.startsWith('pool-item-') && !x.startsWith('pool-item-off-'))
    expect(ids).toEqual(['pool-item-d-mlab', 'pool-item-d-air26', 'pool-item-p-mserv', 'pool-item-p-air19'])
    const dot = (id: string) => within(screen.getByTestId(`pool-item-${id}`)).getByTestId('pool-status').getAttribute('data-dot')
    expect([dot('d-mlab'), dot('d-air26'), dot('p-mserv'), dot('p-air19')]).toEqual(['live', 'live', 'live', 'off'])
  })

  it('a host whose worker is off carries 「不接」; the others do not', async () => {
    livePool()
    render(<HostSidebar {...props()} />)
    await flush()
    expect(screen.getByTestId('pool-item-off-d-air26')).toBeTruthy()
    expect(screen.queryByTestId('pool-item-off-d-mlab')).toBeNull()
  })

  it('wwd silent but the fleet still reads the host: a yellow light, not an offline one', async () => {
    fetchPoolHosts.mockResolvedValue([ph('mlab')])
    render(<HostSidebar {...props()} />)
    await flush()
    expect(within(screen.getByTestId('pool-item-d-air26')).getByTestId('pool-status').getAttribute('data-dot')).toBe('warn')
  })

  it('a fleet error with the pool silent is red', async () => {
    fetchPoolHosts.mockResolvedValue([ph('mlab')])
    useFleetStore.setState((s) => ({ snapshot: { ...s.snapshot!, hosts: { ...s.snapshot!.hosts, 'p-air19': probe('p-air19', 'air19', { status: 'error', fast: null }) } } }) as never)
    render(<HostSidebar {...props()} />)
    await flush()
    expect(within(screen.getByTestId('pool-item-p-air19')).getByTestId('pool-status').getAttribute('data-dot')).toBe('error')
  })

  it('a probe host opens its own entry; a daemon host opens its pool host page', async () => {
    livePool()
    const p = props()
    render(<HostSidebar {...p} />)
    await flush()
    fireEvent.click(screen.getByTestId('pool-item-p-mserv'))
    expect(p.onSelect).toHaveBeenLastCalledWith('probe:p-mserv', '')
    fireEvent.click(screen.getByTestId('pool-item-d-mlab'))
    expect(p.onSelect).toHaveBeenLastCalledWith('pool:d-mlab', '')
  })

  it('capability gate: no daemon announces pool.v1 → the observed-hosts group as it was', async () => {
    useNexHostStore.setState({ byHost: { [HOST_ID]: { phase: 'ready', daemonCapabilities: [] } } } as never)
    render(<HostSidebar {...props()} />)
    await flush()
    expect(fetchPoolHosts).not.toHaveBeenCalled()
    expect(screen.queryByTestId('pool-group')).toBeNull()
    expect(screen.getByTestId('probe-group').textContent).toContain('Observed hosts')
  })
})

describe('the overview table', () => {
  it('four rows with status, Worker, what each takes and wwd\'s version', async () => {
    livePool()
    render(<PoolHostsSection />)
    await flush()
    const row = (id: string) => screen.getByTestId(`pool-host-row-${id}`)
    expect(within(row('d-mlab')).getByTestId('pool-host-status').textContent).toBe('Normal')
    expect(within(row('d-mlab')).getByTestId('pool-host-worker').textContent).toContain('On')
    expect(within(row('d-mlab')).getByTestId('pool-host-headroom').textContent).toContain('heavy 1')
    expect(within(row('d-air26')).getByTestId('pool-host-worker').textContent).toBe('Off')
    expect(within(row('p-mserv')).getByTestId('pool-host-status').textContent).toBe('Unknown')
    expect(within(row('d-mlab')).getByTestId('pool-host-version').textContent).toBe('1.0.0-alpha.755')
  })

  it('the host the pool does not read: wwd not answering with worker and headroom unknown; and one nobody reads is offline', async () => {
    livePool()
    render(<PoolHostsSection />)
    await flush()
    const air19 = screen.getByTestId('pool-host-row-p-air19')
    expect(within(air19).getByTestId('pool-host-status').textContent).toBe('Offline')
    expect(within(air19).getByTestId('pool-host-worker').textContent).toBe('Unknown')
    expect(within(air19).getByTestId('pool-host-version').textContent).toBe('—')
  })

  it('a fleet error with the pool silent says 「Error」 in the status word, not 「Offline」', async () => {
    fetchPoolHosts.mockResolvedValue([ph('mlab')])
    useFleetStore.setState((s) => ({ snapshot: { ...s.snapshot!, hosts: { ...s.snapshot!.hosts, 'p-air19': probe('p-air19', 'air19', { status: 'error', fast: null }) } } }) as never)
    render(<PoolHostsSection />)
    await flush()
    const air19 = screen.getByTestId('pool-host-row-p-air19')
    expect(within(air19).getByTestId('pool-host-status').textContent).toBe('Error')
  })

  it('a fallback row shows when the pool last read the host when it knows, the short words when it does not', async () => {
    fetchPoolHosts.mockResolvedValue([ph('mlab')])
    render(<PoolHostsSection />)
    await flush()
    const status = within(screen.getByTestId('pool-host-row-d-air26')).getByTestId('pool-host-status').textContent ?? ''
    expect(status).toMatch(/^wwd is not answering( \(since .+\))?$/)
  })

  it('a version behind the newest is marked, the newest and the unknown are not', async () => {
    livePool()
    render(<PoolHostsSection />)
    await flush()
    expect(screen.getByTestId('pool-host-row-p-mserv').querySelector('[data-testid="pool-host-version"]')!.getAttribute('data-behind')).toBe('true')
    expect(screen.getByTestId('pool-host-row-d-mlab').querySelector('[data-testid="pool-host-version"]')!.getAttribute('data-behind')).toBeNull()
    expect(screen.getByTestId('pool-host-row-p-air19').querySelector('[data-testid="pool-host-version"]')!.getAttribute('data-behind')).toBeNull()
  })

  it('has no add button, and says why in the hint', async () => {
    livePool()
    render(<PoolHostsSection />)
    await flush()
    expect(screen.queryByTestId('probe-add')).toBeNull()
    expect(screen.getByTestId('pool-hosts-section').textContent).toContain('installing wwd')
  })

  it('renders nothing before the pool has answered', async () => {
    fetchPoolHosts.mockReturnValue(new Promise(() => {}))
    render(<PoolHostsSection />)
    await flush()
    expect(screen.queryByTestId('pool-hosts-section')).toBeNull()
  })
})

describe('the section on an older daemon', () => {
  it('no daemon announces pool.v1: the observed-hosts section as before, with one notice on top', async () => {
    useNexHostStore.setState({ byHost: { [HOST_ID]: { phase: 'ready', daemonCapabilities: [] } } } as never)
    useFleetStore.setState((s) => ({ byHost: { ...s.byHost, [HOST_ID]: { ...s.byHost[HOST_ID], role: 'primary', support: 'yes' } } }) as never)
    render(<ProbeHostsSection />)
    await flush()
    expect(screen.getByTestId('pool-old-daemon').textContent).toContain('upgrade')
    expect(screen.getByTestId('probe-hosts-section')).toBeTruthy()
  })
  it('no notice while the capabilities are not read yet, or while the announcing daemon is only disconnected', async () => {
    useNexHostStore.setState({ byHost: { [HOST_ID]: { phase: 'loading' } } } as never)
    useFleetStore.setState((s) => ({ byHost: { ...s.byHost, [HOST_ID]: { ...s.byHost[HOST_ID], role: 'primary', support: 'yes' } } }) as never)
    const { unmount } = render(<ProbeHostsSection />)
    await flush()
    expect(screen.queryByTestId('pool-old-daemon')).toBeNull()
    unmount()
    useNexHostStore.setState({ byHost: { [HOST_ID]: { phase: 'ready', daemonCapabilities: ['pool.v1'] } } } as never)
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } } as never)
    usePoolStore.getState().reset()
    render(<ProbeHostsSection />)
    await flush()
    expect(screen.queryByTestId('pool-old-daemon')).toBeNull()
  })
  it('on the pool the old section is not drawn', async () => {
    livePool()
    render(<ProbeHostsSection />)
    await flush()
    expect(screen.queryByTestId('probe-hosts-section')).toBeNull()
    expect(screen.queryByTestId('pool-old-daemon')).toBeNull()
    expect(screen.getByTestId('pool-hosts-section')).toBeTruthy()
  })
})

describe('the pool host page on the Hosts page', () => {
  it('a probe host: the pool host page with the style editor under 「樣式與 SSH 備援」', async () => {
    livePool()
    useFleetDraftStore.getState().selectProbe({ id: 'p-mserv', isNew: false })
    render(<ProbeHostPage probeId="p-mserv" />)
    await flush()
    expect(screen.getByTestId('pool-page')).toBeTruthy()
    expect(screen.queryByTestId('probe-page')).toBeNull()
    expect(screen.getByTestId('pool-page-style').textContent).toContain('Style and SSH fallback')
  })

  it('no pool.v1: the probe page as it was', async () => {
    useNexHostStore.setState({ byHost: { [HOST_ID]: { phase: 'ready', daemonCapabilities: [] } } } as never)
    render(<ProbeHostPage probeId="p-mserv" />)
    await flush()
    expect(screen.getByTestId('probe-page')).toBeTruthy()
    expect(screen.queryByTestId('pool-page')).toBeNull()
  })

  it('a daemon host: its pool host page without the style editor', async () => {
    livePool()
    render(<PoolHostRoute id="d-mlab" />)
    await flush()
    expect(screen.getByTestId('pool-page-name').textContent).toBe('mlab')
    expect(screen.queryByTestId('pool-page-style')).toBeNull()
  })

  it('a host wwd is silent for shows the fleet\'s readings, not the pool\'s old facts', async () => {
    fetchPoolHosts.mockResolvedValue([ph('mlab')])
    render(<PoolHostRoute id="d-air26" />)
    await flush()
    const now = screen.getByTestId('pool-page-now')
    expect(within(now).getByTestId('pool-page-cpu').textContent).toBe('CPU 5%')
    expect(within(now).getByTestId('pool-page-mem').textContent).toBe('Memory 40%')
    expect(within(now).getByTestId('pool-page-disk').textContent).toContain('Disk 30% used')
  })

  it('a live host shows what wwd read', async () => {
    livePool()
    render(<PoolHostRoute id="d-mlab" />)
    await flush()
    const cpu = Math.round(100 - darwin.cpu.idle_pct)
    expect(within(screen.getByTestId('pool-page-now')).getByTestId('pool-page-cpu').textContent).toBe(`CPU ${cpu}%`)
  })

  it('a host that left the list shows nothing', async () => {
    livePool()
    const { container } = render(<PoolHostRoute id="d-gone" />)
    await flush()
    expect(container.textContent).toBe('')
  })

  it('picking a pool host drops the probe selection and the other way round', () => {
    const d = useFleetDraftStore.getState
    d().selectProbe({ id: 'p-mserv', isNew: false })
    d().selectPool('d-mlab')
    expect(d().selectedProbe).toBeNull()
    expect(d().selectedPool).toBe('d-mlab')
    d().selectProbe({ id: 'p-air19', isNew: false })
    expect(d().selectedPool).toBeNull()
    d().selectPool('d-mlab')
    d().selectProbe(null)
    expect(d().selectedPool).toBeNull()
  })
})
