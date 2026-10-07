import { StrictMode } from 'react'
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, act, within } from '@testing-library/react'
import NexExecutionsTable from './NexExecutionsTable'
import { LIST_REFRESH_DEBOUNCE_MS, resetExecutionListForTests, useExecutionListStore } from '../../../stores/useExecutionListStore'
import { useNexHostStore, type NexHostEntry } from '../../../stores/useNexHostStore'
import { subscriptionSlots } from '../../../lib/nex/subscription-slots'
import * as api from '../../../lib/nex/nex-api'
import * as sse from '../../../lib/nex/nex-sse'
import type { NexSseOptions } from '../../../lib/nex/nex-sse'
import { NexApiError } from '../../../lib/nex/types'
import { useShownHostsStore } from '../../../stores/useShownHostsStore'

const { mockOpenExecutionDetailTab } = vi.hoisted(() => ({ mockOpenExecutionDetailTab: vi.fn() }))
vi.mock('../../../lib/deeplink/deeplinkResolver', () => ({ openExecutionDetailTab: mockOpenExecutionDetailTab }))
vi.mock('../../../lib/nex/nex-api', () => ({ listExecutions: vi.fn(), attachControl: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), archiveExecution: vi.fn() }))
vi.mock('../../../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))
vi.mock('../../../lib/nex/client-id', () => ({ getNexClientId: () => 't-me' }))

const row = (over = {}) => ({ id: 'exc_0123456789abcdef', state: 'running', provider: 'claude', principal_id: 'p', cwd: '/Users/w/repo', mount_kind: 'dev', brief: 'first line\nsecond', labels: {}, created_at: 0, updated_at: Date.now(), duration_ms: null, event_count: 3, observers: 1, archived: false, effective_profile: 'standard', last_turn_reason: 'completed', lease: { principal_id: 'pdx:mlab/t-me', expires_at: 1 }, ...over })
// A close mock always has this shape — typed once so every `{ close }`
// literal returned from an openNexSse mock structurally satisfies
// NexSseHandle without each call site needing its own annotation
// (same pattern as useExecutionSubscription.test.ts).
type CloseMock = ReturnType<typeof vi.fn<() => void>>
let sseOpts: NexSseOptions | null
let sseClose: CloseMock

// The table reads the shared execution list store, which opens its site-wide
// SSE only for a nex-ready host: both hosts the tests switch between are
// seeded ready, and the hook's `ensure` is stubbed (no host-api here).
const readyEntry: NexHostEntry = {
  info: { configured: true, mounted: true, ready: true, init_error: '', effective: null },
  capabilities: null, phase: 'ready', error: null, fetchedAt: 1, generation: 1, fingerprint: '1:1:t',
}
const archivedCalls = () => vi.mocked(api.listExecutions).mock.calls.filter((c) => c[1]?.includeArchived === true).length

beforeEach(() => {
  // shouldAdvanceTime: true keeps the fake clock ticking at real-time pace
  // (on top of explicit vi.advanceTimersByTimeAsync jumps below) so
  // @testing-library's `waitFor` — whose internal poll interval is not
  // detected as "fake" without a `globalThis.jest` shim (this repo has
  // none) — still progresses instead of hanging until the real 5s test
  // timeout (see @testing-library/dom's jestFakeTimersAreEnabled()).
  vi.useFakeTimers({ shouldAdvanceTime: true })
  subscriptionSlots.resetForTests()
  resetExecutionListForTests()
  useExecutionListStore.setState({ byHost: {} })
  useNexHostStore.setState({ byHost: { h: readyEntry, h2: readyEntry }, ensure: vi.fn().mockResolvedValue(undefined) })
  sseOpts = null; sseClose = vi.fn<() => void>()
  vi.mocked(sse.openNexSse).mockReset().mockImplementation((o) => { sseOpts = o; return { close: sseClose } })
  vi.mocked(api.listExecutions).mockReset().mockResolvedValue({ items: [row()], next_cursor: '' })
  vi.mocked(api.attachControl).mockReset().mockResolvedValue({ mode: 'control', lease_id: 'ls', expires_at: 1 })
  vi.mocked(api.terminateExecution).mockReset().mockResolvedValue(undefined)
  vi.mocked(api.releaseLease).mockReset().mockResolvedValue(undefined)
  vi.mocked(api.archiveExecution).mockReset().mockResolvedValue(undefined)
  mockOpenExecutionDetailTab.mockClear()
  useShownHostsStore.setState({ ids: ['h', 'h2'] }) // shown in this workbench unless a test hides one (H2d-2)
})
afterEach(() => vi.useRealTimers())

// H2d-2 T2 (plan §0.21, user rules 1 / 5): a host hidden in this workbench keeps its executions listed and manageable
// (terminate / archive); only "open" (it creates a tab) is not offered.
describe('NexExecutionsTable — a host hidden in this workbench (H2d-2)', () => {
  it('rows stay listed with terminate / archive, but no Open and the hint instead; clicking creates no tab', async () => {
    useShownHostsStore.setState({ ids: ['h2'] })
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByText('exc_01234567')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /terminate/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^archive$/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^open$/i })).toBeNull()
    expect(screen.getByTestId('nex-executions-open-hint')).toHaveTextContent('Show this host in this workbench to open its executions')
    const tr = screen.getByText('exc_01234567').closest('tr')!
    fireEvent.click(tr)
    for (const cell of Array.from(tr.querySelectorAll('td'))) fireEvent.click(cell)
    for (const button of Array.from(tr.querySelectorAll('button'))) fireEvent.click(button)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(mockOpenExecutionDetailTab).not.toHaveBeenCalled()
  })

  it('shown again: Open comes back and the hint goes', async () => {
    useShownHostsStore.setState({ ids: [] })
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => useShownHostsStore.setState({ ids: ['h'] }))
    expect(screen.queryByTestId('nex-executions-open-hint')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: /^open$/i }))
    expect(mockOpenExecutionDetailTab).toHaveBeenCalledWith('exc_0123456789abcdef', 'h')
  })
})

