// W5e-2: the buttons of the watcher's launch panel — 啟動守門員 (none) and 叫醒守門員 (resting). The panel only asks when pressed.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor, act } from '@testing-library/react'

const hostFetch = vi.hoisted(() => vi.fn())
const readWatchRow = vi.hoisted(() => vi.fn())
vi.mock('../../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../../lib/host-api')>()), hostFetch: (...a: unknown[]) => hostFetch(...a) }))
vi.mock('../../lib/watch/watch-row', async (orig) => ({ ...(await orig<typeof import('../../lib/watch/watch-row')>()), readWatchRow: (...a: unknown[]) => readWatchRow(...a) }))
import { WatchLaunchPanel } from './WatchLaunchPanel'
import { WatchGuardianPane } from './WatchGuardianPane'
import { useWatchStatusStore } from '../../lib/watch/status-store'
import { startWatchLaunch, useWatchLaunchStore } from '../../lib/watch/launch'
import type { WatchStatus } from '../../lib/watch/status-types'
import { useHostStore, lookKeyOf } from '../../stores/useHostStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useI18nStore } from '../../stores/useI18nStore'
const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const status = (hostId: string, state: 'awake' | 'resting' | 'none', extra: Partial<WatchStatus> = {}): WatchStatus =>
  ({ self: { host_id: hostId, state, since: '2026-10-16T01:00:00Z', session_id: state === 'none' ? undefined : 'conv-1234' }, enabled: true, auto_wake: true, mode: 'patrol', targets: [], ...extra })
const answer = (appHost: string, s: WatchStatus) => useWatchStatusStore.setState((st) => ({ byHost: { ...st.byHost, [appHost]: { kind: 'ok', status: s, at: 1 } } }))
const reply = (code: number, body: unknown) => hostFetch.mockResolvedValueOnce(new Response(JSON.stringify(body), { status: code }))
const start = () => screen.getByTestId('watch-launch-start')
const wake = () => screen.getByTestId('watch-launch-wake')
const note = () => screen.getByTestId('watch-launch-note')
const pad = (n: number) => String(n).padStart(2, '0')

let stops: Array<() => void> = []
beforeEach(() => {
  cleanup()
  hostFetch.mockReset(); readWatchRow.mockReset()
  readWatchRow.mockResolvedValue({ revision: 1, row: {}, agentCommand: 'claude --model opus', workdir: '~/w' })
  useWatchLaunchStore.setState({ slot: null, seq: 0 })
  useWatchStatusStore.getState().reset()
  useSessionStore.setState({ sessions: {} })
  useHostLookStore.setState({ looks: {} })
  useHostStore.setState({
    hosts: {
      h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd1' },
      h2: { id: 'h2', name: 'air26', ip: '2', port: 1, token: 't', order: 1, daemonId: 'd2' },
    },
    hostOrder: ['h1', 'h2'], activeHostId: 'h1', runtime: { h1: { status: 'connected' }, h2: { status: 'connected' } },
  } as never)
  stops.push(startWatchLaunch())
})
afterEach(() => { for (const s of stops) s(); stops = []; vi.useRealTimers() })

describe('which button, where', () => {
  it('none: 啟動守門員 only', () => {
    answer('h1', status('d1', 'none'))
    render(<WatchLaunchPanel title="x" />)
    expect(start()).toHaveTextContent(t('watch.launch.start'))
    expect(screen.queryByTestId('watch-launch-wake')).toBeNull()
  })

  it('resting (the holder connected): 叫醒守門員 only', () => {
    answer('h1', status('d1', 'resting'))
    render(<WatchLaunchPanel title="x" />)
    expect(wake()).toHaveTextContent(t('watch.launch.wake'))
    expect(screen.queryByTestId('watch-launch-start')).toBeNull()
  })

  it('no button when the watcher is awake, when its status cannot be read, or when its holder is a host this App is not connected to', () => {
    answer('h1', status('d1', 'awake'))
    const { unmount } = render(<WatchLaunchPanel title="x" />)
    expect(screen.queryAllByRole('button')).toHaveLength(0)
    unmount()
    useWatchStatusStore.getState().reset()
    const u = render(<WatchGuardianPane pane={{ id: 'p', content: { kind: 'watch-guardian' } }} isActive />)
    expect(screen.queryAllByRole('button')).toHaveLength(0) // unknown
    u.unmount()
    answer('h1', status('d1', 'none', { current: { alias: 'far', host_id: 'dfar', session_id: 'x', state: 'resting' } }))
    render(<WatchGuardianPane pane={{ id: 'p', content: { kind: 'watch-guardian' } }} isActive />)
    expect(screen.queryByTestId('watch-launch-start')).toBeNull() // the watcher exists, somewhere this App cannot reach
    expect(screen.queryByTestId('watch-launch-wake')).toBeNull()
  })

  it('the guardian pane\'s none panel carries the start button', () => {
    answer('h1', status('d1', 'none'))
    render(<WatchGuardianPane pane={{ id: 'p', content: { kind: 'watch-guardian' } }} isActive />)
    expect(start()).toBeInTheDocument()
  })
})

