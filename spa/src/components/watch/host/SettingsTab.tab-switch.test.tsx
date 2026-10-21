// Regression (CLAUDE.md tab-hosted checklist, MON-9a-1): what the reader chose or typed on a host's 設定 tab and has not sent — the classes a pause lets through, its marks, a half-typed end time, a half-typed
// guardian path — and where the tab was scrolled, survive switching to another tab and back. The pane really unmounts (keepAliveCount 0), so this mounts the real TabContent.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within, waitFor } from '@testing-library/react'
import { TabContent } from '../../TabContent'
import { WatchHostPane } from '../WatchHostPane'
import { registerModule, clearModuleRegistry } from '../../../lib/module-registry'
import { clearViewMemory } from '../../../lib/watch/view-memory'
import { clearWatchDrafts } from '../../../lib/watch/draft-memory'
import { clearWorkerDrafts } from '../../../lib/pool/worker-draft-memory'
import { useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { fingerprintOf } from '../../../lib/nex/nex-host-effects'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { createTab } from '../../../types/tab'
import type { Tab } from '../../../types/tab'
import { poolHostOf, seedPool, SEED_HOST_ID } from '../host-pane-seed'
import { useI18nStore } from '../../../stores/useI18nStore'
const t = (k: string) => useI18nStore.getState().t(k)

const ROW = { enabled: true, mode: 'patrol', patrol_idle_min: 120, auto_wake: true, agent_command: 'claude', workdir: '~/w', start_prompt: '', wake_prompt: '' }
vi.mock('../../../lib/host-api', async (orig) => ({
  ...(await orig<typeof import('../../../lib/host-api')>()),
  hostFetch: async (_id: string, path: string) => new Response(path === '/api/hostconfig' ? JSON.stringify({ watch: { items: ROW, revision: 3 }, wwdAlerts: { items: { push: true }, revision: 2 } }) : '{}', { status: 200 }),
}))

// The host tab starts the pool's poll (as the cells do); it answers with the hosts this test seeds, so the poll does not wipe the seeded store.
const fetchPoolHosts = vi.fn()
vi.mock('../../../lib/pool/api', async (orig) => ({
  ...(await orig<typeof import('../../../lib/pool/api')>()),
  fetchPoolHosts: (...a: unknown[]) => fetchPoolHosts(...a),
}))

const MSERV_ID = 'mserv-host-id'
const Other = () => <div data-testid="other-tab" />
const mlabTab: Tab = { ...createTab({ kind: 'watch-host', poolHostId: SEED_HOST_ID }), id: `watch-host-${SEED_HOST_ID}` }
const mservTab: Tab = { ...createTab({ kind: 'watch-host', poolHostId: MSERV_ID }), id: `watch-host-${MSERV_ID}` }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [mlabTab, mservTab, dashTab]

const openSettings = () => fireEvent.click(within(screen.getByTestId('page-switch')).getByRole('button', { name: t('watch.host.page.settings') }))

beforeEach(() => {
  cleanup(); clearModuleRegistry(); clearViewMemory(); clearWatchDrafts(); clearWorkerDrafts()
  registerModule({ id: 'watch', name: 'Watch', panes: [{ kind: 'watch-host', component: WatchHostPane }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } }, hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: { 'cfg-a': { status: 'connected' } } } as never)
  // wwd's host id names cfg-a as the App host that fronts air26; mserv has no daemon of its own.
  useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', fetchedAt: Date.now(), daemonCapabilities: ['pool.v1', 'pool.control.v1'], wwd: { hostId: SEED_HOST_ID }, fingerprint: fingerprintOf('cfg-a') } } } as never)
  const seeded = [poolHostOf(), poolHostOf({ host_id: MSERV_ID, alias: 'mserv' })]
  fetchPoolHosts.mockReset()
  fetchPoolHosts.mockResolvedValue(seeded)
  seedPool(seeded)
})
afterEach(() => { clearViewMemory(); clearWatchDrafts(); clearWorkerDrafts() })

