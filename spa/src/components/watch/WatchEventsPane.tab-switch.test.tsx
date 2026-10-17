// Regression (CLAUDE.md tab-hosted checklist): the chip filters of the 事件 tab survive switching to another tab and back. The pane
// really unmounts (keepAliveCount 0; watch-events is not a "light" kind), so this mounts the real TabContent.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { WatchEventsPane } from './WatchEventsPane'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { useWatchEventsStore } from '../../lib/watch/events-store'
import { useWatchStatusStore } from '../../lib/watch/status-store'
import { clearViewMemory } from '../../lib/watch/view-memory'
import { useHostStore } from '../../stores/useHostStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'

const t = (k: string) => useI18nStore.getState().t(k)
const Other = () => <div data-testid="other-tab" />
const eventsTab: Tab = { ...createTab({ kind: 'watch-events' }), id: 'watch-events' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [eventsTab, dashTab]
const ev = (id: string, kind: string, status: 'pending' | 'handled') => ({ id, host: 'mlab', kind, at: new Date().toISOString(), status })

beforeEach(() => {
  cleanup(); clearModuleRegistry(); clearViewMemory()
  registerModule({ id: 'watch', name: 'Watch', panes: [{ kind: 'watch-events', component: WatchEventsPane }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: ['h1'], activeHostId: 'h1', runtime: { h1: { status: 'connected' } } } as never)
  useWatchStatusStore.getState().reset()
  useWatchEventsStore.setState({ byHost: { h1: { kind: 'ok', at: 1, events: [ev('a', 'pressure_high', 'pending'), ev('b', 'slot_long', 'handled')] } }, known: ['h1'] })
})
afterEach(() => clearViewMemory())

describe('the events tab across tab switches', () => {
  it('keeps the chip filters the reader picked when they switch to another tab and back', () => {
    const { rerender } = render(<TabContent activeTab={eventsTab} allTabs={all} />)
    expect(screen.getAllByTestId('watch-event')).toHaveLength(2)
    fireEvent.click(within(screen.getByTestId('filter-kind')).getByRole('button', { name: t('watch.events.kind.slot_long_short') }))
    fireEvent.click(within(screen.getByTestId('filter-status')).getByRole('button', { name: t('watch.events.filter.status_handled') }))
    expect(screen.getAllByTestId('watch-event')).toHaveLength(1)

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('watch-events-pane')).toBeNull() // really unmounted, not hidden
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()

    rerender(<TabContent activeTab={eventsTab} allTabs={all} />)
    expect(screen.getAllByTestId('watch-event').map((n) => n.getAttribute('data-event-id'))).toEqual(['b'])
    expect(within(screen.getByTestId('filter-kind')).getByRole('button', { name: t('watch.events.kind.slot_long_short') })).toHaveAttribute('aria-pressed', 'true')
    expect(within(screen.getByTestId('filter-status')).getByRole('button', { name: t('watch.events.filter.status_handled') })).toHaveAttribute('aria-pressed', 'true')
  }, 30_000) // the first TabContent render pulls in its lazy modules, which is slow on a loaded host
})
