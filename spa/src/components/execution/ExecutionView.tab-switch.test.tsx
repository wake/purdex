// Regression: what the reader typed in a worker pane's reply box (and where the
// transcript was scrolled) must survive switching to another tab and back. The
// alive pool keeps no execution tab by default (keepAliveCount 0, and an
// execution pane is not a "light" kind), so the pane really unmounts — this
// mounts the real TabContent, not ExecutionView alone.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { useState } from 'react'
import { TabContent } from '../TabContent'
import ExecutionView from './ExecutionView'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { createTab } from '../../types/tab'
import type { ExecutionViewMode, Tab } from '../../types/tab'
import { readScrollMemo, forgetScrollMemo } from '../../lib/nex/transcript-scroll-memory'
import { readWorkerDraft, forgetWorkerDraft } from '../../lib/nex/worker-draft-memory'
import { clearAllPermissionCards, isPermissionCardClosed, permissionCardCount, permissionCardKey, readPermissionCard, writePermissionCard } from '../../lib/nex/permission-card-memory'
import { NexApiError, type NexEvent } from '../../lib/nex/types'
import peerScoped from '../../lib/nex/__fixtures__/peer-mailbox/event-peer-message.scoped.json'
import * as api from '../../lib/nex/nex-api'
import * as lease from '../../hooks/useExecutionLease'
import * as sub from '../../hooks/useExecutionSubscription'

