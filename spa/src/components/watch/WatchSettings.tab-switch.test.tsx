// Regression (CLAUDE.md tab-hosted checklist): text typed into a 設定 field and not sent yet survives switching to another tab and back.
// The pane really unmounts (keepAliveCount 0), so this mounts the real TabContent.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within, waitFor } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { WatchHostPane } from './WatchHostPane'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { useFleetStore } from '../../lib/fleet/store'
import { clearViewMemory } from '../../lib/watch/view-memory'
import { clearWatchDrafts } from '../../lib/watch/draft-memory'
import { useHostStore } from '../../stores/useHostStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { host, primaryAnswer } from '../../lib/fleet/__fixtures__/builders'
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

const Other = () => <div data-testid="other-tab" />
const hostTab: Tab = { ...createTab({ kind: 'watch-host', fleetHostId: 'd-a' }), id: 'watch-host-d-a' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [hostTab, dashTab]

beforeEach(() => {
  cleanup(); clearModuleRegistry(); clearViewMemory(); clearWatchDrafts()
  registerModule({ id: 'watch', name: 'Watch', panes: [{ kind: 'watch-host', component: WatchHostPane }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } }, hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: {} } as never)
  const s = useFleetStore.getState()
  s.reset()
  const gen = s.begin('cfg-a')
  s.setSupport('cfg-a', gen, 'yes')
  s.applyFetched('cfg-a', gen, primaryAnswer(1, [host('d-a', { alias: 'air26' })]))
})
afterEach(() => { clearViewMemory(); clearWatchDrafts() })

describe('設定 across tab switches', () => {
  it('keeps a half-typed prompt and a half-typed number when the reader switches away and back', async () => {
    const { rerender } = render(<TabContent activeTab={hostTab} allTabs={all} />)
    fireEvent.click(within(screen.getByTestId('page-switch')).getByRole('button', { name: t('watch.host.page.settings') }))
    fireEvent.change(await screen.findByTestId('watch-set-start_prompt'), { target: { value: 'half a prompt' } })
    fireEvent.change(screen.getByTestId('watch-set-y_mb'), { target: { value: '20' } })

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('watch-set-start_prompt')).toBeNull() // really unmounted, not hidden

    rerender(<TabContent activeTab={hostTab} allTabs={all} />)
    await waitFor(() => expect(screen.getByTestId('watch-set-start_prompt')).toHaveValue('half a prompt'))
    expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('20')
    expect(screen.getByTestId('watch-set-t_s')).toHaveValue('60') // an untouched field shows the stored value
  }, 30_000)
})