describe('opening the panel asks nothing', () => {
  it('neither state sends a request on render, on re-render or on a status change', async () => {
    answer('h1', status('d1', 'none'))
    const { rerender } = render(<WatchLaunchPanel title="x" />)
    rerender(<WatchLaunchPanel title="y" />)
    act(() => answer('h1', status('d1', 'resting')))
    rerender(<WatchLaunchPanel title="z" />)
    await waitFor(() => expect(readWatchRow).toHaveBeenCalled())
    expect(hostFetch).not.toHaveBeenCalled()
  })
})

describe('啟動守門員', () => {
  beforeEach(() => answer('h1', status('d1', 'none')))

  it('press: POST /api/watch/start, busy (disabled, 啟動中…) while the request is on its way', async () => {
    let finish!: (r: Response) => void
    hostFetch.mockReturnValueOnce(new Promise<Response>((r) => { finish = r }))
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(start())
    expect(hostFetch).toHaveBeenCalledWith('h1', '/api/watch/start', expect.objectContaining({ method: 'POST' }))
    expect(start()).toBeDisabled()
    expect(start()).toHaveTextContent(t('watch.launch.starting'))
    await act(async () => { finish(new Response('{}', { status: 409 })) })
  })

  it('200: says the guardian is already awake and frees the button', async () => {
    reply(200, { host: 'mlab', host_id: 'd1', state: 'awake', session_id: 's' })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(start())
    await waitFor(() => expect(note()).toHaveTextContent(t('watch.launch.awake_already')))
    expect(start()).toBeEnabled()
  })

  it('202: names the host it starts on, and stays busy', async () => {
    reply(202, { host: 'air26', host_id: 'd2' })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(start())
    await waitFor(() => expect(note()).toHaveTextContent(t('watch.launch.starting_on', { host: 'air26' })))
    expect(start()).toBeDisabled()
  })

  it('409: the time it may start again, in local time (today: HH:mm)', async () => {
    vi.useFakeTimers({ toFake: ['Date'] }); vi.setSystemTime(new Date(2026, 9, 17, 10, 0, 0))
    reply(409, { error: 'cooldown', detail: new Date(2026, 9, 17, 14, 5, 0).toISOString() })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(start())
    await waitFor(() => expect(note()).toHaveTextContent(t('watch.launch.cooldown_start', { time: '14:05' })))
    expect(start()).toBeEnabled()
  })

  it('409: another day carries its date', async () => {
    vi.useFakeTimers({ toFake: ['Date'] }); vi.setSystemTime(new Date(2026, 9, 17, 10, 0, 0))
    reply(409, { error: 'cooldown', detail: new Date(2026, 9, 19, 9, 3, 0).toISOString() })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(start())
    await waitFor(() => expect(note()).toHaveTextContent(t('watch.launch.cooldown_start', { time: `2026-10-19 ${pad(9)}:${pad(3)}` })))
  })

  it('403: shows the daemon\'s own text, and frees the button', async () => {
    reply(403, { error: 'forbidden', detail: 'only the admin token starts the watcher' })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(start())
    await waitFor(() => expect(note()).toHaveTextContent('only the admin token starts the watcher'))
    expect(note()).toHaveAttribute('role', 'alert')
    expect(start()).toBeEnabled()
  })

  it('a network error says so, and frees the button', async () => {
    hostFetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(start())
    await waitFor(() => expect(note()).toHaveTextContent(t('watch.launch.err_network')))
    expect(start()).toBeEnabled()
  })

  it('another failure shows the daemon\'s text, or its status when it has none', async () => {
    reply(502, { error: 'start_failed', detail: 'tmux: no server' })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(start())
    await waitFor(() => expect(note()).toHaveTextContent('tmux: no server'))
    hostFetch.mockResolvedValueOnce(new Response('nope', { status: 500 }))
    fireEvent.click(start())
    await waitFor(() => expect(note()).toHaveTextContent(t('watch.launch.err_generic', { status: 500 })))
  })

  it('busy until the watcher registers: no `watch.changed` keeps it busy; awake releases it', async () => {
    reply(202, { host: 'mlab', host_id: 'd1' })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(start())
    await waitFor(() => expect(start()).toBeDisabled())
    await new Promise((r) => setTimeout(r, 30))
    expect(start()).toBeDisabled()
    act(() => answer('h1', status('d1', 'awake')))
    expect(useWatchLaunchStore.getState().slot).toBeNull()
    act(() => answer('h1', status('d1', 'none'))) // later, the watcher is gone again: the button is not stuck busy
    expect(start()).toBeEnabled()
  })
})

