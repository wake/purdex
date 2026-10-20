// Regression (CLAUDE.md tab-hosted checklist, plan MON-7b-2): the host page's 狀態 tab and its scroll survive switching to another tab and back, and the pause card reads again on the second mount, showing "reading" until the answer is in.
// The pane really unmounts (keepAliveCount 0), so this mounts the real TabContent.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within, waitFor } from '@testing-library/react'

vi.mock('../../../lib/pool/jobs-driver-live', () => ({ watchJobs: vi.fn(() => () => {}), jobsDaemons: () => [] }))
vi.mock('../../../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../../../lib/host-api')>()), hostFetch: () => new Promise(() => {}) }))
const fetchPoolHosts = vi.fn()
vi.mock('../../../lib/pool/api', async (orig) => ({ ...(await orig<typeof import('../../../lib/pool/api')>()), fetchPoolHosts: (...a: unknown[]) => fetchPoolHosts(...a) }))
const fetchPauses = vi.fn()
vi.mock('../../../lib/pool/control-api', async (orig) => ({ ...(await orig<typeof import('../../../lib/pool/control-api')>()), fetchPauses: (...a: unknown[]) => fetchPauses(...a), controlSource: () => 'cfg-a' }))

import { TabContent } from '../../TabContent'
import { WatchHostPane } from '../WatchHostPane'
import { poolHostOf, seedPool, SEED_HOST_ID } from '../host-pane-seed'
import { registerModule, clearModuleRegistry } from '../../../lib/module-registry'
import { clearViewMemory } from '../../../lib/watch/view-memory'
import { useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { createTab } from '../../../types/tab'
import type { Tab } from '../../../types/tab'
import { fingerprintOf } from '../../../lib/nex/nex-host-effects'
import { useI18nStore } from '../../../stores/useI18nStore'
const t = (k: string) => useI18nStore.getState().t(k)

const Other = () => <div data-testid="other-tab" />
const hostTab: Tab = { ...createTab({ kind: 'watch-host', poolHostId: SEED_HOST_ID }), id: `watch-host-${SEED_HOST_ID}` }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [hostTab, dashTab]

beforeEach(() => {
  cleanup(); clearModuleRegistry(); clearViewMemory()
  registerModule({ id: 'watch', name: 'Watch', panes: [{ kind: 'watch-host', component: WatchHostPane }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } }, hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: { 'cfg-a': { status: 'connected' } } } as never)
  useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', fetchedAt: Date.now(), daemonCapabilities: ['pool.v1'], wwd: { hostId: SEED_HOST_ID }, fingerprint: fingerprintOf('cfg-a') } } } as never)
  const facts = poolHostOf().facts!
  const hosts = [poolHostOf({ facts: { ...facts, exceptions: [{ name: 'deploy', since: facts.at - 1000, until: facts.at + 3_600_000 }] } })]
  fetchPoolHosts.mockReset(); fetchPoolHosts.mockResolvedValue(hosts)
  fetchPauses.mockReset()
  seedPool(hosts)
})
afterEach(() => clearViewMemory())

describe('狀態 across tab switches', () => {
  it('stays on 狀態, at the place it was scrolled to; the pause card reads again and shows "reading" until the answer is in', async () => {
    let answer: (v: unknown) => void = () => {}
    fetchPauses.mockResolvedValueOnce([{ name: 'deploy', until: null, pid: 7, holds: ['heavy'] }])
    const { rerender } = render(<TabContent activeTab={hostTab} allTabs={all} />)
    fireEvent.click(within(screen.getByTestId('page-switch')).getByRole('button', { name: t('watch.host.page.status') }))
    expect(screen.getByTestId('status-tab')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('pause-deploy')).toBeInTheDocument())
    const scroller = screen.getByTestId(`watch-host-${SEED_HOST_ID}`)
    scroller.scrollTop = 240
    fireEvent.scroll(scroller)

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('status-tab')).toBeNull() // really unmounted, not hidden

    fetchPauses.mockReturnValueOnce(new Promise((r) => { answer = r }))
    rerender(<TabContent activeTab={hostTab} allTabs={all} />)
    expect(screen.getByTestId('status-tab')).toBeInTheDocument()
    expect(screen.getByTestId(`watch-host-${SEED_HOST_ID}`).scrollTop).toBe(240)
    expect(fetchPauses).toHaveBeenCalledTimes(2)
    expect(screen.getByTestId('pause-loading')).toBeInTheDocument()
    answer([{ name: 'deploy', until: null, pid: 7, holds: ['heavy'] }])
    await waitFor(() => expect(screen.getByTestId('pause-deploy')).toBeInTheDocument())
  }, 30_000)
})