describe('設定 tab across tab switches', () => {
  it('mserv (no daemon): the classes and marks picked, a half-typed end time and the scroll place are still there after switching away and back', async () => {
    const { rerender } = render(<TabContent activeTab={mservTab} allTabs={all} />)
    openSettings()
    fireEvent.click(await screen.findByTestId('pool-pause-allow-release'))
    fireEvent.click(screen.getByTestId('pool-pause-flag-release'))
    fireEvent.change(screen.getByTestId('pool-pause-clock'), { target: { value: '19:45' } })
    const scroller = screen.getByTestId(`watch-host-${MSERV_ID}`)
    scroller.scrollTop = 321
    fireEvent.scroll(scroller)

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('pool-pause-clock')).toBeNull() // really unmounted, not hidden

    rerender(<TabContent activeTab={mservTab} allTabs={all} />)
    await waitFor(() => expect(screen.getByTestId('pool-pause-clock')).toHaveValue('19:45'))
    expect(screen.getByTestId('pool-pause-allow-release')).toBeChecked()
    expect(screen.getByTestId('pool-pause-allow-merge')).not.toBeChecked()
    expect(screen.getByTestId('pool-pause-flag-release')).toBeChecked()
    expect(screen.getByTestId(`watch-host-${MSERV_ID}`).scrollTop).toBe(321)
    expect(screen.queryByTestId('watch-set-guardian')).toBeNull() // no daemon, no guardian section
  }, 30_000)

  it('the 推播 switch reads its state again after switching away and back, and shows the same value', async () => {
    const { rerender } = render(<TabContent activeTab={mlabTab} allTabs={all} />)
    openSettings()
    await waitFor(() => expect(screen.getByTestId('push-toggle')).toHaveAttribute('aria-checked', 'true'))
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('push-toggle')).toBeNull()
    rerender(<TabContent activeTab={mlabTab} allTabs={all} />)
    await waitFor(() => expect(screen.getByTestId('push-toggle')).toHaveAttribute('aria-checked', 'true'))
  it('the 側欄顯示 colour mode (主控台／終端機) chosen on a host is still chosen after switching away and back', async () => {
    const { rerender } = render(<TabContent activeTab={mservTab} allTabs={all} />)
    openSettings()
    const modeBtn = async (name: string) => within(await screen.findByTestId('look-settings')).getByRole('button', { name })
    expect(await modeBtn(t('hosts.color.mode.terminal'))).toHaveAttribute('aria-pressed', 'true') // the sidebar's layer is the default
    fireEvent.click(await modeBtn(t('hosts.color.mode.console')))
    expect(await modeBtn(t('hosts.color.mode.console'))).toHaveAttribute('aria-pressed', 'true')

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('look-settings')).toBeNull()

    rerender(<TabContent activeTab={mservTab} allTabs={all} />)
    await waitFor(() => expect(within(screen.getByTestId('look-settings')).getByRole('button', { name: t('hosts.color.mode.console') })).toHaveAttribute('aria-pressed', 'true'))
  }, 30_000)

  it('mlab (a daemon): a half-typed guardian path is still there after switching away and back', async () => {
    const { rerender } = render(<TabContent activeTab={mlabTab} allTabs={all} />)
    openSettings()
    fireEvent.click(await screen.findByRole('button', { name: t('watch.set.guardian') }))
    fireEvent.change(await screen.findByTestId('watch-set-workdir'), { target: { value: '~/half' } })

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('watch-set-workdir')).toBeNull()

    rerender(<TabContent activeTab={mlabTab} allTabs={all} />)
    fireEvent.click(await screen.findByRole('button', { name: t('watch.set.guardian') }))
    await waitFor(() => expect(screen.getByTestId('watch-set-workdir')).toHaveValue('~/half'))
  }, 30_000)
})
