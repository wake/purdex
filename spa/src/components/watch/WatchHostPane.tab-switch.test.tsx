// Regression (CLAUDE.md tab-hosted checklist): the sub-tab, the range and the scroll of each sub-tab picked on a host tab survive switching to another tab and back. The pane really unmounts
// (keepAliveCount 0; watch-host is not a "light" kind), so this mounts the real TabContent, not WatchHostPane alone. Each host has its own memory.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within, waitFor } from '@testing-library/react'

vi.mock('../../lib/pool/jobs-driver-live', () => ({ watchJobs: vi.fn(() => () => {}), jobsDaemons: () => [] }))
vi.mock('../../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../../lib/host-api')>()), hostFetch: () => new Promise(() => {}) }))
const fetchPoolHosts = vi.fn()
vi.mock('../../lib/pool/api', async (orig) => ({ ...(await orig<typeof import('../../lib/pool/api')>()), fetchPoolHosts: (...a: unknown[]) => fetchPoolHosts(...a) }))

import { TabContent } from '../TabContent'
import { WatchHostPane } from './WatchHostPane'
import { poolHostOf, seedPool, SEED_HOST_ID } from './host-pane-seed'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { clearViewMemory } from '../../lib/watch/view-memory'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { fingerprintOf } from '../../lib/nex/nex-host-effects'
import { useI18nStore } from '../../stores/useI18nStore'
const t = (k: string) => useI18nStore.getState().t(k)

const OTHER_ID = '1234567890abcdef'
const Other = () => <div data-testid="other-tab" />
const tabOf = (id: string): Tab => ({ ...createTab({ kind: 'watch-host', poolHostId: id }), id: `watch-host-${id}` })
const hostTab = tabOf(SEED_HOST_ID)
const otherHostTab = tabOf(OTHER_ID)
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [hostTab, otherHostTab, dashTab]
const pick = (testId: string, name: string) => fireEvent.click(within(screen.getByTestId(testId)).getByRole('button', { name }))
const pressed = (testId: string, name: string) => expect(within(screen.getByTestId(testId)).getByRole('button', { name })).toHaveAttribute('aria-pressed', 'true')

// Whether the watcher section starts open depends on whether the pool lists the host yet: open it only if it is closed.
const openGuardian = () => {
  const b = screen.getByRole('button', { name: t('watch.set.guardian') })
  if (b.getAttribute('aria-expanded') !== 'true') fireEvent.click(b)
}

beforeEach(() => {
  cleanup(); clearModuleRegistry(); clearViewMemory()
  registerModule({ id: 'watch', name: 'Watch', panes: [{ kind: 'watch-host', component: WatchHostPane }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } }, hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: { 'cfg-a': { status: 'connected' } } } as never)
  useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', fetchedAt: Date.now(), daemonCapabilities: ['pool.v1'], wwd: { hostId: SEED_HOST_ID }, fingerprint: fingerprintOf('cfg-a') } } } as never)
  const hosts = [poolHostOf(), poolHostOf({ host_id: OTHER_ID, alias: 'mserv' })]
  fetchPoolHosts.mockReset()
  fetchPoolHosts.mockResolvedValue(hosts)
  seedPool(hosts)
})
afterEach(() => clearViewMemory())

describe('the host tab across tab switches', () => {
  it('keeps the sub-tab, the range and the scroll of the sub-tab when the reader switches to another tab and back; another host starts from the defaults', () => {
    const { rerender } = render(<TabContent activeTab={hostTab} allTabs={all} />)
    pressed('page-switch', t('watch.host.page.monitor'))
    pressed('range-switch', t('watch.host.range.30m'))
    pick('range-switch', t('watch.host.range.6h'))
    pick('page-switch', t('watch.host.page.procs'))
    const scroller = screen.getByTestId(`watch-host-${SEED_HOST_ID}`)
    scroller.scrollTop = 240
    fireEvent.scroll(scroller)

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('page-switch')).toBeNull() // really unmounted, not hidden
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()

    rerender(<TabContent activeTab={hostTab} allTabs={all} />)
    pressed('page-switch', t('watch.host.page.procs'))
    expect(screen.getByTestId(`watch-host-${SEED_HOST_ID}`).scrollTop).toBe(240)
    pick('page-switch', t('watch.host.page.monitor'))
    pressed('range-switch', t('watch.host.range.6h'))

    // Another host's tab is its own: 監控, 30 分, scrolled to the top.
    rerender(<TabContent activeTab={otherHostTab} allTabs={all} />)
    pressed('page-switch', t('watch.host.page.monitor'))
    pressed('range-switch', t('watch.host.range.30m'))
    expect(screen.getByTestId(`watch-host-${OTHER_ID}`).scrollTop).toBe(0)
  }, 30_000) // the first TabContent render pulls in its lazy modules, which is slow on a loaded host

  it('keeps 設定 across the switch, and the range switch shows on 監控 and 狀態 only', async () => {
    const { rerender } = render(<TabContent activeTab={hostTab} allTabs={all} />)
    pick('page-switch', t('watch.host.page.settings'))
    expect(screen.queryByTestId('range-switch')).toBeNull()
    await waitFor(() => { openGuardian(); expect(screen.getByTestId('watch-set-loading')).toBeInTheDocument() })
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('page-switch')).toBeNull()
    rerender(<TabContent activeTab={hostTab} allTabs={all} />)
    pressed('page-switch', t('watch.host.page.settings'))
    await waitFor(() => { openGuardian(); expect(screen.getByTestId('watch-set-loading')).toBeInTheDocument() })
    expect(screen.queryByTestId('range-switch')).toBeNull()
  }, 30_000)
})