describe('叫醒守門員', () => {
  beforeEach(() => answer('h1', status('d1', 'resting')))

  it('press: POST /api/watch/wake with `{}` to the holder', async () => {
    reply(200, { starting: false })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(wake())
    expect(hostFetch).toHaveBeenCalledWith('h1', '/api/watch/wake', expect.objectContaining({ method: 'POST', body: '{}' }))
    await waitFor(() => expect(wake()).toBeDisabled())
    expect(wake()).toHaveTextContent(t('watch.launch.waking'))
  })

  it('200 and 202 are both success: busy', async () => {
    reply(202, { starting: true })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(wake())
    await waitFor(() => expect(wake()).toBeDisabled())
    expect(screen.queryByTestId('watch-launch-note')).toBeNull()
  })

  it('409: the time it may be woken again', async () => {
    vi.useFakeTimers({ toFake: ['Date'] }); vi.setSystemTime(new Date(2026, 9, 17, 10, 0, 0))
    reply(409, { error: 'cooldown', detail: new Date(2026, 9, 17, 11, 30, 0).toISOString() })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(wake())
    await waitFor(() => expect(note()).toHaveTextContent(t('watch.launch.cooldown_wake', { time: '11:30' })))
    expect(wake()).toBeEnabled()
  })

  it('403: the daemon\'s own text', async () => {
    reply(403, { error: 'forbidden', detail: 'only the admin token wakes the watcher' })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(wake())
    await waitFor(() => expect(note()).toHaveTextContent('only the admin token wakes the watcher'))
    expect(wake()).toBeEnabled()
  })

  it('a network error frees the button', async () => {
    hostFetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(wake())
    await waitFor(() => expect(note()).toHaveTextContent(t('watch.launch.err_network')))
    expect(wake()).toBeEnabled()
  })

  it('awake releases the busy state', async () => {
    reply(200, { starting: false })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(wake())
    await waitFor(() => expect(wake()).toBeDisabled())
    act(() => answer('h1', status('d1', 'awake')))
    act(() => answer('h1', status('d1', 'resting')))
    expect(wake()).toBeEnabled()
  })
})

