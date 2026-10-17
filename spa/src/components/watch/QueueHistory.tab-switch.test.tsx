// Regression (CLAUDE.md tab-hosted checklist): the 當下｜歷史 choice and the 重｜輕 class of the 歷史 body survive switching to another tab and
// back. The pane really unmounts (keepAliveCount 0; watch-host is not a "light" kind), so this mounts the real TabContent.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within, waitFor } from '@testing-library/react'

const fetchGateDecisions = vi.fn()
vi.mock('../../lib/watch/api', async (orig) => ({ ...(await orig<typeof import('../../lib/watch/api')>()), fetchGateDecisions: (...a: unknown[]) => fetchGateDecisions(...a) }))
import { TabContent } from '../TabContent'
import { WatchHostPane } from './WatchHostPane'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { useFleetStore } from '../../lib/fleet/store'
import { clearViewMemory, readHistoryClass, readQueueTab } from '../../lib/watch/view-memory'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
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
const q = { heavy_used: 0, heavy_total: 2, light_used: 0, light_total: 3, holders: [], waiters: [], longest_wait_s: 0, admission_held: false, admission_reason: '', heavy_paused: false, light_paused: false }

beforeEach(() => {
  cleanup(); clearModuleRegistry(); clearViewMemory(); fetchGateDecisions.mockReset(); fetchGateDecisions.mockResolvedValue({ kind: 'ok', records: [], skipped: 0 })
  registerModule({ id: 'watch', name: 'Watch', panes: [{ kind: 'watch-host', component: WatchHostPane }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } }, hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: { 'cfg-a': { status: 'connected' } } } as never)
  useNexHostStore.setState({ byHost: { 'cfg-a': { daemonCapabilities: ['gate.decisions.v1'] } } } as never)
  const s = useFleetStore.getState()
  s.reset()
  const gen = s.begin('cfg-a')
  s.setSupport('cfg-a', gen, 'yes')
  s.applyFetched('cfg-a', gen, primaryAnswer(1, [host('d-a', { alias: 'air26', slow: { at: 1, top_cpu: [], top_mem: [], queue: q } })]))
})
afterEach(() => clearViewMemory())

describe('the 歷史 body across tab switches', () => {
  it('keeps 歷史 and the picked 輕指令 (view memory, not component state) across the switch, and reads that class on return', async () => {
    const { rerender } = render(<TabContent activeTab={hostTab} allTabs={all} />)
    fireEvent.click(within(screen.getByTestId('queue-tabs')).getByRole('button', { name: t('watch.queue.tab.history') }))
    fireEvent.click(await screen.findByRole('button', { name: t('watch.history.kind.light') }))
    expect(readQueueTab('d-a')).toBe('history')
    expect(readHistoryClass('d-a')).toBe('light')

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('queue-history')).toBeNull() // really unmounted, not hidden
    fetchGateDecisions.mockClear()

    rerender(<TabContent activeTab={hostTab} allTabs={all} />)
    expect(await screen.findByTestId('queue-history')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: t('watch.history.kind.light') })).toHaveAttribute('aria-pressed', 'true')
    await waitFor(() => expect(fetchGateDecisions).toHaveBeenCalledWith('cfg-a', 'light', expect.any(String)))
  }, 30_000)
})
