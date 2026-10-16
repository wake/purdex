// T-a3.4: the panel the guardian tab shows when there is no session to show, and T-a3.5's launch panel (same component).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, within, waitFor } from '@testing-library/react'

const readWatchRow = vi.hoisted(() => vi.fn())
vi.mock('../../lib/watch/watch-row', async (orig) => ({ ...(await orig<typeof import('../../lib/watch/watch-row')>()), readWatchRow: (...a: unknown[]) => readWatchRow(...a) }))
import { WatchGuardianPane } from './WatchGuardianPane'
import { WatchLaunchPanel } from './WatchLaunchPanel'
import { useWatchStatusStore } from '../../lib/watch/status-store'
import type { WatchStatus } from '../../lib/watch/status-types'
import { useHostStore, lookKeyOf } from '../../stores/useHostStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { useSessionStore } from '../../stores/useSessionStore'
import zh from '../../locales/zh-TW.json'
import { useI18nStore } from '../../stores/useI18nStore'
const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const status = (hostId: string, state: 'awake' | 'resting' | 'none', extra: Partial<WatchStatus> = {}): WatchStatus =>
  ({ self: { host_id: hostId, state, since: '2026-10-16T01:00:00Z', session_id: state === 'none' ? undefined : 'conv-1234' }, enabled: true, auto_wake: true, mode: 'patrol', targets: [], ...extra })
const answer = (appHost: string, s: WatchStatus) => useWatchStatusStore.setState((st) => ({ byHost: { ...st.byHost, [appHost]: { kind: 'ok', status: s, at: 1 } } }))
const pane = { id: 'watch-guardian.p', content: { kind: 'watch-guardian' as const } }

beforeEach(() => {
  cleanup()
  readWatchRow.mockReset()
  readWatchRow.mockResolvedValue({ revision: 1, row: {}, agentCommand: 'claude --model opus', workdir: '~/Workspace/wake/mlab-purdex-watch' })
  useWatchStatusStore.getState().reset()
  useSessionStore.setState({ sessions: {} })
  useHostLookStore.setState({ looks: {} })
  useHostStore.setState({
    hosts: { h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd1' } },
    hostOrder: ['h1'], activeHostId: 'h1', runtime: { h1: { status: 'connected' } },
  } as never)
})

describe('WatchGuardianPane headings', () => {
  it('none: says there is no guardian and what starting one does (no button yet)', () => {
    answer('h1', status('d1', 'none'))
    render(<WatchGuardianPane pane={pane} isActive />)
    expect(screen.getByTestId('watch-launch-title')).toHaveTextContent(t('watch.launch.none_title'))
    expect(screen.getByTestId('watch-launch-hint')).toHaveTextContent(t('watch.launch.none_hint'))
    expect(screen.queryAllByRole('button')).toHaveLength(0)
  })

  it('unknown: says the status cannot be read', () => {
    render(<WatchGuardianPane pane={pane} isActive />)
    expect(screen.getByTestId('watch-launch-title')).toHaveTextContent(t('watch.launch.unknown_title'))
  })

  it('the holder is not connected to this App: names its alias', () => {
    answer('h1', status('d1', 'none', { current: { alias: 'air26', host_id: 'dfar', session_id: 'x', state: 'awake' } }))
    render(<WatchGuardianPane pane={pane} isActive />)
    expect(screen.getByTestId('watch-launch-title')).toHaveTextContent(t('watch.launch.far_title', { alias: 'air26' }))
  })

  it('the holder is connected but its session list has no pdx-watch', async () => {
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [] } })
    render(<WatchGuardianPane pane={pane} isActive />)
    expect(screen.getByTestId('watch-launch-title')).toHaveTextContent(t('watch.launch.no_session_title', { alias: 'mlab' }))
    expect(await screen.findByTestId('watch-launch-command')).toHaveTextContent('claude --model opus')
  })
})

describe('the host name comes through the look', () => {
  const setLookName = () => {
    const key = lookKeyOf(useHostStore.getState().hosts.h1, {})
    useHostLookStore.setState({ looks: { [key]: { name: 'Look Name' } } })
  }

  it('the guardian pane title uses the look name, not HostConfig.name', () => {
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [] } })
    setLookName()
    render(<WatchGuardianPane pane={pane} isActive />)
    expect(screen.getByTestId('watch-launch-title')).toHaveTextContent(t('watch.launch.no_session_title', { alias: 'Look Name' }))
  })

  it('the launch panel host row uses the look name', () => {
    answer('h1', status('d1', 'resting'))
    setLookName()
    render(<WatchLaunchPanel title="x" />)
    expect(screen.getByTestId('watch-launch-host')).toHaveTextContent('Look Name')
  })
})

describe('the watcher\'s launch panel', () => {
  it('lists host, pdx-watch, workdir, agent_command and the conversation it resumes; no button', async () => {
    answer('h1', status('d1', 'resting'))
    render(<WatchLaunchPanel title={t('watch.launch.not_running')} />)
    const p = screen.getByTestId('watch-launch-panel')
    expect(within(p).getByTestId('watch-launch-host')).toHaveTextContent('mlab')
    expect(within(p).getByTestId('watch-launch-session')).toHaveTextContent('pdx-watch')
    await waitFor(() => expect(within(p).getByTestId('watch-launch-workdir')).toHaveTextContent('~/Workspace/wake/mlab-purdex-watch'))
    expect(within(p).getByTestId('watch-launch-command')).toHaveTextContent('claude --model opus')
    expect(within(p).getByTestId('watch-launch-conversation')).toHaveTextContent('conv-1234')
    expect(readWatchRow).toHaveBeenCalledWith('h1')
    expect(screen.queryAllByRole('button')).toHaveLength(0)
  })

  it('shows the conversation only while resting (an awake watcher is not being resumed)', () => {
    answer('h1', status('d1', 'awake'))
    render(<WatchLaunchPanel title="x" />)
    expect(screen.queryByTestId('watch-launch-conversation')).toBeNull()
  })

  it('reads the row again when the holder changes, and not otherwise', async () => {
    answer('h1', status('d1', 'resting'))
    const { rerender } = render(<WatchLaunchPanel title="x" />)
    await waitFor(() => expect(readWatchRow).toHaveBeenCalledTimes(1))
    rerender(<WatchLaunchPanel title="y" />)
    expect(readWatchRow).toHaveBeenCalledTimes(1)
  })

  it('a row that cannot be read leaves the command and workdir out, not blank', async () => {
    readWatchRow.mockResolvedValue(null)
    answer('h1', status('d1', 'resting'))
    render(<WatchLaunchPanel title="x" />)
    await waitFor(() => expect(readWatchRow).toHaveBeenCalled())
    expect(screen.queryByTestId('watch-launch-command')).toBeNull()
    expect(screen.queryByTestId('watch-launch-workdir')).toBeNull()
  })
})

describe('the zh-TW copy is the plan\'s wording', () => {
  it('headings', () => {
    const z = zh as Record<string, string>
    expect(z['watch.launch.none_title']).toBe('這台主機群目前沒有守門員')
    expect(z['watch.launch.none_hint']).toBe('啟動後，它會看管重指令名額與記憶體壓力，事件交給它處理。')
    expect(z['watch.launch.unknown_title']).toBe('讀不到守門員狀態')
    expect(z['watch.launch.far_title']).toBe('守門員在 {{alias}}，這台 App 沒有連到它')
    expect(z['watch.launch.no_session_title']).toBe('{{alias}} 上沒有 pdx-watch')
  })
})