describe('busy times out after 120 s', () => {
  // Only the timer pair is faked, and `waitFor` (which polls on those timers) is not used: the clock is moved by hand.
  beforeEach(() => { vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] }) })
  const turn = (ms = 0) => act(async () => { await vi.advanceTimersByTimeAsync(ms) })
  const untilDisabled = async (id: string) => {
    for (let i = 0; i < 50 && !(screen.getByTestId(id) as HTMLButtonElement).disabled; i += 1) await turn()
    expect(screen.getByTestId(id)).toBeDisabled()
  }
  const noTimeoutLine = () => expect(screen.queryByText(t('watch.launch.timeout'))).toBeNull() // (a busy start has its own "Starting on" line)
  const pressed = async (state: 'none' | 'resting') => {
    answer('h1', status('d1', state))
    reply(202, { host: 'mlab', host_id: 'd1' })
    render(<WatchLaunchPanel title="x" />)
    const id = state === 'none' ? 'watch-launch-start' : 'watch-launch-wake'
    fireEvent.click(screen.getByTestId(id))
    await untilDisabled(id)
    return id
  }

  it.each(['none', 'resting'] as const)('%s: busy at 119_999 ms; at 120_000 ms the line appears and the button is pressable again', async (state) => {
    const id = await pressed(state)
    await turn(119_999)
    expect(screen.getByTestId(id)).toBeDisabled()
    noTimeoutLine()
    await turn(1)
    expect(note()).toHaveTextContent(t('watch.launch.timeout'))
    expect(screen.getByTestId(id)).toBeEnabled()
    expect(screen.getByTestId(id)).toHaveTextContent(t(state === 'none' ? 'watch.launch.start' : 'watch.launch.wake'))
    // …and pressing it asks again
    reply(202, { host: 'mlab', host_id: 'd1' })
    fireEvent.click(screen.getByTestId(id))
    expect(hostFetch).toHaveBeenCalledTimes(2)
    await untilDisabled(id)
    noTimeoutLine()
  })

  it('the watcher registering within the 120 s releases it, and the deadline then shows nothing', async () => {
    const id = await pressed('none')
    await turn(60_000)
    act(() => answer('h1', status('d1', 'awake')))
    expect(vi.getTimerCount()).toBe(0)
    act(() => answer('h1', status('d1', 'none')))
    expect(screen.getByTestId(id)).toBeEnabled()
    await turn(120_000)
    noTimeoutLine()
    expect(screen.getByTestId(id)).toBeEnabled()
  })

  it('the watcher registering clears the timeout line', async () => {
    await pressed('none')
    await turn(120_000)
    expect(note()).toHaveTextContent(t('watch.launch.timeout'))
    act(() => answer('h1', status('d1', 'awake')))
    act(() => answer('h1', status('d1', 'none')))
    noTimeoutLine()
  })

  it('a host re-pointed while busy drops the timer: the deadline shows nothing', async () => {
    const repoint = (ip: string) => act(() => useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h1: { ...useHostStore.getState().hosts.h1, ip } } } as never))
    repoint('9.9.9.9'); const bare = vi.getTimerCount(); repoint('1') // a host change may leave a timer of its own: that is the count without ours
    const id = await pressed('none')
    expect(vi.getTimerCount()).toBe(bare + 1)
    await turn(10_000)
    repoint('9.9.9.9')
    expect(vi.getTimerCount()).toBe(bare)
    await turn(120_000)
    noTimeoutLine()
    expect(screen.getByTestId(id)).toBeEnabled()
  })

  it('an older request\'s deadline does not release a newer one', async () => {
    const id = await pressed('none') // seq 1 at 0 s
    await turn(50_000)
    act(() => useWatchLaunchStore.setState({ slot: null })) // freed, e.g. by a host change, then pressed again
    reply(202, { host: 'mlab', host_id: 'd1' })
    fireEvent.click(screen.getByTestId(id))
    await untilDisabled(id) // seq 2 at 50 s
    await turn(70_000) // 120 s after the first press
    expect(screen.getByTestId(id)).toBeDisabled()
    noTimeoutLine()
    await turn(50_000)
    expect(note()).toHaveTextContent(t('watch.launch.timeout'))
  })
})

describe('re-point', () => {
  it('an answer of the old endpoint, arriving after the host was re-pointed, does not make the panel busy', async () => {
    answer('h1', status('d1', 'none'))
    let finish!: (r: Response) => void
    hostFetch.mockReturnValueOnce(new Promise<Response>((r) => { finish = r }))
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(start())
    act(() => useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h1: { ...useHostStore.getState().hosts.h1, ip: '9.9.9.9' } } } as never))
    await act(async () => { finish(new Response(JSON.stringify({ host: 'mlab', host_id: 'd1' }), { status: 202 })) })
    expect(useWatchLaunchStore.getState().slot).toBeNull()
    expect(start()).toBeEnabled()
    expect(screen.queryByTestId('watch-launch-note')).toBeNull()
  })
})

describe('the host name comes through the look', () => {
  const lookNames = () => {
    const hosts = useHostStore.getState().hosts
    useHostLookStore.setState({ looks: { [lookKeyOf(hosts.h1, {})]: { name: 'Look Mlab' }, [lookKeyOf(hosts.h2, {})]: { name: 'Look Air' } } })
  }

  it('a start on the second host is named by that host\'s look, not the daemon\'s alias nor HostConfig.name', async () => {
    answer('h1', status('d1', 'none'))
    lookNames()
    reply(202, { host: 'air26-alias', host_id: 'd2' })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(start())
    await waitFor(() => expect(note()).toHaveTextContent(t('watch.launch.starting_on', { host: 'Look Air' })))
  })

  it('and on the first host by the first host\'s look', async () => {
    answer('h1', status('d1', 'none'))
    lookNames()
    reply(202, { host: 'mlab-alias', host_id: 'd1' })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(start())
    await waitFor(() => expect(note()).toHaveTextContent(t('watch.launch.starting_on', { host: 'Look Mlab' })))
  })

  it('a host this App does not have is named by the alias the daemon gave', async () => {
    answer('h1', status('d1', 'none'))
    reply(202, { host: 'far-alias', host_id: 'dzzz' })
    render(<WatchLaunchPanel title="x" />)
    fireEvent.click(start())
    await waitFor(() => expect(note()).toHaveTextContent(t('watch.launch.starting_on', { host: 'far-alias' })))
  })
})
