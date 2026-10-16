// Regression (CLAUDE.md tab-hosted checklist): the range picked on a host tab survives switching to another tab and back. The pane really
// unmounts (keepAliveCount 0; watch-host is not a "light" kind), so this mounts the real TabContent, not WatchHostPane alone.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { WatchHostPane } from './WatchHostPane'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { useFleetStore } from '../../lib/fleet/store'
import { clearViewMemory } from '../../lib/watch/view-memory'
import { useHostStore } from '../../stores/useHostStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { host, primaryAnswer } from '../../lib/fleet/__fixtures__/builders'
import { useI18nStore } from '../../stores/useI18nStore'
const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const Other = () => <div data-testid="other-tab" />
const hostTab: Tab = { ...createTab({ kind: 'watch-host', fleetHostId: 'd-a' }), id: 'watch-host-d-a' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [hostTab, dashTab]

beforeEach(() => {
  cleanup(); clearModuleRegistry(); clearViewMemory()
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
afterEach(() => clearViewMemory())

describe('the host tab across tab switches', () => {
  it('keeps the range the reader picked when they switch to another tab and back', () => {
    const { rerender } = render(<TabContent activeTab={hostTab} allTabs={all} />)
    fireEvent.click(within(screen.getByTestId('range-switch')).getByRole('button', { name: t('watch.host.range.1h') }))
    expect(screen.getByRole('button', { name: t('watch.host.resources', { range: t('watch.host.range.1h') }) })).toBeInTheDocument()

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('range-switch')).toBeNull() // really unmounted, not hidden
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()

    rerender(<TabContent activeTab={hostTab} allTabs={all} />)
    expect(screen.getByRole('button', { name: t('watch.host.resources', { range: t('watch.host.range.1h') }) })).toBeInTheDocument()
    expect(screen.getByTestId('axis')).toHaveTextContent(t('watch.host.axis_hour', { n: 1 }))
  }, 30_000) // the first TabContent render pulls in its lazy modules, which is slow on a loaded host
})
