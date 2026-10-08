// spa/src/components/executions/ExecutionsView.test.tsx
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act, within, waitFor } from '@testing-library/react'
import { ExecutionsView } from './ExecutionsView'
import NexExecutionsTable from '../hosts/nex/NexExecutionsTable'
import { resetExecutionListForTests, useExecutionListStore } from '../../stores/useExecutionListStore'
import { useNexHostStore, type NexHostEntry } from '../../stores/useNexHostStore'
import { useHostStore } from '../../stores/useHostStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { openWorkerTab } from '../../features/workspace/lib/open-worker-tab'
import { subscriptionSlots } from '../../lib/nex/subscription-slots'
import { STATE_DOT_CLASSES } from '../../lib/nex/state-dot'
import type { ExecutionSummary } from '../../lib/nex/types'
import { exitWorker } from '../../lib/nex/exit-worker'
import { useUndoToast } from '../../stores/useUndoToast'
import { HandoffApiError } from '../../lib/nex/handoff-api'
import * as api from '../../lib/nex/nex-api'
import * as sse from '../../lib/nex/nex-sse'

vi.mock('../../lib/nex/nex-api', () => ({ listExecutions: vi.fn(), attachControl: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), archiveExecution: vi.fn() }))
vi.mock('../../lib/nex/exit-worker', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/nex/exit-worker')>()),
  exitWorker: vi.fn(),
}))
vi.mock('../../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))
vi.mock('../../lib/deeplink/deeplinkResolver', () => ({ openExecutionDetailTab: vi.fn() }))
vi.mock('../../features/workspace/lib/open-worker-tab', () => ({ openWorkerTab: vi.fn() }))

const H = 'host-a'
const OTHER = 'host-b'
const NOW = 1_700_000_000_000
const MIN = 60_000
const HOUR = 60 * MIN
const DAY = 24 * HOUR

const row = (over: Partial<ExecutionSummary> & { id: string }): ExecutionSummary =>
  ({ state: 'running', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'brief', labels: {}, created_at: 0, updated_at: NOW, duration_ms: null, event_count: 0, observers: 0, archived: false, ...over }) as ExecutionSummary

const readyEntry: NexHostEntry = {
  info: { configured: true, mounted: true, ready: true, init_error: '', effective: null },
  capabilities: null, phase: 'ready', error: null, fetchedAt: 1, generation: 1, fingerprint: '1:1:t',
}
const entryWith = (patch: Partial<NexHostEntry>): NexHostEntry => ({ ...readyEntry, ...patch })

function seedList(items: ExecutionSummary[], patch: Partial<{ phase: 'idle' | 'loading' | 'ready' | 'error'; error: string | null; truncated: boolean }> = {}) {
  useExecutionListStore.setState({ byHost: { [H]: { items, phase: 'ready', error: null, lastSeq: null, refreshRevision: 0, truncated: false, complete: true, ...patch } } })
}

let ensure: ReturnType<typeof vi.fn<(hostId: string) => Promise<void>>>

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.setSystemTime(NOW)
  subscriptionSlots.resetForTests()
  resetExecutionListForTests()
  useExecutionListStore.setState({ byHost: {} })
  ensure = vi.fn<(hostId: string) => Promise<void>>().mockResolvedValue(undefined)
  useNexHostStore.setState({ byHost: { [H]: readyEntry }, ensure })
  useHostStore.setState({
    hosts: {
      [H]: { id: H, name: 'Mini Lab', ip: '1', port: 1, token: 't', order: 0 },
      [OTHER]: { id: OTHER, name: 'Air', ip: '2', port: 2, token: 't', order: 1 },
    },
    hostOrder: [H, OTHER], activeHostId: H, runtime: {},
  })
  useShownHostsStore.setState({ ids: [H, OTHER] }) // shown in this workbench unless a test hides one (H2d-2)
  vi.mocked(exitWorker).mockReset().mockResolvedValue({ exited: true, terminated: true, archived: true, state: 'terminated' })
  useUndoToast.setState({ toast: null, notice: null })
  vi.mocked(openWorkerTab).mockReset().mockReturnValue('tab-1')
  vi.mocked(sse.openNexSse).mockReset().mockImplementation(() => ({ close: vi.fn() }))
  vi.mocked(api.listExecutions).mockReset().mockResolvedValue({ items: [], next_cursor: '' })
})
afterEach(() => vi.useRealTimers())

