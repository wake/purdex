// Regression: what the reader typed in a worker pane's reply box (and where the
// transcript was scrolled) must survive switching to another tab and back. The
// alive pool keeps no execution tab by default (keepAliveCount 0, and an
// execution pane is not a "light" kind), so the pane really unmounts — this
// mounts the real TabContent, not ExecutionView alone.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { useState } from 'react'
import { TabContent } from '../TabContent'
import ExecutionView from './ExecutionView'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { createTab } from '../../types/tab'
import type { ExecutionViewMode, Tab } from '../../types/tab'
import { readScrollMemo, forgetScrollMemo } from '../../lib/nex/transcript-scroll-memory'
import { readWorkerDraft, forgetWorkerDraft } from '../../lib/nex/worker-draft-memory'
import * as api from '../../lib/nex/nex-api'
import * as lease from '../../hooks/useExecutionLease'
import * as sub from '../../hooks/useExecutionSubscription'

vi.mock('../../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), uploadWorkerFile: vi.fn(), fetchExecutionPrelude: vi.fn(), listExecutions: vi.fn(), fetchExecutionEvents: vi.fn() }))
vi.mock('../../hooks/useExecutionSubscription', () => ({ useExecutionSubscription: vi.fn(() => ({ problem: null, paused: false })) }))
vi.mock('../../hooks/useExecutionLease', () => ({ useExecutionLease: vi.fn() }))
vi.mock('../../lib/nex/client-id', () => ({ getNexClientId: () => 't-me000000' }))

const H = 'h', E = 'exc_1'
const summary = { id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/Users/w/repo', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 2, archived: false, effective_profile: 'standard', turn_count: 3 }

// The real pane wrapper owns the mode in the tab store; a local state stands in for it.
function ExecutionRenderer({ pane, isActive, isFocusTarget = false }: PaneRendererProps) {
  const [mode, setMode] = useState<ExecutionViewMode>('room')
  if (pane.content.kind !== 'execution') return null
  return <ExecutionView key={`${H}:${pane.content.executionId}`} hostId={H} executionId={pane.content.executionId} isActive={isActive}
    isFocusTarget={isFocusTarget} tabId="t-exec" paneId={pane.id} mode={mode} onModeChange={setMode} />
}
const Other = () => <div data-testid="other-tab" />

const execTab: Tab = { ...createTab({ kind: 'execution', executionId: E, host: H }), id: 't-exec' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const paneId = (tab: Tab) => (tab.layout as { pane: { id: string } }).pane.id

beforeEach(() => {
  cleanup()
  clearModuleRegistry()
  registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: ExecutionRenderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  useExecutionStore.setState({ executions: {} })
  useExecutionStore.getState().setSummary(H, E, summary as never)
  useExecutionStore.getState().setHistoryLoaded(H, E, true)
  vi.mocked(lease.useExecutionLease).mockReturnValue({ ensureLease: vi.fn().mockResolvedValue('ls_1'), release: vi.fn(), forget: vi.fn(), touch: vi.fn() })
  vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: null, paused: false })
  vi.mocked(api.sendMessage).mockReset().mockResolvedValue({ turn_id: 't1', delivery: 'delivered' })
})
afterEach(() => {
  forgetWorkerDraft(`${H}:${E}`)
  forgetScrollMemo(`${paneId(execTab)}:${H}:${E}`)
})

const all = [execTab, dashTab]
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement

describe('worker pane across tab switches', () => {
  it('keeps the typed reply when the reader switches to another tab and back', () => {
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'half a thought' } })

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    // The pane really is gone (not merely hidden): this is what the draft store is for.
    expect(screen.queryByTestId('execution-view')).toBeNull()
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()

    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(box().value).toBe('half a thought')
  })

  it('keeps it across a full unmount and remount', () => {
    const first = render(<TabContent activeTab={execTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'keep me' } })
    first.unmount()

    render(<TabContent activeTab={execTab} allTabs={all} />)
    expect(box().value).toBe('keep me')
  })

  it('a sent message is not resurrected by the next tab switch', async () => {
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'ship it' } })
    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(box().value).toBe('')
    expect(readWorkerDraft(`${H}:${E}`)).toBeUndefined()
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(box().value).toBe('')
  })

  it('forgets the draft once the execution has ended', () => {
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'too late' } })
    expect(readWorkerDraft(`${H}:${E}`)).toBe('too late')
    useExecutionStore.getState().setSummary(H, E, { ...summary, state: 'terminated', archived: true } as never)
    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(readWorkerDraft(`${H}:${E}`)).toBeUndefined()
  })

  it('one execution\'s draft does not show in another', () => {
    const first = render(<TabContent activeTab={execTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'for exc_1' } })
    first.unmount()
    const E2 = 'exc_2'
    useExecutionStore.getState().setSummary(H, E2, { ...summary, id: E2 } as never)
    useExecutionStore.getState().setHistoryLoaded(H, E2, true)
    const other: Tab = { ...createTab({ kind: 'execution', executionId: E2, host: H }), id: 't-exec2' }
    render(<TabContent activeTab={other} allTabs={[other]} />)
    expect(box().value).toBe('')
  })

  it('remembers where the transcript was scrolled across the switch', () => {
    const { container, rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    const scroller = container.querySelector('[data-testid="execution-view"] .overflow-y-auto') as HTMLElement
    Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 1000 })
    Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 200 })
    Object.defineProperty(scroller, 'scrollTop', { configurable: true, writable: true, value: 300 })
    fireEvent.scroll(scroller)
    const key = `${paneId(execTab)}:${H}:${E}`
    expect(readScrollMemo(key)).toMatchObject({ scrollTop: 300, atBottom: false })

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('execution-view')).toBeNull()
    expect(readScrollMemo(key)).toMatchObject({ scrollTop: 300, atBottom: false })

    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo as never
    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(scrollTo).toHaveBeenCalledWith({ top: 300, behavior: 'auto' })
  })
})
