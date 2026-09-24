// spa/src/components/executions/ExecutionsView.test.tsx
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act, within } from '@testing-library/react'
import { ExecutionsView } from './ExecutionsView'
import NexExecutionsTable from '../hosts/nex/NexExecutionsTable'
import { resetExecutionListForTests, useExecutionListStore } from '../../stores/useExecutionListStore'
import { useNexHostStore, type NexHostEntry } from '../../stores/useNexHostStore'
import { useHostStore } from '../../stores/useHostStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTabStore } from '../../stores/useTabStore'
import { subscriptionSlots } from '../../lib/nex/subscription-slots'
import { STATE_DOT_CLASSES } from '../../lib/nex/state-dot'
import type { ExecutionSummary } from '../../lib/nex/types'
import * as api from '../../lib/nex/nex-api'
import * as sse from '../../lib/nex/nex-sse'

vi.mock('../../lib/nex/nex-api', () => ({ listExecutions: vi.fn(), attachControl: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), archiveExecution: vi.fn() }))
vi.mock('../../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))
vi.mock('../../lib/deeplink/deeplinkResolver', () => ({ openExecutionDetailTab: vi.fn() }))

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

function seedList(items: ExecutionSummary[], patch: Partial<{ phase: 'idle' | 'loading' | 'ready' | 'error'; error: string | null }> = {}) {
  useExecutionListStore.setState({ byHost: { [H]: { items, phase: 'ready', error: null, lastSeq: null, refreshRevision: 0, ...patch } } })
}

type OpenSingletonTab = ReturnType<typeof useTabStore.getState>['openSingletonTab']
let ensure: ReturnType<typeof vi.fn<(hostId: string) => Promise<void>>>
let openSingletonTab: ReturnType<typeof vi.fn<OpenSingletonTab>>

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
  openSingletonTab = vi.fn<OpenSingletonTab>().mockReturnValue('tab-1')
  useTabStore.setState({ openSingletonTab })
  vi.mocked(sse.openNexSse).mockReset().mockImplementation(() => ({ close: vi.fn() }))
  vi.mocked(api.listExecutions).mockReset().mockResolvedValue({ items: [], next_cursor: '' })
})
afterEach(() => vi.useRealTimers())

describe('ExecutionsView', () => {
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

  it('only includeArchived: false, limit: 100 is requested', async () => {
    render(<ExecutionsView hostId={H} isActive />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    expect(api.listExecutions).toHaveBeenCalledWith(H, { includeArchived: false, limit: 100 })
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

  it('click opens the singleton tab', () => {
    seedList([row({ id: 'exc_click' })])
    render(<ExecutionsView hostId={H} isActive />)
    fireEvent.click(screen.getByTestId('executions-row'))
    expect(openSingletonTab).toHaveBeenCalledTimes(1)
    expect(openSingletonTab).toHaveBeenCalledWith({ kind: 'execution', executionId: 'exc_click', host: H })
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
    expect(openSingletonTab).not.toHaveBeenCalled()
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
    expect(within(rows[1]).getByTestId('executions-brief').textContent).toBe('')
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
})
