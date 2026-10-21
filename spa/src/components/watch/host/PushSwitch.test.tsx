// spa/src/components/watch/host/PushSwitch.test.tsx — the 設定 tab's 推播 switch (MON-9a-2b; spec M37, M50): four states, one press writes every host that has a daemon, hosts that are offline wait in the queue,
// a page of a host with no daemon says who delivers its alerts.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { PushSwitch } from './PushSwitch'
import { usePoolStore } from '../../../lib/pool/store'
import { clearPushQueue } from '../../../lib/pool/push-switch'
import { parseFacts, parseHostsAnswer } from '../../../lib/pool/parse'
import type { PoolHost } from '../../../lib/pool/types'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import darwinJson from '../../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../../lib/pool/__fixtures__/pool_hosts_response.json'
import { useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { useI18nStore } from '../../../stores/useI18nStore'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const fetchHostConfig = vi.fn()
const putHostConfig = vi.fn()
vi.mock('../../../lib/host-config-api', async (orig) => ({
  ...(await orig<typeof import('../../../lib/host-config-api')>()),
  fetchHostConfig: (...a: unknown[]) => fetchHostConfig(...a),
  putHostConfig: (...a: unknown[]) => putHostConfig(...a),
}))

const facts = parseFacts(darwinJson)!
const poolHost = (id: string, alias: string): PoolHost => ({ ...parseHostsAnswer(hostsJson)![0], host_id: id, alias, reachable: true, labels: [], facts: { ...facts, alias } })
const monitorHost = (id: string, alias: string, daemonHostId: string | null) => ({ hostId: id, alias, daemonHostId } as MonitorHost)
const MLAB = monitorHost('w-mlab', 'mlab', 'cfg-a')
const MSERV = monitorHost('w-mserv', 'mserv', null)

let server: Record<string, { push: boolean; revision: number }>
const setConnection = (a: 'connected' | 'disconnected', b: 'connected' | 'disconnected') => act(() => {
  useHostStore.setState({
    hosts: { 'cfg-a': { id: 'cfg-a', name: 'mlab', ip: '1', port: 1, token: 't', order: 0 }, 'cfg-b': { id: 'cfg-b', name: 'air26', ip: '2', port: 1, token: 't', order: 1 } },
    hostOrder: ['cfg-a', 'cfg-b'], runtime: { 'cfg-a': { status: a }, 'cfg-b': { status: b } },
  } as never)
})
const mount = async (host: MonitorHost = MLAB) => { render(<PushSwitch host={host} />); await act(async () => {}) }
const press = async () => { await act(async () => { fireEvent.click(screen.getByRole('switch')) }) }

beforeEach(() => {
  cleanup(); clearPushQueue()
  fetchHostConfig.mockReset(); putHostConfig.mockReset()
  server = { 'cfg-a': { push: false, revision: 1 }, 'cfg-b': { push: false, revision: 4 } }
  fetchHostConfig.mockImplementation(async (id: string) => ({ wwdAlerts: { items: { push: server[id].push }, revision: server[id].revision } }))
  putHostConfig.mockImplementation(async (id: string, _c: string, items: { push: boolean }) => { server[id] = { push: items.push, revision: server[id].revision + 1 }; return {} })
  usePoolStore.getState().reset()
  usePoolStore.setState({ hosts: [poolHost('w-mlab', 'mlab'), poolHost('w-air26', 'air26'), poolHost('w-mserv', 'mserv')], sourceHostId: 'cfg-a' } as never)
  setConnection('connected', 'connected')
  useNexHostStore.setState({ byHost: {
    'cfg-a': { phase: 'ready', fetchedAt: Date.now(), wwd: { hostId: 'w-mlab', alias: 'mlab' }, wwdAlerts: { push: 'on', deliverer: true } },
    'cfg-b': { phase: 'ready', fetchedAt: Date.now(), wwd: { hostId: 'w-air26', alias: 'air26' }, wwdAlerts: { push: 'on', deliverer: false } },
  } } as never)
})
afterEach(() => { cleanup(); clearPushQueue() })

describe('the four states', () => {
  it('on', async () => {
    server['cfg-a'].push = true; server['cfg-b'].push = true
    await mount()
    expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'true')
    expect(screen.queryByTestId('push-partial')).toBeNull()
  })

  it('off', async () => {
    await mount()
    expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'false')
  })

  it('partial: says 部分開啟 and lists every host', async () => {
    server['cfg-a'].push = true
    await mount()
    const line = screen.getByTestId('push-partial')
    expect(line).toHaveTextContent(t('watch.push.partial'))
    expect(line).toHaveTextContent(`mlab：${t('watch.push.on')}`)
    expect(line).toHaveTextContent(`air26：${t('watch.push.off')}`)
  })

  it('unknown: nothing could be read — the switch is off and says so', async () => {
    fetchHostConfig.mockRejectedValue(new Error('down'))
    await mount()
    expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByTestId('push-unknown')).toHaveTextContent(t('watch.push.unknown'))
  })
})

