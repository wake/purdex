// spa/src/components/watch/host/SettingsTab.test.tsx — the host page's 設定 tab as a whole (MON-9a-1): the order of its sections, the same layout with and without a daemon, the guardian section last and collapsed
// and only where there is a daemon, and the worker settings working inside it (spec M46, M47, M52). The pieces have their own tests; the pool and the App host stores are real, the daemon's HTTP is mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { SettingsTab } from './SettingsTab'
import { PENDING_NOTICE_MS } from '../WorkerSettings'
import { usePoolStore } from '../../../lib/pool/store'
import { clearControlDenied } from '../../../lib/pool/control-api'
import { clearWorkerDrafts } from '../../../lib/pool/worker-draft-memory'
import { clearWatchDrafts } from '../../../lib/watch/draft-memory'
import { parseFacts, parseHostsAnswer } from '../../../lib/pool/parse'
import type { PoolHost, PoolWorker } from '../../../lib/pool/types'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import darwinJson from '../../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../../lib/pool/__fixtures__/pool_hosts_response.json'
import { useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { useI18nStore } from '../../../stores/useI18nStore'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const hostFetch = vi.fn()
vi.mock('../../../lib/host-api', async (orig) => ({
  ...(await orig<typeof import('../../../lib/host-api')>()),
  hostFetch: (...a: unknown[]) => hostFetch(...a),
}))

const CFG = 'cfg-a'
const facts = parseFacts(darwinJson)!
const WORKER: PoolWorker = { enabled: true, slots: { heavy: 1, light: 2 }, owner_rule: 'hid<5m' }
const poolHost = (alias: string, over: Partial<PoolHost> = {}): PoolHost => {
  const base = parseHostsAnswer(hostsJson)![0]
  return { ...base, host_id: `id-${alias}`, alias, reachable: true, worker: WORKER, labels: [], facts: { ...facts, alias, worker: { ...facts.worker, ...WORKER } }, ...over }
}
const monitorHost = (alias: string, daemonHostId: string | null): MonitorHost => ({ hostId: `id-${alias}`, alias, daemonHostId } as MonitorHost)
const reply = (status: number, body: unknown = null) => new Response(body === null ? '' : JSON.stringify(body), { status })
const setPool = (hosts: PoolHost[]) => act(() => { usePoolStore.setState({ hosts, sourceHostId: CFG }) })
const click = async (id: string) => { await act(async () => { fireEvent.click(screen.getByTestId(id)) }) }
/** Where `a` sits relative to `b` in the document. */
const before = (a: Element, b: Element) => (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0

let readOnce: ReturnType<typeof vi.fn>
beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  vi.setSystemTime(1_000_000)
  hostFetch.mockReset()
  hostFetch.mockImplementation(async (_id: string, path: string) => (path === '/api/hostconfig' ? reply(200, { watch: { items: { enabled: true }, revision: 1 } }) : reply(200, { ok: true })))
  clearControlDenied(); clearWorkerDrafts(); clearWatchDrafts()
  readOnce = vi.fn()
  usePoolStore.getState().reset()
  usePoolStore.setState({ readOnce } as never)
  useHostStore.setState({ hosts: { [CFG]: { id: CFG, name: CFG, ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [CFG], activeHostId: CFG, runtime: { [CFG]: { status: 'connected' } } } as never)
  useNexHostStore.setState({ byHost: { [CFG]: { phase: 'ready', daemonCapabilities: ['pool.v1', 'pool.control.v1'] } } } as never)
})
afterEach(() => { cleanup(); vi.useRealTimers() })

const mount = async (h: MonitorHost) => { render(<SettingsTab host={h} />); await act(async () => {}) }

describe('the sections and their order', () => {
  it('a host with a daemon: 接工作, then 暫停, then the guardian — last, and collapsed', async () => {
    setPool([poolHost('mlab')])
    await mount(monitorHost('mlab', CFG))
    const worker = screen.getByTestId('pool-worker-settings')
    const pause = screen.getByTestId('pool-pause')
    const guardian = screen.getByRole('button', { name: t('watch.set.guardian') })
    expect(before(worker, pause)).toBe(true)
    expect(before(pause, guardian)).toBe(true)
    expect(guardian).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByTestId('watch-set-guardian')).toBeNull()
    fireEvent.click(guardian)
    await act(async () => {})
    expect(screen.getByTestId('watch-set-guardian')).toBeInTheDocument()
  })

  it('a host with no daemon (mserv) has the same worker and pause sections and no guardian section, and its slots can be changed here (M52)', async () => {
    setPool([poolHost('mserv')])
    await mount(monitorHost('mserv', null))
    expect(screen.getByTestId('pool-worker-settings')).toBeInTheDocument()
    expect(screen.getByTestId('pool-pause')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: t('watch.set.guardian') })).toBeNull()
    await click('pool-worker-slots-heavy-inc')
    const call = hostFetch.mock.calls.at(-1)!
    expect(call[1]).toBe('/api/pool/hosts/mserv/worker')
    expect(JSON.parse((call[2] as RequestInit).body as string).slots.heavy).toBe(2)
  })

  it('the guardian section reads the row of the App host that fronts the pool host, and is offline when that host is not connected', async () => {
    setPool([poolHost('mlab')])
    act(() => useHostStore.setState({ runtime: { [CFG]: { status: 'disconnected' } } } as never))
    await mount(monitorHost('mlab', CFG))
    fireEvent.click(screen.getByRole('button', { name: t('watch.set.guardian') }))
    await act(async () => {})
    expect(screen.getByTestId('watch-set-offline')).toHaveTextContent(t('watch.set.offline'))
    expect(hostFetch.mock.calls.some((c) => c[1] === '/api/hostconfig')).toBe(false)
  })
})

describe('the worker settings inside the tab (M46, M47)', () => {
  it('turning 接工作 off while air26 runs 1 job asks first and says that it finishes', async () => {
    setPool([poolHost('air26')]) // the fixture runs one light job
    await mount(monitorHost('air26', CFG))
    await click('pool-worker-enabled')
    expect(hostFetch.mock.calls.some((c) => (c[2] as RequestInit | undefined)?.method === 'PUT')).toBe(false)
    expect(screen.getByTestId('pool-worker-confirm-off')).toHaveTextContent(t('pool.worker.confirm_off', { n: 1 }))
  })

  it('changing a slot shows the sent value at once, and 15 s without a newer reading says the host has not reported', async () => {
    setPool([poolHost('air26')])
    await mount(monitorHost('air26', CFG))
    await click('pool-worker-slots-light-inc')
    expect(screen.getByTestId('pool-worker-slots-light-value')).toHaveTextContent('3')
    expect(screen.getByTestId('pool-worker-pending')).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(PENDING_NOTICE_MS) })
    expect(screen.getByTestId('pool-worker-late')).toHaveTextContent(t('pool.worker.late'))
  })
})
