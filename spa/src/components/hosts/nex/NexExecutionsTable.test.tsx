import { StrictMode } from 'react'
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import NexExecutionsTable, { LIST_REFRESH_DEBOUNCE_MS } from './NexExecutionsTable'
import * as api from '../../../lib/nex/nex-api'
import * as sse from '../../../lib/nex/nex-sse'
import type { NexSseOptions } from '../../../lib/nex/nex-sse'
import { NexApiError } from '../../../lib/nex/types'

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

beforeEach(() => {
  // shouldAdvanceTime: true keeps the fake clock ticking at real-time pace
  // (on top of explicit vi.advanceTimersByTimeAsync jumps below) so
  // @testing-library's `waitFor` — whose internal poll interval is not
  // detected as "fake" without a `globalThis.jest` shim (this repo has
  // none) — still progresses instead of hanging until the real 5s test
  // timeout (see @testing-library/dom's jestFakeTimersAreEnabled()).
  vi.useFakeTimers({ shouldAdvanceTime: true })
  sseOpts = null; sseClose = vi.fn<() => void>()
  vi.mocked(sse.openNexSse).mockReset().mockImplementation((o) => { sseOpts = o; return { close: sseClose } })
  vi.mocked(api.listExecutions).mockReset().mockResolvedValue({ items: [row()], next_cursor: '' })
  vi.mocked(api.attachControl).mockReset().mockResolvedValue({ mode: 'control', lease_id: 'ls', expires_at: 1 })
  vi.mocked(api.terminateExecution).mockReset().mockResolvedValue(undefined)
  vi.mocked(api.releaseLease).mockReset().mockResolvedValue(undefined)
  vi.mocked(api.archiveExecution).mockReset().mockResolvedValue(undefined)
  mockOpenExecutionDetailTab.mockClear()
})
afterEach(() => vi.useRealTimers())

describe('NexExecutionsTable', () => {
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