describe('pressing it', () => {
  it('off → both hosts are written true', async () => {
    await mount()
    await press()
    expect(putHostConfig).toHaveBeenCalledWith('cfg-a', 'wwd_alerts', { push: true }, 1)
    expect(putHostConfig).toHaveBeenCalledWith('cfg-b', 'wwd_alerts', { push: true }, 4)
    expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'true')
  })

  it('on → both are written false', async () => {
    server['cfg-a'].push = true; server['cfg-b'].push = true
    await mount()
    await press()
    expect(putHostConfig).toHaveBeenCalledWith('cfg-a', 'wwd_alerts', { push: false }, 1)
    expect(putHostConfig).toHaveBeenCalledWith('cfg-b', 'wwd_alerts', { push: false }, 4)
  })

  it('partial → one press sets both to ON', async () => {
    server['cfg-a'].push = true
    await mount()
    await press()
    expect(putHostConfig).toHaveBeenCalledTimes(2)
    for (const c of putHostConfig.mock.calls) expect(c[2]).toEqual({ push: true })
    expect(screen.queryByTestId('push-partial')).toBeNull()
  })

  it('a host that fails says why, on its own line, and the other is still written', async () => {
    putHostConfig.mockImplementationOnce(async () => { throw new Error('500 boom') })
    await mount()
    await press()
    expect(screen.getByTestId('push-error-cfg-a')).toHaveTextContent('mlab')
    expect(screen.getByTestId('push-error-cfg-a')).toHaveTextContent('500 boom')
    expect(putHostConfig).toHaveBeenCalledTimes(2)
    expect(screen.queryByTestId('push-error-cfg-b')).toBeNull()
  })
})

describe('a host that is offline', () => {
  it('is not skipped: the line names it, and goes away when it connects and is written', async () => {
    setConnection('connected', 'disconnected')
    await mount()
    await press()
    expect(putHostConfig).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('push-queued')).toHaveTextContent(t('watch.push.queued', { n: 1, hosts: 'air26' }))
    await act(async () => { setConnection('connected', 'connected') })
    await vi.waitFor(() => expect(putHostConfig).toHaveBeenCalledTimes(2))
    await vi.waitFor(() => expect(screen.queryByTestId('push-queued')).toBeNull())
  })

  it('the line lists the alias of each waiting host', async () => {
    setConnection('disconnected', 'disconnected')
    await mount()
    await press()
    expect(screen.getByTestId('push-queued')).toHaveTextContent(t('watch.push.queued', { n: 2, hosts: 'mlab、air26' }))
  })

  it('coming back to the page later (remount) still shows the waiting line', async () => {
    setConnection('connected', 'disconnected')
    await mount()
    await press()
    cleanup()
    await mount()
    expect(screen.getByTestId('push-queued')).toHaveTextContent('air26')
  })
})

describe('the page of a host with no daemon', () => {
  it('says who delivers its alerts (the deliverer from /api/info.wwdalerts) and who takes over', async () => {
    await mount(MSERV)
    expect(screen.getByTestId('push-no-daemon')).toHaveTextContent(t('watch.push.no_daemon', { alias: 'mserv', deliverer: 'mlab', other: 'air26' }))
  })

  it('names the other host when the deliverer is the other one', async () => {
    useNexHostStore.setState({ byHost: {
      'cfg-a': { phase: 'ready', fetchedAt: Date.now(), wwd: { hostId: 'w-mlab', alias: 'mlab' }, wwdAlerts: { push: 'on', deliverer: false } },
      'cfg-b': { phase: 'ready', fetchedAt: Date.now(), wwd: { hostId: 'w-air26', alias: 'air26' }, wwdAlerts: { push: 'on', deliverer: true } },
    } } as never)
    await mount(MSERV)
    expect(screen.getByTestId('push-no-daemon')).toHaveTextContent(t('watch.push.no_daemon', { alias: 'mserv', deliverer: 'air26', other: 'mlab' }))
  })

  it('when the deliverer cannot be read it says the fallback: mlab, then air26', async () => {
    useNexHostStore.setState({ byHost: {
      'cfg-a': { phase: 'ready', fetchedAt: Date.now(), wwd: { hostId: 'w-mlab', alias: 'mlab' }, wwdAlerts: null },
      'cfg-b': { phase: 'ready', fetchedAt: Date.now(), wwd: { hostId: 'w-air26', alias: 'air26' } },
    } } as never)
    await mount(MSERV)
    expect(screen.getByTestId('push-no-daemon')).toHaveTextContent(t('watch.push.no_daemon_fallback', { alias: 'mserv' }))
  })

  it('a host that has a daemon has no such line', async () => {
    await mount(MLAB)
    expect(screen.queryByTestId('push-no-daemon')).toBeNull()
  })
})
