// Regression (CLAUDE.md tab-hosted checklist): the 事件 tab's chip filters and its scroll survive switching to another tab and back. The pane really unmounts (keepAliveCount 0; watch-events is not a "light" kind),
// so this mounts the real TabContent.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, within, waitFor } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { WatchEventsPane } from './WatchEventsPane'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { useAlertsStore } from '../../lib/pool/alerts-store'
import { usePoolStore } from '../../lib/pool/store'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { clearViewMemory, readEventsScroll } from '../../lib/watch/view-memory'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'

const fetchObservations = vi.hoisted(() => vi.fn())
vi.mock('../../lib/pool/observations-api', async (orig) => ({ ...(await orig<typeof import('../../lib/pool/observations-api')>()), fetchObservations }))
vi.mock('../../lib/pool/use-monitor-hosts', () => ({
  useMonitorHosts: () => [
    { hostId: 'h-mlab', alias: 'mlab', look: { indicator: 'ring' } },
    { hostId: 'h-mserv', alias: 'mserv', look: { indicator: 'ring' } },
  ],
}))

const t = (k: string) => useI18nStore.getState().t(k)
const Other = () => <div data-testid="other-tab" />
const eventsTab: Tab = { ...createTab({ kind: 'watch-events' }), id: 'watch-events' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [eventsTab, dashTab]
const NOW = Date.now()
const alert = (key: string) => ({ key, kind: 'orphans', level: 'digest', since: NOW - 60_000, last: NOW, clearedAt: null, detail: key })
const obs = (id: string) => ({ id, host: 'mserv', session: 's', via: 'session', command: 'c', signature: `sig-${id}`, startedAt: NOW - 120_000, endedAt: NOW - 60_000, cpuSeconds: 1, cpuPeak: 100, memPeakMb: 1, procsPeak: 1 })

beforeEach(() => {
  cleanup(); clearModuleRegistry(); clearViewMemory(); fetchObservations.mockReset()
  registerModule({ id: 'watch', name: 'Watch', panes: [{ kind: 'watch-events', component: WatchEventsPane }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  usePoolStore.setState({ sourceHostId: 'src', hosts: [] } as never)
  useNexHostStore.setState({ byHost: { src: { phase: 'ready', daemonCapabilities: ['pool.alerts.v1', 'pool.observations.v1'] } } } as never)
  useAlertsStore.setState({ byHost: { mlab: { kind: 'ok', known: true, at: NOW, alerts: [alert('a1')] } }, source: null })
  fetchObservations.mockImplementation(async (_s: string, alias: string) => ({ host: alias, known: true, observations: alias === 'mserv' ? [obs('o1')] : [], truncated: false, nextUntil: null }))
})
afterEach(() => clearViewMemory())

describe('the events tab across tab switches', () => {
  it('keeps the filters the reader picked and the scroll when they switch to another tab and back', async () => {
    const { rerender } = render(<TabContent activeTab={eventsTab} allTabs={all} />)
    await waitFor(() => expect(screen.getAllByTestId('watch-event')).toHaveLength(2))
    fireEvent.click(within(screen.getByTestId('filter-host')).getByRole('button', { name: 'mserv' }))
    fireEvent.click(within(screen.getByTestId('filter-source')).getByRole('button', { name: t('watch.events.filter.source_obs') }))
    expect(screen.getAllByTestId('watch-event')).toHaveLength(1)
    const pane = screen.getByTestId('watch-events-pane')
    pane.scrollTop = 180
    fireEvent.scroll(pane)
    expect(readEventsScroll()).toBe(180)

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('watch-events-pane')).toBeNull() // really unmounted, not hidden
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()

    rerender(<TabContent activeTab={eventsTab} allTabs={all} />)
    expect(within(screen.getByTestId('filter-host')).getByRole('button', { name: 'mserv' })).toHaveAttribute('aria-pressed', 'true')
    expect(within(screen.getByTestId('filter-source')).getByRole('button', { name: t('watch.events.filter.source_obs') })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('watch-events-pane').scrollTop).toBe(180)
    // The observations read before are there at once, before any new answer.
    expect(screen.getAllByTestId('watch-event').map((n) => n.getAttribute('data-source'))).toEqual(['obs'])
  }, 30_000) // the first TabContent render pulls in its lazy modules, which is slow on a loaded host
})
