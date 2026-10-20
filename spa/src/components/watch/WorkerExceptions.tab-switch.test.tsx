// Regression (CLAUDE.md tab-hosted checklist): a pause end time typed into the 暫停 form and not sent yet survives switching to another tab
// and back. The pane really unmounts (keepAliveCount 0), so this mounts the real TabContent.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within, waitFor } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { WatchHostPane } from './WatchHostPane'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { useFleetStore } from '../../lib/fleet/store'
import { usePoolStore } from '../../lib/pool/store'
import { clearViewMemory } from '../../lib/watch/view-memory'
import { clearWatchDrafts } from '../../lib/watch/draft-memory'
import { clearWorkerDrafts } from '../../lib/pool/worker-draft-memory'
import { parseFacts, parseHostsAnswer } from '../../lib/pool/parse'
import darwinJson from '../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../lib/pool/__fixtures__/pool_hosts_response.json'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { fingerprintOf } from '../../lib/nex/nex-host-effects'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { host, primaryAnswer } from '../../lib/fleet/__fixtures__/builders'
import { useI18nStore } from '../../stores/useI18nStore'
const t = (k: string) => useI18nStore.getState().t(k)

vi.mock('../../lib/host-api', async (orig) => ({
  ...(await orig<typeof import('../../lib/host-api')>()),
  hostFetch: async () => new Response('{}', { status: 200 }),
}))

// The host tab starts the pool's poll (as the cells do); it answers with the host this test seeds, so the poll does not wipe the seeded store.
const fetchPoolHosts = vi.fn()
vi.mock('../../lib/pool/api', async (orig) => ({
  ...(await orig<typeof import('../../lib/pool/api')>()),
  fetchPoolHosts: (...a: unknown[]) => fetchPoolHosts(...a),
}))

const Other = () => <div data-testid="other-tab" />
const hostTab: Tab = { ...createTab({ kind: 'watch-host', fleetHostId: 'd-a' }), id: 'watch-host-d-a' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [hostTab, dashTab]

beforeEach(() => {
  cleanup(); clearModuleRegistry(); clearViewMemory(); clearWatchDrafts(); clearWorkerDrafts()
  registerModule({ id: 'watch', name: 'Watch', panes: [{ kind: 'watch-host', component: WatchHostPane }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } }, hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: { 'cfg-a': { status: 'connected' } } } as never)
  // Fresh and of this host's identity, so the pool's identity refresh does not read /api/info again and replace the seeded entry.
  useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', daemonCapabilities: ['pool.v1', 'pool.control.v1'], fetchedAt: Date.now(), fingerprint: fingerprintOf('cfg-a') } } } as never)
  const s = useFleetStore.getState()
  s.reset()
  const gen = s.begin('cfg-a')
  s.setSupport('cfg-a', gen, 'yes')
  s.applyFetched('cfg-a', gen, primaryAnswer(1, [host('d-a', { alias: 'air26' })]))
  const facts = parseFacts(darwinJson)!
  usePoolStore.getState().reset()
  const seeded = [{ ...parseHostsAnswer(hostsJson)![0], alias: 'air26', reachable: true, facts }]
  fetchPoolHosts.mockReset()
  fetchPoolHosts.mockResolvedValue(seeded)
  usePoolStore.setState({ hosts: seeded, sourceHostId: 'cfg-a' })
})
afterEach(() => { clearViewMemory(); clearWatchDrafts(); clearWorkerDrafts() })

describe('Worker 暫停 across tab switches', () => {
  it('keeps a half-typed pause end time when the reader switches away and back', async () => {
    const { rerender } = render(<TabContent activeTab={hostTab} allTabs={all} />)
    fireEvent.click(within(screen.getByTestId('page-switch')).getByRole('button', { name: t('watch.host.page.settings') }))
    fireEvent.change(await screen.findByTestId('pool-pause-clock'), { target: { value: '19:45' } })

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('pool-pause-clock')).toBeNull() // really unmounted, not hidden

    rerender(<TabContent activeTab={hostTab} allTabs={all} />)
    await waitFor(() => expect(screen.getByTestId('pool-pause-clock')).toHaveValue('19:45'))
  }, 30_000)
})