vi.mock('../../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), uploadWorkerFile: vi.fn(), fetchExecutionPrelude: vi.fn(), listExecutions: vi.fn(), fetchExecutionEvents: vi.fn(), answerPermission: vi.fn() }))
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
  clearAllPermissionCards()
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

// Permission channel P-2c: the request card's deny note, its open note field and its expanded input preview live
// outside the card, so the switch that unmounts the pane brings them back — for that request only.
describe('the permission request card across tab switches', () => {
  let seq = 0
  const apply = (executionId: string, kind: string, payload: Record<string, unknown>) => act(() => {
    seq += 1
    useExecutionStore.getState().applyEvents(H, executionId, [{ seq, execution_id: executionId, kind, payload, created_at: Date.now() }])
  })
  const LONG = `python3 -c "print(1)" ${'x'.repeat(500)} THE-TAIL`
  const ask = (requestId: string, executionId = E) =>
    apply(executionId, 'permission.requested', { request_id: requestId, turn_id: 'trn_1', tool_name: 'Bash', input: { command: LONG } })
  const note = () => screen.getByTestId('permission-deny-note') as HTMLInputElement
  /** Open the note, type it, and expand the preview, on the card on screen. */
  const work = (text: string) => {
    fireEvent.click(screen.getByTestId('permission-expand'))
    fireEvent.click(screen.getByTestId('permission-deny'))
    fireEvent.change(note(), { target: { value: text } })
  }
  const expectFresh = () => {
    expect(screen.getByTestId('permission-card')).toBeInTheDocument()
    expect(screen.queryByTestId('permission-deny-note')).toBeNull()
    expect(screen.getByTestId('permission-deny')).toHaveTextContent(/^Deny$/)
    expect(screen.getByTestId('permission-input')).not.toHaveTextContent('THE-TAIL')
    expect(screen.getByTestId('permission-expand')).toHaveTextContent('Show all')
  }

  beforeEach(() => {
    seq = 0
    useI18nStore.getState().setLocale('en')
    useExecutionStore.getState().setSummary(H, E, { ...summary, state: 'running', effective_profile: 'handoff_ask' } as never)
  })

  it('keeps the typed note, the open note field and the expanded input when the reader switches away and back', () => {
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    ask('req_a')
    work('不要動 prod 設定')
    expect(screen.getByTestId('permission-input')).toHaveTextContent('THE-TAIL')

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('execution-view')).toBeNull()

    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(note().value).toBe('不要動 prod 設定')
    expect(screen.getByTestId('permission-deny')).toHaveTextContent('Send deny')
    expect(screen.getByTestId('permission-input')).toHaveTextContent('THE-TAIL')
    expect(screen.getByTestId('permission-expand')).toHaveTextContent('Show less')
  })

  it('keeps an open but empty note field, and a collapsed preview stays collapsed', () => {
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    ask('req_a')
    fireEvent.click(screen.getByTestId('permission-deny'))
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(note().value).toBe('')
    expect(screen.getByTestId('permission-expand')).toHaveTextContent('Show all')
  })

  it('the restored note stays within 2048 UTF-8 bytes', () => {
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    ask('req_a')
    work('中'.repeat(1000))
    expect(note().value).toBe('中'.repeat(682))
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(note().value).toBe('中'.repeat(682))
    expect(new TextEncoder().encode(note().value).length).toBeLessThanOrEqual(2048)
  })

  it('a different request does not inherit them — the next one of this worker, or one of another worker', () => {
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    ask('req_a')
    ask('req_b')
    work('for req_a only')
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    // While the pane is away, req_a is answered elsewhere: req_b is next.
    apply(E, 'permission.resolved', { request_id: 'req_a', turn_id: 'trn_1', outcome: 'allowed' })
    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(screen.getByTestId('permission-input')).toHaveTextContent('print(1)')
    expectFresh()
    work('for exc_1 req_b only')

    // Another worker whose request happens to carry the same id.
    const E2 = 'exc_2'
    useExecutionStore.getState().setSummary(H, E2, { ...summary, id: E2, state: 'running', effective_profile: 'handoff_ask' } as never)
    useExecutionStore.getState().setHistoryLoaded(H, E2, true)
    const other: Tab = { ...createTab({ kind: 'execution', executionId: E2, host: H }), id: 't-exec2' }
    rerender(<TabContent activeTab={other} allTabs={[...all, other]} />)
    ask('req_b', E2)
    expectFresh()
  })

  // The memory cannot grow without bound: a request that stopped waiting drops its entry.
  it.each([
    ['allowed elsewhere', { outcome: 'allowed' }],
    ['denied elsewhere', { outcome: 'denied' }],
    ['cancelled', { outcome: 'cancelled', reason: 'interrupt' }],
    ['expired', { outcome: 'expired', timeout_s: 300 }],
  ])('a request %s drops its entry; a still-waiting one keeps its own', (_label, resolution) => {
    render(<TabContent activeTab={execTab} allTabs={all} />)
    ask('req_a')
    ask('req_b')
    work('for req_a')
    writePermissionCard(permissionCardKey(H, E, 'req_b'), { note: 'for req_b' })
    apply(E, 'permission.resolved', { request_id: 'req_a', turn_id: 'trn_1', ...resolution })
    expect(readPermissionCard(permissionCardKey(H, E, 'req_a')).note).toBe('')
    expect(readPermissionCard(permissionCardKey(H, E, 'req_b')).note).toBe('for req_b')
  })

  it('a deny this pane sent drops the entry at once, before the resolution arrives', async () => {
    vi.mocked(api.answerPermission).mockReset().mockResolvedValue({ request_id: 'req_a', outcome: 'denied' })
    render(<TabContent activeTab={execTab} allTabs={all} />)
    ask('req_a')
    work('no prod')
    expect(permissionCardCount()).toBe(1)
    await act(async () => { fireEvent.click(screen.getByTestId('permission-deny')) })
    expect(api.answerPermission).toHaveBeenCalledTimes(1)
    expect(permissionCardCount()).toBe(0)
  })

  // The exit result patches the summary before any event settles the request (it still reads pending here).
  it('an ended worker drops every entry of it, and only of it', () => {
    writePermissionCard(permissionCardKey(H, 'exc_other', 'req_a'), { note: 'another worker' })
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    ask('req_a')
    work('for req_a')
    act(() => { useExecutionStore.getState().applySummaryPatch(H, E, { state: 'terminated', archived: true }) })
    expect(useExecutionStore.getState().executions[`${H}:${E}`].permissions.req_a.status).toBe('pending')
    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(readPermissionCard(permissionCardKey(H, E, 'req_a')).note).toBe('')
    expect(permissionCardCount()).toBe(1)
    expect(readPermissionCard(permissionCardKey(H, 'exc_other', 'req_a')).note).toBe('another worker')
  })

  it('nothing is dropped before the history is in: a remount that has not replayed the request yet keeps it', () => {
    writePermissionCard(permissionCardKey(H, E, 'req_a'), { note: 'typed before the reload of history', noteOpen: true })
    useExecutionStore.getState().setHistoryLoaded(H, E, false)
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    expect(readPermissionCard(permissionCardKey(H, E, 'req_a')).note).toBe('typed before the reload of history')
    ask('req_a')
    act(() => { useExecutionStore.getState().setHistoryLoaded(H, E, true) })
    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(note().value).toBe('typed before the reload of history')
  })

  // A2 / spec §5.3: after an answer the card goes away — and stays away across the switch that remounts the pane,
  // in the window where the answer succeeded but its `permission.resolved` has not arrived yet (the store still
  // reads pending), until the store settles the request.
  describe('a request this pane answered', () => {
    const KEY_A = permissionCardKey(H, E, 'req_a')
    const statusOf = (id: string, executionId = E) => useExecutionStore.getState().executions[`${H}:${executionId}`].permissions[id]?.status
    const allow = async () => { await act(async () => { fireEvent.click(screen.getByTestId('permission-allow')) }) }
    type Rerender = ReturnType<typeof render>['rerender']
    const away = (rerender: Rerender) => {
      rerender(<TabContent activeTab={dashTab} allTabs={all} />)
      expect(screen.queryByTestId('execution-view')).toBeNull()
    }
    const back = (rerender: Rerender) => {
      rerender(<TabContent activeTab={execTab} allTabs={all} />)
      expect(screen.getByTestId('execution-view')).toBeInTheDocument()
    }
    const answeredIds = () => vi.mocked(api.answerPermission).mock.calls.map((c) => c[2])

    beforeEach(() => {
      vi.mocked(api.answerPermission).mockReset().mockResolvedValue({ request_id: 'req_a', outcome: 'allowed' })
    })

    it('stays closed across a tab switch before its resolution arrives, and is not answered again', async () => {
      const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
      ask('req_a')
      work('typed, then allowed instead')
      await allow()
      expect(answeredIds()).toEqual(['req_a'])
      expect(screen.queryByTestId('permission-card')).toBeNull()
      expect(statusOf('req_a')).toBe('pending')

      away(rerender)
      back(rerender)
      expect(screen.queryByTestId('permission-card')).toBeNull()
      expect(statusOf('req_a')).toBe('pending')
      // A second round trip changes nothing, and nothing was sent again.
      away(rerender)
      back(rerender)
      expect(screen.queryByTestId('permission-card')).toBeNull()
      expect(answeredIds()).toEqual(['req_a'])
      expect(isPermissionCardClosed(KEY_A)).toBe(true)
      // The draft went with the answer.
      expect(readPermissionCard(KEY_A).note).toBe('')
    })

    it('a 409 permission_not_pending close holds across the switch too', async () => {
      vi.mocked(api.answerPermission).mockReset().mockRejectedValue(new NexApiError(409, 'permission_not_pending', 'gone'))
      const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
      ask('req_a')
      await allow()
      expect(screen.queryByTestId('permission-card')).toBeNull()
      away(rerender)
      back(rerender)
      expect(screen.queryByTestId('permission-card')).toBeNull()
      expect(answeredIds()).toEqual(['req_a'])
    })

    it.each([
      ['resolved while the pane shows', 'shown'],
      ['resolved while the pane is away', 'away'],
    ] as const)('the closed mark is dropped once the request is %s', async (_label, when) => {
      const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
      ask('req_a')
      await allow()
      away(rerender)
      if (when === 'shown') back(rerender)
      expect(isPermissionCardClosed(KEY_A)).toBe(true)
      apply(E, 'permission.resolved', { request_id: 'req_a', turn_id: 'trn_1', outcome: 'allowed' })
      if (when === 'away') back(rerender)
      expect(isPermissionCardClosed(KEY_A)).toBe(false)
      expect(screen.queryByTestId('permission-card')).toBeNull()
    })

    it('the closed mark is dropped once the worker has ended', async () => {
      const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
      ask('req_a')
      await allow()
      away(rerender)
      back(rerender)
      expect(isPermissionCardClosed(KEY_A)).toBe(true)
      act(() => { useExecutionStore.getState().applySummaryPatch(H, E, { state: 'terminated', archived: true }) })
      rerender(<TabContent activeTab={execTab} allTabs={all} />)
      expect(isPermissionCardClosed(KEY_A)).toBe(false)
    })

    it('another request is unaffected: the next one of this worker, and the same id of another worker', async () => {
      const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
      ask('req_a')
      ask('req_b')
      await allow()
      expect(answeredIds()).toEqual(['req_a'])
      // req_b is now the card on screen — before and after the switch — and it answers as itself.
      expect(screen.getByTestId('permission-card')).toBeInTheDocument()
      away(rerender)
      back(rerender)
      expect(screen.getByTestId('permission-card')).toBeInTheDocument()
      vi.mocked(api.answerPermission).mockResolvedValueOnce({ request_id: 'req_b', outcome: 'allowed' })
      await allow()
      expect(answeredIds()).toEqual(['req_a', 'req_b'])

      // Another worker whose request carries the same id as the one this pane closed.
      const E2 = 'exc_2'
      useExecutionStore.getState().setSummary(H, E2, { ...summary, id: E2, state: 'running', effective_profile: 'handoff_ask' } as never)
      useExecutionStore.getState().setHistoryLoaded(H, E2, true)
      const other: Tab = { ...createTab({ kind: 'execution', executionId: E2, host: H }), id: 't-exec2' }
      rerender(<TabContent activeTab={other} allTabs={[...all, other]} />)
      ask('req_a', E2)
      expect(screen.getByTestId('permission-card')).toBeInTheDocument()
      expect(isPermissionCardClosed(permissionCardKey(H, E2, 'req_a'))).toBe(false)
    })

    it('the closed mark is kept while a remount has not replayed the history yet', async () => {
      const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
      ask('req_a')
      await allow()
      away(rerender)
      // The pane comes back to a store that reloads the history: the request is not in the table yet.
      act(() => {
        useExecutionStore.getState().clearExecution(H, E)
        useExecutionStore.getState().setSummary(H, E, { ...summary, state: 'running', effective_profile: 'handoff_ask' } as never)
      })
      back(rerender)
      expect(screen.getByTestId('execution-loading')).toBeInTheDocument()
      expect(isPermissionCardClosed(KEY_A)).toBe(true)
      ask('req_a')
      act(() => { useExecutionStore.getState().setHistoryLoaded(H, E, true) })
      expect(statusOf('req_a')).toBe('pending')
      expect(screen.queryByTestId('permission-card')).toBeNull()
      away(rerender)
      back(rerender)
      expect(screen.queryByTestId('permission-card')).toBeNull()
      expect(answeredIds()).toEqual(['req_a'])
    })
  })
})

// Peer mailbox spec §7 / §10: a peer turn lives in the execution store, not in the pane, so the switch that unmounts
// the pane brings it back as the peer block — and it never touches what the reader was typing.
describe('a peer turn across tab switches', () => {
  it('keeps the peer block and the reader\'s draft when the reader switches away and back', () => {
    useI18nStore.getState().setLocale('en')
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'half a reply' } })
    act(() => { useExecutionStore.getState().applyEvents(H, E, [structuredClone(peerScoped) as NexEvent]) })
    expect(screen.getByTestId('peer-message')).toHaveTextContent(peerScoped.payload.text)
    expect(box().value).toBe('half a reply')

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('execution-view')).toBeNull()

    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    const block = screen.getByTestId('peer-message')
    expect(block).toHaveTextContent(`From ${peerScoped.payload.from_name}`)
    expect(block).toHaveTextContent(peerScoped.payload.text)
    expect(screen.queryByTestId('room-user-line')).toBeNull()
    expect(box().value).toBe('half a reply')
  })
})