describe('NexExecutionsTable', () => {
  it('renders at most 100 rows at first and reveals more on demand (#1593)', async () => {
    const many = Array.from({ length: 250 }, (_, i) => row({ id: `exc_${String(i).padStart(16, '0')}` }))
    vi.mocked(api.listExecutions).mockResolvedValue({ items: many, next_cursor: '' })
    const { container } = render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    const rows = () => container.querySelectorAll('tbody tr').length
    expect(rows()).toBe(100)
    fireEvent.click(screen.getByText(/show more/i))
    expect(rows()).toBe(200)
    fireEvent.click(screen.getByText(/show more/i))
    expect(rows()).toBe(250)
    expect(screen.queryByText(/show more/i)).toBeNull()
  })

  it('a terminate confirmation does not survive its row leaving the rendered slice (#1593 R2)', async () => {
    // Text queries, not role queries: a role query over a 100+ row table is slow enough to hit the test timeout under load.
    const mk = (n: number) => Array.from({ length: n }, (_, i) => row({ id: `exc_${String(i).padStart(16, '0')}` }))
    vi.mocked(api.listExecutions).mockResolvedValue({ items: mk(150), next_cursor: '' })
    const { container } = render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByText(/show more/i))
    const lastRow = () => container.querySelectorAll('tbody tr')[149] as HTMLElement
    fireEvent.click(within(lastRow()).getByText(/^terminate$/i))
    expect(within(lastRow()).getByText(/confirm terminate/i)).toBeInTheDocument()
    // a refresh removes the confirming row from the list, then it comes back
    vi.mocked(api.listExecutions).mockResolvedValue({ items: mk(100), next_cursor: '' })
    fireEvent.click(screen.getByText(/^refresh$/i))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    vi.mocked(api.listExecutions).mockResolvedValue({ items: mk(150), next_cursor: '' })
    fireEvent.click(screen.getByText(/^refresh$/i))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    // the page count (200) was kept, so the row is rendered again — without its stale confirmation
    expect(lastRow()).toBeDefined()
    expect(within(lastRow()).queryByText(/confirm terminate/i)).toBeNull()
  })

  it('toggling Show archived clears an open terminate confirmation (#1593 R2)', async () => {
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByRole('button', { name: /terminate/i }))
    expect(screen.getByRole('button', { name: /confirm terminate/i })).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText(/show archived/i))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.queryByRole('button', { name: /confirm terminate/i })).toBeNull()
  })

  it('a host switch starts from 100 rows again, not the grown count (#1593)', async () => {
    const many = Array.from({ length: 250 }, (_, i) => row({ id: `exc_${String(i).padStart(16, '0')}` }))
    vi.mocked(api.listExecutions).mockResolvedValue({ items: many, next_cursor: '' })
    const { container, rerender } = render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByText(/show more/i))
    expect(container.querySelectorAll('tbody tr').length).toBe(200)
    rerender(<NexExecutionsTable hostId="h2" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(container.querySelectorAll('tbody tr').length).toBe(100)
  })
  it('lists executions with (you) on my lease and opens a host-scoped execution pane via the deeplink helper', async () => {
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByText('exc_01234567')).toBeInTheDocument()
    expect(screen.getByText(/\(you\)/)).toBeInTheDocument()
    expect(screen.getByText('first line')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /open/i }))
    // Spec §4.4.3: Open goes through the same helper the deeplink resolver
    // uses (openExecutionDetailTab), not a hand-rolled openSingletonTab call.
    expect(mockOpenExecutionDetailTab).toHaveBeenCalledWith('exc_0123456789abcdef', 'h')
  })

  it('opens one site-wide SSE as a refresh signal, debounces refetch, never applies frames', async () => {
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
    expect(sseOpts!.url).toBe('/api/nex/v1/events')
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    act(() => {
      sseOpts!.onFrame({ id: '1', event: 'execution.delegated', data: '{}' })
      sseOpts!.onFrame({ id: '2', event: 'execution.running', data: '{}' })
      sseOpts!.onFrame({ id: null, event: 'stream_event', data: '{}' })
    })
    // Still within the debounce window — no refetch yet.
    await act(async () => { await vi.advanceTimersByTimeAsync(400) })
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    // Past the debounce window — exactly one trailing refetch.
    await act(async () => { await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS + 1 - 400) })
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
    expect(sseOpts!.getLastEventId()).toBe(2) // cursor kept for reconnect; transient frames do not move it
  })

  it('refetches after the SSE reconnects (a gap may not replay without a durable id seen yet)', async () => {
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    act(() => { sseOpts!.onStatus('reconnecting') })
    act(() => { sseOpts!.onStatus('open') })
    await act(async () => { await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS + 1) })
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
  })

  it('does not refetch on the initial connecting -> open transition (only reconnecting -> open)', async () => {
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    act(() => { sseOpts!.onStatus('connecting') })
    act(() => { sseOpts!.onStatus('open') })
    await act(async () => { await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS + 1) })
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
  })

  it('does nothing while disabled and closes the SSE on unmount', async () => {
    const { unmount, rerender } = render(<NexExecutionsTable hostId="h" enabled={false} />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.listExecutions).not.toHaveBeenCalled()
    rerender(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
    unmount()
    expect(sseClose).toHaveBeenCalledTimes(1)
  })

  it('manual Refresh does nothing while disabled', async () => {
    render(<NexExecutionsTable hostId="h" enabled={false} />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByRole('button', { name: /refresh/i }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.listExecutions).not.toHaveBeenCalled()
  })

  it('terminate needs confirmation, then takes a lease, terminates and releases', async () => {
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByRole('button', { name: /^terminate$/i }))
    expect(api.terminateExecution).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: /confirm terminate/i }))
    await waitFor(() => {
      expect(api.terminateExecution).toHaveBeenCalledWith('h', 'exc_0123456789abcdef', 'ls')
      expect(api.releaseLease).toHaveBeenCalledWith('h', 'exc_0123456789abcdef', 'ls')
    })
  })

  it('shows the NexApiError code inline when attachControl fails (e.g. lease already held)', async () => {
    vi.mocked(api.attachControl).mockRejectedValueOnce(new NexApiError(409, 'lease_held', 'lease held elsewhere'))
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByRole('button', { name: /^terminate$/i }))
    fireEvent.click(screen.getByRole('button', { name: /confirm terminate/i }))
    await waitFor(() => expect(screen.getByTestId('nex-executions-action-error')).toHaveTextContent('lease_held'))
    expect(api.terminateExecution).not.toHaveBeenCalled()
    expect(api.releaseLease).not.toHaveBeenCalled()
  })

  it('swallows a releaseLease rejection (best-effort, spec §4.3.3) — no action error, list still refetched', async () => {
    vi.mocked(api.releaseLease).mockRejectedValueOnce(new Error('already gone'))
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: /^terminate$/i }))
    fireEvent.click(screen.getByRole('button', { name: /confirm terminate/i }))
    await waitFor(() => expect(api.terminateExecution).toHaveBeenCalledWith('h', 'exc_0123456789abcdef', 'ls'))
    await waitFor(() => expect(api.listExecutions).toHaveBeenCalledTimes(2))
    expect(screen.queryByTestId('nex-executions-action-error')).not.toBeInTheDocument()
  })

  it('archive toggles and include-archived re-queries', async () => {
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByRole('button', { name: /^archive$/i }))
    await waitFor(() => expect(api.archiveExecution).toHaveBeenCalledWith('h', 'exc_0123456789abcdef', false))
    fireEvent.click(screen.getByLabelText(/show archived/i))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(vi.mocked(api.listExecutions).mock.calls.at(-1)![1]).toMatchObject({ includeArchived: true })
  })

  it('keeps the cursor across an enabled toggle on the same host, but resets it when hostId changes', async () => {
    const { rerender } = render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { sseOpts!.onFrame({ id: '5', event: 'execution.running', data: '{}' }) })
    expect(sseOpts!.getLastEventId()).toBe(5)

    // enabled -> false -> true, same host: cursor must survive.
    rerender(<NexExecutionsTable hostId="h" enabled={false} />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    rerender(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sseOpts!.getLastEventId()).toBe(5)

    // hostId change: cursor must reset (a durable seq from another host's
    // log means nothing here).
    rerender(<NexExecutionsTable hostId="h2" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sseOpts!.getLastEventId()).toBe(null)
  })

  it('clears stale rows on a host change, even when the new host\'s fetch fails', async () => {
    vi.mocked(api.listExecutions).mockReset().mockImplementation((hostId: string) => {
      if (hostId === 'h') return Promise.resolve({ items: [row({ id: 'aaaaaaaaaaaaaaaaaaaaaaaaaa' })], next_cursor: '' })
      return Promise.reject(new NexApiError(500, 'boom', 'boom'))
    })
    const { rerender } = render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByText('aaaaaaaaaaaa')).toBeInTheDocument()

    rerender(<NexExecutionsTable hostId="h2" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.queryByText('aaaaaaaaaaaa')).not.toBeInTheDocument()
  })

  it('does not let a late action refetch render the old host\'s list under the new host', async () => {
    let resolveArchive: () => void = () => {}
    vi.mocked(api.archiveExecution).mockImplementation(() => new Promise((resolve) => { resolveArchive = resolve }))
    vi.mocked(api.listExecutions).mockReset().mockImplementation((hostId: string) => {
      if (hostId === 'h') return Promise.resolve({ items: [row({ id: 'aaaaaaaaaaaaaaaaaaaaaaaaaa' })], next_cursor: '' })
      return Promise.resolve({ items: [row({ id: 'bbbbbbbbbbbbbbbbbbbbbbbbbb' })], next_cursor: '' })
    })

    const { rerender } = render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByText('aaaaaaaaaaaa')).toBeInTheDocument()

    // Start an archive on h; it never resolves yet.
    fireEvent.click(screen.getByRole('button', { name: /^archive$/i }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })

    // Switch to h2 while the archive is still in flight.
    rerender(<NexExecutionsTable hostId="h2" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByText('bbbbbbbbbbbb')).toBeInTheDocument()
    expect(screen.queryByText('aaaaaaaaaaaa')).not.toBeInTheDocument()

    const listCallsForHBeforeResolve = vi.mocked(api.listExecutions).mock.calls.filter((c) => c[0] === 'h').length

    // Now let the stale h archive resolve.
    await act(async () => {
      resolveArchive()
      await vi.advanceTimersByTimeAsync(0)
    })

    // No new listExecutions('h', ...) call, and h2's list is still what's shown.
    expect(vi.mocked(api.listExecutions).mock.calls.filter((c) => c[0] === 'h').length).toBe(listCallsForHBeforeResolve)
    expect(screen.getByText('bbbbbbbbbbbb')).toBeInTheDocument()
    expect(screen.queryByText('aaaaaaaaaaaa')).not.toBeInTheDocument()
  })

  // List rows carry no `lease` field (confirmed live on mlab).
  // Render "—" instead of a lease holder when it is absent.
  it('renders — for a row with no lease (list rows may omit lease)', async () => {
    vi.mocked(api.listExecutions).mockResolvedValue({
      items: [row({ id: 'exc_ffffffffffffffff', lease: undefined })],
      next_cursor: '',
    })
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByTestId('nex-lease-exc_ffffffffffffffff')).toHaveTextContent('—')
  })

  describe('R4 T4.2: rollup columns (cost, turns, last tool, running)', () => {
    const rollupEntry = (cost_basis: string | undefined): NexHostEntry => ({
      ...readyEntry,
      capabilities: { host_id: 'd', worker_rollup: { task_kinds: [], task_statuses: [], activity_phases: [], cost_basis, subagent_cost: false } } as unknown as NexHostEntry['capabilities'],
    })
    const cells = (id: string) => ['cost', 'turns', 'last-tool', 'running'].map((c) => screen.getByTestId(`nex-${c}-${id}`))
    const ID = 'exc_rollup000000000000000000'

    it('header has the four columns after observers', async () => {
      render(<NexExecutionsTable hostId="h" enabled />)
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      const heads = screen.getAllByRole('columnheader').map((h) => h.textContent)
      const at = heads.indexOf('Observers')
      expect(heads.slice(at + 1, at + 5)).toEqual(['Cost', 'Turns', 'Last tool', 'Running'])
    })

    it('a row with rollup fields and cost_basis result_evidence shows all four; the hand-over note is the cost title', async () => {
      useNexHostStore.setState({ byHost: { h: rollupEntry('result_evidence') } })
      vi.mocked(api.listExecutions).mockResolvedValue({
        items: [row({ id: ID, cost_usd: 0.1126, turn_count: 3, running_tasks: 2, resume_session_id: 'c191a5a0', last_tool: { name: 'Bash', tool_use_id: 't', at: 1 } })],
        next_cursor: '',
      })
      render(<NexExecutionsTable hostId="h" enabled />)
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      const [cost, turns, lastTool, running] = cells(ID)
      expect(cost).toHaveTextContent('$0.11')
      expect(cost.querySelector('[title]')).toHaveAttribute('title', 'Includes spend from before the hand-over')
      expect(turns).toHaveTextContent('3')
      expect(lastTool).toHaveTextContent('Bash')
      expect(running).toHaveTextContent('2')
    })

    it('today\'s v0.13.1 daemon (cost_basis session_cumulative): cost is — , the rest still shows', async () => {
      useNexHostStore.setState({ byHost: { h: rollupEntry('session_cumulative') } })
      vi.mocked(api.listExecutions).mockResolvedValue({ items: [row({ id: ID, cost_usd: 0.1126, turn_count: 3, running_tasks: 0 })], next_cursor: '' })
      render(<NexExecutionsTable hostId="h" enabled />)
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      const [cost, turns, lastTool, running] = cells(ID)
      expect(cost).toHaveTextContent('—')
      expect(turns).toHaveTextContent('3')
      expect(lastTool).toHaveTextContent('—')
      expect(running).toHaveTextContent('0')
    })

    it('cost_usd null → —', async () => {
      useNexHostStore.setState({ byHost: { h: rollupEntry('result_evidence') } })
      vi.mocked(api.listExecutions).mockResolvedValue({ items: [row({ id: ID, cost_usd: null })], next_cursor: '' })
      render(<NexExecutionsTable hostId="h" enabled />)
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      expect(cells(ID)[0]).toHaveTextContent('—')
    })

    it('an old daemon\'s row (no rollup fields): — in all four', async () => {
      vi.mocked(api.listExecutions).mockResolvedValue({ items: [row({ id: ID })], next_cursor: '' })
      render(<NexExecutionsTable hostId="h" enabled />)
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      for (const cell of cells(ID)) expect(cell).toHaveTextContent('—')
    })
  })

  it('archived toggle on: frame, reconnect, archive and terminate refresh the archived query', async () => {
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(archivedCalls()).toBe(0)

    fireEvent.click(screen.getByLabelText(/show archived/i))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(archivedCalls()).toBe(1)
    expect(api.listExecutions).toHaveBeenLastCalledWith('h', { includeArchived: true, limit: 100 })

    // SSE frame → the store's debounced refresh commits → revision bump → re-query.
    act(() => { sseOpts!.onFrame({ id: '1', event: 'execution.running', data: '{}' }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS + 1) })
    expect(archivedCalls()).toBe(2)
    expect(api.listExecutions).toHaveBeenLastCalledWith('h', { includeArchived: true, limit: 100 })

    // Reconnect.
    act(() => { sseOpts!.onStatus('reconnecting') })
    act(() => { sseOpts!.onStatus('open') })
    await act(async () => { await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS + 1) })
    expect(archivedCalls()).toBe(3)
    expect(api.listExecutions).toHaveBeenLastCalledWith('h', { includeArchived: true, limit: 100 })

    // Archive action → shared refetch → revision bump → re-query.
    fireEvent.click(screen.getByRole('button', { name: /^archive$/i }))
    await waitFor(() => expect(api.archiveExecution).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(archivedCalls()).toBe(4))
    expect(api.listExecutions).toHaveBeenLastCalledWith('h', { includeArchived: true, limit: 100 })

    // Terminate action.
    fireEvent.click(screen.getByRole('button', { name: /^terminate$/i }))
    fireEvent.click(screen.getByRole('button', { name: /confirm terminate/i }))
    await waitFor(() => expect(api.terminateExecution).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(archivedCalls()).toBe(5))
    expect(api.listExecutions).toHaveBeenLastCalledWith('h', { includeArchived: true, limit: 100 })
  })

  it('archived mode: a failed shared refresh still re-runs the archived query and its error is shown', async () => {
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByLabelText(/show archived/i))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(archivedCalls()).toBe(1)
    expect(screen.queryByText(/nex_unavailable/)).not.toBeInTheDocument()

    // The shared (non-archived) refresh fails; the archived query succeeds.
    vi.mocked(api.listExecutions).mockImplementation((_hostId, opts) =>
      opts?.includeArchived
        ? Promise.resolve({ items: [row()], next_cursor: '' })
        : Promise.reject(new NexApiError(503, 'nex_unavailable', 'down')))
    fireEvent.click(screen.getByRole('button', { name: /refresh/i }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(archivedCalls()).toBe(2)
    expect(screen.getByText(/nex_unavailable/)).toBeInTheDocument()
    expect(screen.getByText('exc_01234567')).toBeInTheDocument()
  })

  it('a slow archived response from host A is not committed after switching to host B with enabled=false', async () => {
    let resolveArchived: (p: { items: ReturnType<typeof row>[]; next_cursor: string }) => void = () => {}
    vi.mocked(api.listExecutions).mockReset().mockImplementation((_hostId, opts) =>
      opts?.includeArchived
        ? new Promise((resolve) => { resolveArchived = resolve })
        : Promise.resolve({ items: [row()], next_cursor: '' }))
    const { rerender } = render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByLabelText(/show archived/i))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(archivedCalls()).toBe(1)

    rerender(<NexExecutionsTable hostId="h2" enabled={false} />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    await act(async () => {
      resolveArchived({ items: [row({ id: 'exc_stalearchivedstalearchived', archived: true })], next_cursor: '' })
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(screen.queryByText('exc_stalear')).not.toBeInTheDocument()
    expect(archivedCalls()).toBe(1)
  })

  it('a slow archived response is not committed once the same host is disabled', async () => {
    let resolveArchived: (p: { items: ReturnType<typeof row>[]; next_cursor: string }) => void = () => {}
    vi.mocked(api.listExecutions).mockReset().mockImplementation((_hostId, opts) =>
      opts?.includeArchived
        ? new Promise((resolve) => { resolveArchived = resolve })
        : Promise.resolve({ items: [row()], next_cursor: '' }))
    const { rerender } = render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByLabelText(/show archived/i))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(archivedCalls()).toBe(1)

    rerender(<NexExecutionsTable hostId="h" enabled={false} />)
    await act(async () => {
      resolveArchived({ items: [row({ id: 'exc_stalearchivedstalearchived', archived: true })], next_cursor: '' })
      await vi.advanceTimersByTimeAsync(0)
    })
    // The shared (cached) rows stay on screen; the late archived page is dropped.
    expect(screen.queryByText('exc_stalear')).not.toBeInTheDocument()
    expect(screen.getByText('exc_01234567')).toBeInTheDocument()
  })

  it('off→on quick toggle: the first request\'s late response is ignored, only the new request\'s result renders', async () => {
    const pending: Array<(p: { items: ReturnType<typeof row>[]; next_cursor: string }) => void> = []
    vi.mocked(api.listExecutions).mockReset().mockImplementation((_hostId, opts) =>
      opts?.includeArchived
        ? new Promise((resolve) => { pending.push(resolve) })
        : Promise.resolve({ items: [row()], next_cursor: '' }))
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })

    fireEvent.click(screen.getByLabelText(/show archived/i)) // on: request 1
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByLabelText(/show archived/i)) // off
    // Request 1 lands while the toggle is off.
    await act(async () => {
      pending[0]({ items: [row({ id: 'exc_firstfirstfirstfirstfirst', archived: true })], next_cursor: '' })
      await vi.advanceTimersByTimeAsync(0)
    })
    fireEvent.click(screen.getByLabelText(/show archived/i)) // on: request 2
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(pending).toHaveLength(2)
    // Nothing from request 1 shows while request 2 is in flight.
    expect(screen.queryByText('exc_firstfir')).not.toBeInTheDocument()
    expect(screen.getByText('exc_01234567')).toBeInTheDocument()

    await act(async () => {
      pending[1]({ items: [row({ id: 'exc_secondsecondsecondsecond', archived: true })], next_cursor: '' })
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(screen.getByText('exc_secondse')).toBeInTheDocument()
    expect(screen.queryByText('exc_firstfir')).not.toBeInTheDocument()
  })

  it('archived toggle off: no second query', async () => {
    vi.mocked(api.listExecutions).mockReset().mockImplementation((_hostId, opts) =>
      Promise.resolve({
        items: opts?.includeArchived
          ? [row(), row({ id: 'exc_archivedarchivedarchived', archived: true })]
          : [row()],
        next_cursor: '',
      }))
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.queryByText('exc_archived')).not.toBeInTheDocument()

    fireEvent.click(screen.getByLabelText(/show archived/i))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByText('exc_archived')).toBeInTheDocument()
    const calls = vi.mocked(api.listExecutions).mock.calls.length

    // Off again: the shared rows are back at once, and nothing is queried.
    fireEvent.click(screen.getByLabelText(/show archived/i))
    expect(screen.queryByText('exc_archived')).not.toBeInTheDocument()
    expect(screen.getByText('exc_01234567')).toBeInTheDocument()
    await act(async () => { await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS + 1) })
    expect(vi.mocked(api.listExecutions).mock.calls.length).toBe(calls)
  })

  it('enabled=false subscribes nothing', async () => {
    render(<NexExecutionsTable hostId="h" enabled={false} />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sse.openNexSse).not.toHaveBeenCalled()
    expect(api.listExecutions).not.toHaveBeenCalled()
    expect(useNexHostStore.getState().ensure).not.toHaveBeenCalled()
    expect(useExecutionListStore.getState().byHost.h).toBeUndefined()
    // Even the archived toggle stays quiet while the host is not ready.
    fireEvent.click(screen.getByLabelText(/show archived/i))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.listExecutions).not.toHaveBeenCalled()
  })

  // DOM preservation across the shared-store migration (P-C.2 task 3): a
  // fixed dataset — running / idle / archived (toggle off) — captured once,
  // rows + toggle + action buttons must stay byte-identical afterwards.
  it('renders a fixed dataset identically (DOM snapshot)', async () => {
    const NOW = 1_800_000_000_000
    vi.setSystemTime(NOW)
    vi.mocked(api.listExecutions).mockResolvedValue({
      items: [
        row({
          id: 'exc_running0000000000000000', state: 'running', brief: 'running one\nmore', updated_at: NOW - 5 * 60_000,
          // R4 T4.2: rollup fields (worker_rollup daemon); the other two rows are an old daemon's.
          cost_usd: 0.1126, turn_count: 3, running_tasks: 1, last_tool: { name: 'Bash', tool_use_id: 'toolu_1', at: NOW - 60_000 },
        }),
        row({ id: 'exc_idle00000000000000000000', state: 'idle', brief: 'idle one', updated_at: NOW - 3 * 3_600_000, lease: undefined, last_turn_reason: null }),
        row({ id: 'exc_archived0000000000000000', state: 'terminated', brief: 'archived one', updated_at: NOW - 2 * 86_400_000, archived: true, lease: { principal_id: 'pdx:air/t-other', expires_at: 1 } }),
      ],
      next_cursor: '',
    })
    const { container } = render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getAllByRole('row')).toHaveLength(4)
    expect(container).toMatchSnapshot()
  })

  it('renders rows and re-enables actions after an action, even under StrictMode (React 19 dev double-invokes effects)', async () => {
    render(
      <StrictMode>
        <NexExecutionsTable hostId="h" enabled />
      </StrictMode>,
    )
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByText('exc_01234567')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /^archive$/i }))
    await waitFor(() => expect(api.archiveExecution).toHaveBeenCalledWith('h', 'exc_0123456789abcdef', false))
    await waitFor(() => expect(screen.getByRole('button', { name: /^archive$/i })).not.toBeDisabled())
  })
})
