// Regression (CLAUDE.md tab-hosted checklist): text typed into a 設定 field and not sent yet survives switching to another tab and back.
// The pane really unmounts (keepAliveCount 0), so this mounts the real TabContent.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within, waitFor } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { WatchHostPane } from './WatchHostPane'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { clearViewMemory } from '../../lib/watch/view-memory'
import { clearWatchDrafts } from '../../lib/watch/draft-memory'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { fingerprintOf } from '../../lib/nex/nex-host-effects'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { poolHostOf, seedPool, SEED_HOST_ID } from './host-pane-seed'
import { useI18nStore } from '../../stores/useI18nStore'
const t = (k: string) => useI18nStore.getState().t(k)

const ROW = {
  enabled: true, x_cores: 2, y_mb: 1024, t_s: 60, pressure_level: 2, pressure_t_s: 120, page_rate: 50, page_t_s: 60,
  targets: ['air26', 'mlab'], mode: 'patrol', patrol_idle_min: 120, auto_wake: true, agent_command: 'claude', workdir: '~/w', start_prompt: '', wake_prompt: '',
}
vi.mock('../../lib/host-api', async (orig) => ({
  ...(await orig<typeof import('../../lib/host-api')>()),
  hostFetch: async () => new Response(JSON.stringify({ watch: { items: ROW, revision: 3 } }), { status: 200 }),
}))

// The host tab starts the pool's poll (as the cells do); it answers with the host this test seeds, so the poll does not wipe the seeded store.
const fetchPoolHosts = vi.fn()
vi.mock('../../lib/pool/api', async (orig) => ({
  ...(await orig<typeof import('../../lib/pool/api')>()),
  fetchPoolHosts: (...a: unknown[]) => fetchPoolHosts(...a),
}))

const Other = () => <div data-testid="other-tab" />
const hostTab: Tab = { ...createTab({ kind: 'watch-host', poolHostId: SEED_HOST_ID }), id: `watch-host-${SEED_HOST_ID}` }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [hostTab, dashTab]

// Whether the watcher section starts open depends on whether the pool lists the host yet: open it only if it is closed.
const openGuardian = () => {
  const b = screen.getByRole('button', { name: t('watch.set.guardian') })
  if (b.getAttribute('aria-expanded') !== 'true') fireEvent.click(b)
}

beforeEach(() => {
  cleanup(); clearModuleRegistry(); clearViewMemory(); clearWatchDrafts()
  registerModule({ id: 'watch', name: 'Watch', panes: [{ kind: 'watch-host', component: WatchHostPane }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  fetchPoolHosts.mockReset()
  fetchPoolHosts.mockResolvedValue([poolHostOf()])
  seedPool([poolHostOf()])
  useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } }, hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: { 'cfg-a': { status: 'connected' } } } as never)
  // wwd's host id names cfg-a as the App host that fronts it: 設定 reads that daemon's `watch` row.
  useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', fetchedAt: Date.now(), daemonCapabilities: ['pool.v1'], wwd: { hostId: SEED_HOST_ID }, fingerprint: fingerprintOf('cfg-a') } } } as never)
})
afterEach(() => { clearViewMemory(); clearWatchDrafts() })

describe('設定 across tab switches', () => {
  it('keeps a half-typed prompt and a half-typed path when the reader switches away and back', async () => {
    const { rerender } = render(<TabContent activeTab={hostTab} allTabs={all} />)
    fireEvent.click(within(screen.getByTestId('page-switch')).getByRole('button', { name: t('watch.host.page.settings') }))
    await waitFor(() => { openGuardian(); expect(screen.getByTestId('watch-set-start_prompt')).toBeInTheDocument() })
    fireEvent.change(screen.getByTestId('watch-set-start_prompt'), { target: { value: 'half a prompt' } })
    fireEvent.change(screen.getByTestId('watch-set-workdir'), { target: { value: '~/half' } })
    fireEvent.change(screen.getByTestId('watch-set-patrol_idle_min'), { target: { value: '33' } })

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('watch-set-start_prompt')).toBeNull() // really unmounted, not hidden

    rerender(<TabContent activeTab={hostTab} allTabs={all} />)
    await waitFor(() => { openGuardian(); expect(screen.getByTestId('watch-set-start_prompt')).toHaveValue('half a prompt') })
    expect(screen.getByTestId('watch-set-workdir')).toHaveValue('~/half')
    expect(screen.getByTestId('watch-set-patrol_idle_min')).toHaveValue('33') // the half-typed minutes are kept
    expect(screen.getByTestId('watch-set-mode')).toHaveAttribute('aria-checked', 'true') // the switch still shows the host's value
  }, 30_000)
})
