// Regression (CLAUDE.md tab-hosted checklist): a guardian that is coming up is still "busy" after the tab is switched away and back.
// The pane really unmounts (keepAliveCount 0; watch-guardian is not a "light" kind), so this mounts the real TabContent: a busy flag
// kept in the panel's own useState would come back pressable here.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor, act } from '@testing-library/react'

const hostFetch = vi.hoisted(() => vi.fn())
vi.mock('../../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../../lib/host-api')>()), hostFetch: (...a: unknown[]) => hostFetch(...a) }))
vi.mock('../../lib/watch/watch-row', async (orig) => ({
  ...(await orig<typeof import('../../lib/watch/watch-row')>()),
  readWatchRow: vi.fn(async () => ({ revision: 1, row: {}, agentCommand: 'claude --model opus', workdir: '~/w' })),
}))
import { TabContent } from '../TabContent'
import { WatchGuardianPane } from './WatchGuardianPane'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { useWatchStatusStore } from '../../lib/watch/status-store'
import { startWatchLaunch, useWatchLaunchStore } from '../../lib/watch/launch'
import type { WatchStatus } from '../../lib/watch/status-types'
import { useHostStore } from '../../stores/useHostStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { useI18nStore } from '../../stores/useI18nStore'
const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const Other = () => <div data-testid="other-tab" />
const guardianTab: Tab = { ...createTab({ kind: 'watch-guardian' }), id: 'watch-guardian' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [guardianTab, dashTab]

const status = (state: 'awake' | 'resting' | 'none'): WatchStatus =>
  ({ self: { host_id: 'd1', state, since: '2026-10-16T01:00:00Z', session_id: state === 'none' ? undefined : 'conv-1' }, enabled: true, auto_wake: true, mode: 'patrol', targets: [] })
const answer = (s: WatchStatus) => useWatchStatusStore.setState({ byHost: { h1: { kind: 'ok', status: s, at: 1 } } })

let stop: () => void = () => {}
beforeEach(() => {
  cleanup(); clearModuleRegistry(); hostFetch.mockReset()
  registerModule({ id: 'watch', name: 'Watch', panes: [{ kind: 'watch-guardian', component: WatchGuardianPane }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useWatchLaunchStore.setState({ slot: null, seq: 0 })
  useHostStore.setState({
    hosts: { h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd1' } },
    hostOrder: ['h1'], activeHostId: 'h1', runtime: { h1: { status: 'connected' } },
  } as never)
  stop = startWatchLaunch()
})
afterEach(() => { stop(); useWatchLaunchStore.setState({ slot: null, seq: 0 }) })

describe('the launch panel across tab switches', () => {
  it.each([
    ['none', 'watch-launch-start', '/api/watch/start'],
    ['resting', 'watch-launch-wake', '/api/watch/wake'],
  ] as const)('%s: still busy after switching away and back, and released when the watcher is awake', async (state, button, path) => {
    answer(status(state))
    hostFetch.mockResolvedValueOnce(new Response(JSON.stringify({ host: 'mlab', host_id: 'd1' }), { status: 202 }))
    const { rerender } = render(<TabContent activeTab={guardianTab} allTabs={all} />)
    fireEvent.click(await screen.findByTestId(button))
    await waitFor(() => expect(screen.getByTestId(button)).toBeDisabled())
    expect(hostFetch).toHaveBeenCalledWith('h1', path, expect.anything())

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId(button)).toBeNull() // really unmounted, not hidden
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()

    rerender(<TabContent activeTab={guardianTab} allTabs={all} />)
    const again = await screen.findByTestId(button)
    expect(again).toBeDisabled()
    expect(again).toHaveTextContent(t(state === 'none' ? 'watch.launch.starting' : 'watch.launch.waking'))
    fireEvent.click(again)
    expect(hostFetch).toHaveBeenCalledTimes(1)

    act(() => answer(status('awake')))
    act(() => answer(status(state)))
    expect(await screen.findByTestId(button)).toBeEnabled()
  }, 30_000) // the first TabContent render pulls in its lazy modules, which is slow on a loaded host
})
