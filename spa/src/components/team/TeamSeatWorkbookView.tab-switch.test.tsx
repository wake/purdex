// Regression: the workbook view's own state (紀錄 / 待辦, the scroll of each, the finished groups the reader opened) must survive
// switching to another tab and back. The alive pool keeps no tab by default (keepAliveCount 0), so the view really unmounts: this
// mounts the real TabContent, not the view alone (pattern: components/execution/ExecutionView.tab-switch.test.tsx).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { TeamSeatWorkbookView } from './TeamSeatWorkbookView'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { entry, seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import { emptyTodos } from '../../lib/workbook/merge'
import { clearViewMemos } from '../../lib/workbook/view-memory'

const fetchConversation = vi.hoisted(() => vi.fn())
vi.mock('../../lib/workbook/api', async (orig) => ({ ...(await orig<typeof import('../../lib/workbook/api')>()), fetchConversation }))

const Workbook = () => <TeamSeatWorkbookView hostId="h1" sessionId="S" title="T" />
const Other = () => <div data-testid="other-tab" />
// A heavy pane kind (not on lib/pane-weight.ts's light list), so switching away really unmounts it.
const wbTab: Tab = { ...createTab({ kind: 'execution', executionId: 'exc_1', host: 'h1' }), id: 't-wb' }
const otherTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-other' }
const all = [wbTab, otherTab]

beforeEach(() => {
  cleanup()
  clearModuleRegistry()
  registerModule({ id: 'wb', name: 'wb', panes: [{ kind: 'execution', component: Workbook }] })
  registerModule({ id: 'other', name: 'other', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: ['h1'] })
  useWorkbookStore.getState().reset()
  clearViewMemos()
  fetchConversation.mockReset()
  fetchConversation.mockRejectedValue(new Error('offline'))
  useI18nStore.getState().setLocale('zh-TW')
  seedWorkbook('h1', 'S', {
    status: 's',
    entries: [entry(3, { thing: 'A', thingDone: true, entry: 'a-entry' }), entry(2, { thing: 'B' })],
    todos: { ...emptyTodos(), open: [{ id: 1, title: 'todo 1', detail: '', state: 'open', closedBy: '', createdAt: 1, closedAt: 0, addedEntryId: 1, closedEntryId: 0 }] },
  }, { v2: true })
})
afterEach(() => cleanup())

const body = () => screen.getByTestId('workbook-body')
const awayAndBack = (r: ReturnType<typeof render>) => {
  r.rerender(<TabContent activeTab={otherTab} allTabs={all} />)
  expect(screen.queryByTestId('team-seat-workbook')).toBeNull() // really gone, not merely hidden
  r.rerender(<TabContent activeTab={wbTab} allTabs={all} />)
}

describe('the workbook view across tab switches', () => {
  it('keeps 待辦 selected', async () => {
    const r = render(<TabContent activeTab={wbTab} allTabs={all} />)
    await act(async () => { await Promise.resolve() })
    fireEvent.click(screen.getByRole('button', { name: /^待辦/ }))
    expect(screen.getByTestId('workbook-todos')).toBeTruthy()
    awayAndBack(r)
    expect(screen.getByTestId('workbook-todos')).toBeTruthy()
    expect(screen.queryByTestId('workbook-log')).toBeNull()
  })
  it('keeps the scroll of each tab', async () => {
    const r = render(<TabContent activeTab={wbTab} allTabs={all} />)
    await act(async () => { await Promise.resolve() })
    body().scrollTop = 120
    fireEvent.scroll(body())
    fireEvent.click(screen.getByRole('button', { name: /^待辦/ }))
    body().scrollTop = 40
    fireEvent.scroll(body())
    awayAndBack(r)
    expect(body().scrollTop).toBe(40) // back on 待辦
    fireEvent.click(screen.getByRole('button', { name: '紀錄' }))
    expect(body().scrollTop).toBe(120)
  })
  it('keeps a finished group the reader opened', async () => {
    const r = render(<TabContent activeTab={wbTab} allTabs={all} />)
    await act(async () => { await Promise.resolve() })
    expect(screen.queryByText('a-entry')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: /^A/ }))
    expect(screen.getByText('a-entry')).toBeTruthy()
    awayAndBack(r)
    expect(screen.getByText('a-entry')).toBeTruthy()
  })
  it('another conversation does not inherit it', async () => {
    const r = render(<TabContent activeTab={wbTab} allTabs={all} />)
    await act(async () => { await Promise.resolve() })
    fireEvent.click(screen.getByRole('button', { name: /^待辦/ }))
    r.unmount()
    seedWorkbook('h1', 'S', { status: 'x' }, { v2: true })
    const Wb2 = () => <TeamSeatWorkbookView hostId="h1" sessionId="S2" title="T" />
    seedWorkbook('h1', 'S2', { status: 'y' }, { v2: true })
    clearModuleRegistry()
    registerModule({ id: 'wb', name: 'wb', panes: [{ kind: 'execution', component: Wb2 }] })
    render(<TabContent activeTab={wbTab} allTabs={all} />)
    expect(screen.getByTestId('workbook-log')).toBeTruthy()
  })
})