describe('ExecutionsView', () => {
  it('a truncated list shows the persistent notice; an untruncated one does not', () => {
    seedList([row({ id: 'exc_1' })], { truncated: true })
    const { unmount } = render(<ExecutionsView hostId={H} isActive />)
    expect(screen.getByTestId('executions-truncated')).toHaveTextContent('More than 10,000 unarchived executions; the newest may not be listed')
    unmount()
    seedList([row({ id: 'exc_1' })])
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.queryByTestId('executions-truncated')).toBeNull()
  })

  it('the truncated notice is zh-TW verbatim and coexists with the empty note', () => {
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
    try {
      seedList([], { truncated: true })
      render(<ExecutionsView hostId={H} isActive />)
      expect(screen.getByTestId('executions-truncated')).toHaveTextContent('未歸檔的執行紀錄超過 10,000 筆，最新的可能沒有列出')
      expect(screen.getByTestId('executions-empty')).toBeInTheDocument()
    } finally {
      act(() => { useI18nStore.getState().setLocale('en') })
    }
  })

  it('no truncated notice while disabled or on first load', () => {
    seedList([], { truncated: true, phase: 'loading' })
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.queryByTestId('executions-truncated')).toBeNull()
  })

  it('header shows host name + phase dot', () => {
    seedList([])
    render(<ExecutionsView hostId={H} isActive />)
    const header = screen.getByTestId('executions-header')
    expect(within(header).getByText('Mini Lab')).toBeInTheDocument()
    expect(within(header).getByTestId('executions-phase-dot')).toHaveAttribute('data-phase', 'ready')
  })

  it('ensures the nex host on mount', () => {
    seedList([])
    render(<ExecutionsView hostId={H} isActive />)
    expect(ensure).toHaveBeenCalledWith(H)
  })

  it('only includeArchived: false, limit: 500 is requested', async () => {
    render(<ExecutionsView hostId={H} isActive />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    expect(api.listExecutions).toHaveBeenCalledWith(H, { includeArchived: false, limit: 500 })
  })

  it('grouping order and labels (local / purdex i18n, unknown raw), newest first within and across groups', () => {
    seedList([
      row({ id: 'exc_old_local', brief: 'old local', updated_at: NOW - 3 * HOUR }),
      row({ id: 'exc_aigora', brief: 'aigora one', labels: { source: 'aigora' }, updated_at: NOW - 2 * HOUR }),
      row({ id: 'exc_purdex', brief: 'purdex one', labels: { source: 'purdex' }, updated_at: NOW - 1 * HOUR }),
      row({ id: 'exc_new_local', brief: 'new local', updated_at: NOW - 5 * MIN }),
    ])
    render(<ExecutionsView hostId={H} isActive />)
    const view = screen.getByTestId('executions-view')
    const groups = view.querySelectorAll('[data-testid^="executions-group-"]')
    expect(Array.from(groups).map((g) => g.getAttribute('data-testid'))).toEqual([
      'executions-group-local', 'executions-group-purdex', 'executions-group-aigora',
    ])
    expect(within(screen.getByTestId('executions-group-local')).getByText('Local')).toBeInTheDocument()
    expect(within(screen.getByTestId('executions-group-purdex')).getByText('Purdex')).toBeInTheDocument()
    expect(within(screen.getByTestId('executions-group-aigora')).getByText('aigora')).toBeInTheDocument()
    const localRows = within(screen.getByTestId('executions-group-local')).getAllByTestId('executions-row')
    expect(localRows.map((r) => r.textContent)).toEqual([
      expect.stringContaining('new local'), expect.stringContaining('old local'),
    ])
  })

  it('state dot class per state', () => {
    seedList([
      row({ id: 'exc_run', state: 'running', updated_at: NOW - 1 }),
      row({ id: 'exc_idle', state: 'idle', updated_at: NOW - 2 }),
      row({ id: 'exc_fail', state: 'failed', updated_at: NOW - 3 }),
      row({ id: 'exc_weird', state: 'something-new', updated_at: NOW - 4 }),
    ])
    render(<ExecutionsView hostId={H} isActive />)
    const dots = screen.getAllByTestId('executions-state-dot')
    expect(dots[0]).toHaveClass(STATE_DOT_CLASSES.running)
    expect(dots[1]).toHaveClass(STATE_DOT_CLASSES.idle)
    expect(dots[2]).toHaveClass(STATE_DOT_CLASSES.failed)
    expect(dots[3]).toHaveClass('bg-text-muted')
    expect(dots[0]).toHaveAttribute('title', 'running')
  })

  it('brief single line with ellipsis', () => {
    seedList([row({ id: 'exc_multi', brief: `${'a'.repeat(100)}\nsecond line` })])
    render(<ExecutionsView hostId={H} isActive />)
    const brief = screen.getByTestId('executions-brief')
    expect(brief.textContent).toBe(`${'a'.repeat(79)}…`)
    expect(brief.textContent).not.toContain('second line')
    expect(brief).toHaveClass('truncate')
  })

  // #1771 (activity-bar Workers): the host id reaches the row through ExecutionsGroup, so the capability gate is this host's.
  it('a handoff row (empty brief) is named by its conversation title with the capability, else by its cwd basename', () => {
    const handoff = row({ id: 'exc_h', brief: '', cwd: '/w/repo', session_title: { text: 'Zebrafinch', source: 'ai' } })
    useNexHostStore.setState({ byHost: { [H]: entryWith({ capabilities: { session_title: { sources: ['ai'], max_bytes: 200 } } as never }) } })
    seedList([handoff])
    const { unmount } = render(<ExecutionsView hostId={H} isActive />)
    expect(screen.getByTestId('executions-brief').textContent).toBe('Zebrafinch')
    unmount()
    useNexHostStore.setState({ byHost: { [H]: readyEntry } })
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.getByTestId('executions-brief').textContent).toBe('repo')
  })

  it('a hidden host\'s plain handoff row is named too (the non-openable branch gets the host id)', () => {
    useShownHostsStore.setState({ ids: [OTHER] })
    useNexHostStore.setState({ byHost: { [H]: entryWith({ capabilities: { session_title: { sources: ['ai'], max_bytes: 200 } } as never }) } })
    seedList([row({ id: 'exc_h', brief: '', cwd: '/w/repo', session_title: { text: 'Zebrafinch', source: 'ai' } })])
    render(<ExecutionsView hostId={H} isActive />)
    const plain = screen.getByTestId('executions-row')
    expect(plain.tagName).not.toBe('BUTTON')
    expect(plain.getAttribute('aria-label') ?? '').toMatch(/^Zebrafinch · /)
  })

  it('relative age boundaries', () => {
    seedList([
      row({ id: 'exc_now', updated_at: NOW - 59_000 }),
      row({ id: 'exc_min', updated_at: NOW - 60_000 }),
      row({ id: 'exc_hr', updated_at: NOW - 60 * MIN }),
      row({ id: 'exc_day', updated_at: NOW - 24 * HOUR }),
      row({ id: 'exc_days', updated_at: NOW - 3 * DAY }),
    ])
    render(<ExecutionsView hostId={H} isActive />)
    const ages = screen.getAllByTestId('executions-age').map((el) => el.textContent)
    expect(ages).toEqual(['just now', '1m', '1h', '1d', '3d'])
  })

  it('ages advance with the 60 s ticker', () => {
    seedList([row({ id: 'exc_tick', updated_at: NOW - 30_000 })])
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.getByTestId('executions-age').textContent).toBe('just now')
    act(() => { vi.advanceTimersByTime(60_000) })
    expect(screen.getByTestId('executions-age').textContent).toBe('1m')
  })

  it('marker only for /session/ origins stamped with the DAEMON host id from capabilities (the client entry id never matches)', () => {
    // The daemon writes origins with its own id (`purdex://host/<daemon host_id>/session/<code>`);
    // the SPA's host entry id (`H`) is a client-local name and must not be what the marker compares.
    const DAEMON = 'mini-lab:278cbm'
    useNexHostStore.setState({ byHost: { [H]: entryWith({ capabilities: { host_id: DAEMON } as NexHostEntry['capabilities'] }) } })
    seedList([
      row({ id: 'exc_mine', origin: `purdex://host/${DAEMON}/session/zk16vd`, updated_at: NOW - 1 }),
      row({ id: 'exc_client_id', origin: `purdex://host/${H}/session/zk16vd`, updated_at: NOW - 2 }),
      row({ id: 'exc_nosess', origin: `purdex://host/${DAEMON}/somethingelse`, updated_at: NOW - 3 }),
      row({ id: 'exc_none', updated_at: NOW - 4 }),
    ])
    render(<ExecutionsView hostId={H} isActive />)
    const rows = screen.getAllByTestId('executions-row')
    const marker = within(rows[0]).getByTestId('executions-marker')
    expect(marker).toHaveTextContent('↩')
    expect(marker).toHaveAttribute('title', 'from tmux session zk16vd')
    expect(within(rows[1]).queryByTestId('executions-marker')).toBeNull()
    expect(within(rows[2]).queryByTestId('executions-marker')).toBeNull()
    expect(within(rows[3]).queryByTestId('executions-marker')).toBeNull()
  })

  it('R4 T4.1: the rollup cost shows only when the host\'s worker_rollup.cost_basis is "result_evidence"', () => {
    const withBasis = (cost_basis: string | undefined) => entryWith({
      capabilities: { host_id: 'd', worker_rollup: { task_kinds: [], task_statuses: [], activity_phases: [], cost_basis, subagent_cost: false } } as unknown as NexHostEntry['capabilities'],
    })
    seedList([row({ id: 'exc_a', cost_usd: 0.25, running_tasks: 1 })])
    useNexHostStore.setState({ byHost: { [H]: withBasis('session_cumulative') } }) // today's v0.13.1 daemon
    const { unmount } = render(<ExecutionsView hostId={H} isActive />)
    expect(screen.queryByTestId('executions-cost')).toBeNull()
    expect(screen.getByTestId('executions-running')).toHaveTextContent('1') // the rest of the rollup is not gated
    unmount()
    useNexHostStore.setState({ byHost: { [H]: withBasis('result_evidence') } })
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.getByTestId('executions-cost')).toHaveTextContent('$0.25')
  })

  it('click opens the worker through openWorkerTab (spec §4.4: selected in place, else into the current workspace)', () => {
    seedList([row({ id: 'exc_click' })])
    render(<ExecutionsView hostId={H} isActive />)
    fireEvent.click(screen.getByTestId('executions-row'))
    expect(openWorkerTab).toHaveBeenCalledTimes(1)
    expect(openWorkerTab).toHaveBeenCalledWith({ kind: 'execution', executionId: 'exc_click', host: H })
  })

  // H2d-2 T2 (plan §0.21, user rules 1 / 5): a host hidden in this workbench keeps its executions listed; opening one
  // (it creates a tab) is not offered.
  it('hidden host: the executions stay listed, but a row is not an action — no button, not focusable, no pointer / hover, no open', () => {
    useShownHostsStore.setState({ ids: [OTHER] })
    seedList([row({ id: 'exc_a', brief: 'first' }), row({ id: 'exc_b', brief: 'second' })])
    render(<ExecutionsView hostId={H} isActive />)
    const rows = screen.getAllByTestId('executions-row')
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText('first')).toBeInTheDocument()
    expect(screen.queryAllByRole('button')).toHaveLength(0)
    for (const el of rows) {
      expect(el.tagName).not.toBe('BUTTON')
      expect(el).not.toHaveAttribute('tabindex')
      expect(el.className).not.toMatch(/cursor-pointer|hover:/)
      fireEvent.click(el)
      fireEvent.keyDown(el, { key: 'Enter' })
    }
    expect(openWorkerTab).not.toHaveBeenCalled()
  })

  it('hidden host: each row is a list item inside a list, named with the full id, state, summary and age', () => {
    useShownHostsStore.setState({ ids: [] })
    seedList([row({ id: 'exc_0123456789abcdef', state: 'completed', brief: 'fix the login flow\nmore detail', updated_at: NOW - 5 * MIN })])
    render(<ExecutionsView hostId={H} isActive />)
    const list = screen.getByRole('list')
    const item = within(list).getByRole('listitem')
    expect(item).toBe(screen.getByTestId('executions-row'))
    expect(item).not.toHaveAttribute('tabindex')
    const name = item.getAttribute('aria-label') ?? ''
    expect(name).toContain('exc_0123456789abcdef')
    expect(name).toContain('completed')
    expect(name).toContain('fix the login flow')
    expect(name).toContain(within(item).getByTestId('executions-age').textContent!)
    expect(within(list).getByRole('listitem', { name: /exc_0123456789abcdef/ })).toBe(item)
  })

  it('shown host: the rows stay buttons, with no list / listitem roles', () => {
    seedList([row({ id: 'exc_a' })])
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.queryByRole('list')).toBeNull()
    expect(screen.queryByRole('listitem')).toBeNull()
    expect(screen.getByRole('button', { name: /first|brief/ })).toBe(screen.getByTestId('executions-row'))
  })

  it('hidden host: the executions hint (its own key), in en and zh-TW', () => {
    useShownHostsStore.setState({ ids: [] })
    seedList([row({ id: 'exc_a' })])
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.getByTestId('executions-open-hint')).toHaveTextContent('Show this host in this workbench to open its executions')
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
    try {
      expect(screen.getByTestId('executions-open-hint')).toHaveTextContent('在此工作台顯示這台主機後，才能開啟它的 Execution')
    } finally {
      act(() => { useI18nStore.getState().setLocale('en') })
    }
  })

  it('shown host: rows stay buttons that open, no hint', () => {
    seedList([row({ id: 'exc_a' })])
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.queryByTestId('executions-open-hint')).toBeNull()
    expect(screen.getByTestId('executions-row').tagName).toBe('BUTTON')
  })

  it('disabled', () => {
    useNexHostStore.setState({ byHost: { [H]: entryWith({ phase: 'disabled', info: { configured: false, mounted: false, ready: false, init_error: '', effective: null } }) } })
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.getByTestId('executions-disabled')).toHaveTextContent('Nexen is not enabled on this host')
    expect(screen.queryByTestId('executions-empty')).toBeNull()
    expect(screen.getByTestId('executions-phase-dot')).toHaveAttribute('data-phase', 'disabled')
  })

  it('unavailable', () => {
    useNexHostStore.setState({ byHost: { [H]: entryWith({ phase: 'unavailable', error: 'boom' }) } })
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.getByTestId('executions-unavailable')).toHaveTextContent('Nexen is unavailable on this host: boom')
    expect(screen.queryByTestId('executions-empty')).toBeNull()
  })

  it('empty', () => {
    seedList([])
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.getByTestId('executions-empty')).toHaveTextContent('No executions on this host.')
    expect(screen.queryByTestId('executions-loading')).toBeNull()
  })

  it('lists live conversations only, one row each: terminated / archived rows hidden, the newer stint of a session wins', () => {
    seedList([
      row({ id: 'exc_dead', brief: 'dead', state: 'terminated', updated_at: NOW - 1 }),
      row({ id: 'exc_gone', brief: 'gone', archived: true, updated_at: NOW - 2 }),
      row({ id: 'exc_stint1', brief: 'stint one', session_id: 'S', created_at: 10, updated_at: NOW - 3 }),
      row({ id: 'exc_stint2', brief: 'stint two', session_id: 'S', created_at: 20, updated_at: NOW - 4 }),
    ])
    render(<ExecutionsView hostId={H} isActive />)
    const rows = screen.getAllByTestId('executions-row')
    expect(rows).toHaveLength(1)
    expect(rows[0]).toHaveTextContent('stint two')
    fireEvent.click(rows[0])
    expect(openWorkerTab).toHaveBeenCalledWith({ kind: 'execution', executionId: 'exc_stint2', host: H })
  })

  it('items with no live row show the empty state', () => {
    seedList([
      row({ id: 'exc_dead', state: 'terminated' }),
      row({ id: 'exc_gone', archived: true }),
    ])
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.getByTestId('executions-empty')).toBeInTheDocument()
    expect(screen.queryByTestId('executions-row')).toBeNull()
  })

  it('loading skeleton while the first fetch is out', () => {
    seedList([], { phase: 'loading' })
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.getByTestId('executions-loading')).toBeInTheDocument()
    expect(screen.queryByTestId('executions-empty')).toBeNull()
  })

  it('loading skeleton while nex readiness is still being checked', () => {
    useNexHostStore.setState({ byHost: { [H]: entryWith({ phase: 'loading', info: null }) } })
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.getByTestId('executions-loading')).toBeInTheDocument()
    expect(screen.getByTestId('executions-phase-dot')).toHaveAttribute('data-phase', 'loading')
  })

  it('error + retry', async () => {
    seedList([row({ id: 'exc_kept' })])
    vi.mocked(api.listExecutions).mockRejectedValueOnce(new Error('nex_unavailable'))
    render(<ExecutionsView hostId={H} isActive />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByTestId('executions-error')).toHaveTextContent('Could not load: nex_unavailable')
    expect(screen.getByTestId('executions-row')).toBeInTheDocument()
    expect(screen.queryByTestId('executions-loading')).toBeNull()

    vi.mocked(api.listExecutions).mockResolvedValueOnce({ items: [row({ id: 'exc_fresh', brief: 'fresh' })], next_cursor: '' })
    fireEvent.click(screen.getByTestId('executions-retry'))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
    expect(screen.queryByTestId('executions-error')).toBeNull()
    expect(screen.getByText('fresh')).toBeInTheDocument()
  })

  // #1627 C: a keyboard retry keeps focus — the button stays (disabled, aria-busy) while its retry runs, with a status
  // line; a failure keeps focus on it, a success moves it to the list.
  describe('a keyboard retry keeps its focus', () => {
    async function failThenPress() {
      vi.mocked(api.listExecutions).mockRejectedValueOnce(new Error('boom'))
      render(<ExecutionsView hostId={H} isActive />)
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      let settle!: { resolve: (v: Awaited<ReturnType<typeof api.listExecutions>>) => void; reject: (e: unknown) => void }
      vi.mocked(api.listExecutions).mockReturnValueOnce(new Promise((resolve, reject) => { settle = { resolve, reject } }))
      screen.getByTestId('executions-retry').focus()
      fireEvent.click(screen.getByTestId('executions-retry')) // Enter / Space on the focused button
      return settle
    }

    it('the button stays, busy, with a status line while the retry runs; a failure keeps focus on it', async () => {
      const settle = await failThenPress()
      const retry = screen.getByTestId('executions-retry')
      expect(retry).toBeDisabled()
      expect(retry).toHaveAttribute('aria-busy', 'true')
      expect(screen.getByTestId('executions-error')).toHaveTextContent('Could not load: boom')
      expect(screen.getByTestId('executions-loading')).toHaveAttribute('role', 'status')
      expect(screen.queryByTestId('executions-empty')).toBeNull()
      // A browser drops focus from a button that turns disabled; jsdom keeps it — so the failure must refocus it itself.
      const refocus = vi.spyOn(retry, 'focus')
      await act(async () => { settle.reject(new Error('again')) })
      expect(screen.getByTestId('executions-retry')).toBe(retry)
      expect(retry).toBeEnabled()
      expect(screen.getByTestId('executions-error')).toHaveTextContent('Could not load: again')
      expect(refocus).toHaveBeenCalled()
      expect(document.activeElement).toBe(retry)
    })

    it('a success moves focus to the list', async () => {
      const settle = await failThenPress()
      await act(async () => { settle.resolve({ items: [row({ id: 'exc_fresh', brief: 'fresh' })], next_cursor: '' }) })
      expect(screen.queryByTestId('executions-retry')).toBeNull()
      expect(screen.getByText('fresh')).toBeInTheDocument()
      expect(document.activeElement).toBe(screen.getByTestId('executions-list'))
    })
  })

  it('error with no rows shows the error line, not the skeleton or the empty hint', async () => {
    vi.mocked(api.listExecutions).mockRejectedValueOnce(new Error('boom'))
    render(<ExecutionsView hostId={H} isActive />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByTestId('executions-error')).toHaveTextContent('Could not load: boom')
    expect(screen.queryByTestId('executions-loading')).toBeNull()
    expect(screen.queryByTestId('executions-empty')).toBeNull()
  })

  it('renders a garbage page seeded straight into the store without throwing (belt and braces below the validator)', () => {
    seedList([
      row({ id: 'exc_objsource', labels: { source: { nested: true } } as unknown as Record<string, string>, updated_at: NOW - 1 }),
      { ...row({ id: 'exc_numbrief' }), brief: 42, labels: null, origin: 7, updated_at: NOW - 2 } as unknown as ExecutionSummary,
    ])
    render(<ExecutionsView hostId={H} isActive />)
    const rows = screen.getAllByTestId('executions-row')
    expect(rows).toHaveLength(2)
    expect(screen.getByTestId('executions-group-local')).toBeInTheDocument()
    // A non-string brief is no brief (#1771): the row falls back to its cwd basename ('/w' → 'w').
    expect(within(rows[1]).getByTestId('executions-brief').textContent).toBe('w')
    expect(within(rows[1]).queryByTestId('executions-marker')).toBeNull()
  })

  it('a malformed row from the API is dropped at the boundary; the rest still render', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.mocked(api.listExecutions).mockResolvedValueOnce({
      items: [row({ id: 'exc_fine', brief: 'fine' }), { state: 'idle', brief: 'no id' }],
      next_cursor: '',
    } as never)
    render(<ExecutionsView hostId={H} isActive />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getAllByTestId('executions-row')).toHaveLength(1)
    expect(screen.getByText('fine')).toBeInTheDocument()
  })

  it('table and sidebar mounted together open one site-wide SSE per host', async () => {
    render(
      <>
        <NexExecutionsTable hostId={H} enabled />
        <ExecutionsView hostId={H} isActive />
      </>,
    )
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
    expect(vi.mocked(sse.openNexSse).mock.calls[0][0].hostId).toBe(H)
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
  })

  it('switching hosts re-subscribes for the new host', async () => {
    useNexHostStore.setState({ byHost: { [H]: readyEntry, [OTHER]: readyEntry }, ensure })
    const { rerender } = render(<ExecutionsView hostId={H} isActive />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    rerender(<ExecutionsView hostId={OTHER} isActive />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(within(screen.getByTestId('executions-header')).getByText('Air')).toBeInTheDocument()
    expect(ensure).toHaveBeenCalledWith(OTHER)
    expect(vi.mocked(sse.openNexSse).mock.calls.map((c) => c[0].hostId)).toEqual([H, OTHER])
  })

  it('exits an idle row directly and confirms a running one', async () => {
    seedList([row({ id: 'I', state: 'idle', brief: 'idle one' }), row({ id: 'R', state: 'running', brief: 'run one' })])
    render(<ExecutionsView hostId={H} isActive />)
    const rows = screen.getAllByTestId('executions-row')
    const exits = screen.getAllByTestId('executions-row-exit')
    const idleIdx = rows.findIndex((r) => r.textContent?.includes('idle one'))
    fireEvent.click(exits[idleIdx])
    expect(exitWorker).toHaveBeenCalledWith({ hostId: H, executionId: 'I' })
    fireEvent.click(exits[1 - idleIdx])
    expect(exitWorker).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByTestId('exit-confirm'))
    expect(exitWorker).toHaveBeenLastCalledWith({ hostId: H, executionId: 'R' })
    expect(screen.queryByTestId('exit-dialog')).toBeNull()
  })

  it('cancelling the confirm exits nothing', () => {
    seedList([row({ id: 'R', state: 'running' })])
    render(<ExecutionsView hostId={H} isActive />)
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    fireEvent.click(screen.getByTestId('exit-cancel'))
    expect(exitWorker).not.toHaveBeenCalled()
  })

  it('a failed exit shows a toast', async () => {
    seedList([row({ id: 'I', state: 'idle' })])
    vi.mocked(exitWorker).mockRejectedValueOnce(new HandoffApiError(409, 'held_by', { principal: 'ploom:agent-7' }))
    render(<ExecutionsView hostId={H} isActive />)
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    await waitFor(() => expect(useUndoToast.getState().toast?.message).toContain('ploom:agent-7'))
  })

  it('a second click while the exit is pending sends no second request', async () => {
    seedList([row({ id: 'I', state: 'idle' })])
    let resolveExit!: (v: Awaited<ReturnType<typeof exitWorker>>) => void
    vi.mocked(exitWorker).mockReturnValueOnce(new Promise((res) => { resolveExit = res }))
    render(<ExecutionsView hostId={H} isActive />)
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    expect(screen.getByTestId('executions-row-exit')).toBeDisabled()
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    expect(exitWorker).toHaveBeenCalledTimes(1)
    await act(async () => { resolveExit({ exited: true, terminated: true, archived: true, state: 'terminated' }) })
  })

  it('after the exit resolves but before the refetch removes the row, a click still sends nothing', async () => {
    seedList([row({ id: 'I', state: 'idle' })])
    vi.mocked(api.listExecutions).mockResolvedValue({ items: [row({ id: 'I', state: 'idle' })], next_cursor: '' })
    render(<ExecutionsView hostId={H} isActive />)
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    await act(async () => { await Promise.resolve() })
    expect(screen.getByTestId('executions-row-exit')).toBeDisabled()
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    expect(exitWorker).toHaveBeenCalledTimes(1)
  })

  it('a failed exit frees the row to be tried again', async () => {
    seedList([row({ id: 'I', state: 'idle' })])
    vi.mocked(api.listExecutions).mockResolvedValue({ items: [row({ id: 'I', state: 'idle' })], next_cursor: '' })
    vi.mocked(exitWorker).mockRejectedValueOnce(new HandoffApiError(409, 'held_by', { principal: 'x' }))
    render(<ExecutionsView hostId={H} isActive />)
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    await waitFor(() => expect(screen.getByTestId('executions-row-exit')).toBeEnabled())
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    expect(exitWorker).toHaveBeenCalledTimes(2)
  })

  it('the pending guard is per host: a host switch does not inherit it and a late completion clears only its own key', async () => {
    useNexHostStore.setState({ byHost: { [H]: readyEntry, [OTHER]: readyEntry }, ensure })
    vi.mocked(api.listExecutions).mockResolvedValue({ items: [row({ id: 'I', state: 'idle' })], next_cursor: '' })
    const resolvers: Array<(v: Awaited<ReturnType<typeof exitWorker>>) => void> = []
    vi.mocked(exitWorker).mockImplementation(() => new Promise((res) => { resolvers.push(res) }))
    const { rerender } = render(<ExecutionsView hostId={H} isActive />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    expect(screen.getByTestId('executions-row-exit')).toBeDisabled()
    rerender(<ExecutionsView hostId={OTHER} isActive />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByTestId('executions-row-exit')).toBeEnabled()
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    expect(exitWorker).toHaveBeenCalledTimes(2)
    expect(vi.mocked(exitWorker).mock.calls[1][0]).toMatchObject({ hostId: OTHER, executionId: 'I' })
    // Host A's exit completes late: host B's guard stays.
    await act(async () => { resolvers[0]({ exited: false, terminated: false, archived: false, state: 'idle' }) })
    expect(screen.getByTestId('executions-row-exit')).toBeDisabled()
  })

  it('pending keys match the host exactly: host "a" cleanup leaves host "a:b" pending', async () => {
    const A = 'a'
    const AB = 'a:b'
    useShownHostsStore.setState({ ids: [A, AB] })
    useNexHostStore.setState({ byHost: { [A]: readyEntry, [AB]: readyEntry }, ensure })
    vi.mocked(api.listExecutions).mockImplementation(async (h: string) => (
      { items: h === AB ? [row({ id: 'I', state: 'idle' })] : [], next_cursor: '' }
    ))
    vi.mocked(exitWorker).mockImplementation(() => new Promise(() => {}))
    const { rerender } = render(<ExecutionsView hostId={AB} isActive />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    expect(screen.getByTestId('executions-row-exit')).toBeDisabled()
    rerender(<ExecutionsView hostId={A} isActive />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    rerender(<ExecutionsView hostId={AB} isActive />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByTestId('executions-row-exit')).toBeDisabled()
  })

  // #1627 Q1: the confirm is about one listed row; when that row leaves the list (its worker ended elsewhere) the
  // confirm closes by itself, as the pane header's goes with its pane — and nothing is sent.
  it('a running row that leaves the list closes its confirm by itself; nothing is sent', () => {
    seedList([row({ id: 'R', state: 'running' })])
    render(<ExecutionsView hostId={H} isActive />)
    fireEvent.click(screen.getByTestId('executions-row-exit'))
    expect(screen.getByTestId('exit-dialog')).toBeInTheDocument()
    act(() => { seedList([]) })
    expect(screen.queryByTestId('exit-dialog')).toBeNull()
    // The row coming back does not bring the confirm back.
    act(() => { seedList([row({ id: 'R', state: 'running' })]) })
    expect(screen.queryByTestId('exit-dialog')).toBeNull()
    expect(exitWorker).not.toHaveBeenCalled()
  })

  it('a confirm whose row is still listed stays open; another row leaving, or another row\'s pending exit, is untouched', () => {
    vi.mocked(exitWorker).mockImplementation(() => new Promise(() => {}))
    seedList([
      row({ id: 'R', state: 'running', brief: 'run one', session_id: 'SR' }),
      row({ id: 'I', state: 'idle', brief: 'idle one', session_id: 'SI' }),
      row({ id: 'X', state: 'idle', brief: 'other one', session_id: 'SX' }),
    ])
    render(<ExecutionsView hostId={H} isActive />)
    const exitOf = (brief: string) => within(screen.getByText(brief).closest('[data-testid="executions-row"]')!.parentElement!).getByTestId('executions-row-exit')
    fireEvent.click(exitOf('idle one'))
    expect(exitOf('idle one')).toBeDisabled()
    fireEvent.click(exitOf('run one'))
    expect(screen.getByTestId('exit-dialog')).toBeInTheDocument()
    act(() => { seedList([row({ id: 'R', state: 'running', brief: 'run one', session_id: 'SR' }), row({ id: 'I', state: 'idle', brief: 'idle one', session_id: 'SI' })]) })
    expect(screen.getByTestId('exit-dialog')).toBeInTheDocument()
    act(() => { seedList([row({ id: 'I', state: 'idle', brief: 'idle one', session_id: 'SI' })]) })
    expect(screen.queryByTestId('exit-dialog')).toBeNull()
    expect(exitOf('idle one')).toBeDisabled()
    expect(exitWorker).toHaveBeenCalledTimes(1)
  })

  it('a host hidden in this workbench offers no exit', () => {
    useShownHostsStore.setState({ ids: [OTHER] })
    seedList([row({ id: 'I', state: 'idle' })])
    render(<ExecutionsView hostId={H} isActive />)
    expect(screen.queryByTestId('executions-row-exit')).toBeNull()
  })
})
