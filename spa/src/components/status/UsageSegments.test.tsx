import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import { CcUsageSegments, HostQuotaSegments } from './UsageSegments'
import { useAgentStore } from '../../stores/useAgentStore'
import { compositeKey } from '../../lib/composite-key'
import * as nexApi from '../../lib/nex/nex-api'
import type { NexHostInfo } from '../../lib/nex/types'

vi.mock('../../lib/nex/nex-api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/nex/nex-api')>()
  return { ...actual, fetchNexHost: vi.fn() }
})
const mockFetchNexHost = vi.mocked(nexApi.fetchNexHost)

const NOW = 1_800_000_000_000
const payload = (over: Record<string, unknown> = {}) => ({
  context_window: { used_percentage: 23 },
  rate_limits: {
    five_hour: { used_percentage: 24, resets_at: (NOW + (2 * 60 + 13) * 60_000) / 1000 },
    seven_day: { used_percentage: 93, resets_at: (NOW + 3 * 86_400_000) / 1000 },
  },
  ...over,
})
const seed = (raw: Record<string, unknown>, receivedAt = NOW) =>
  useAgentStore.setState({ ccStatus: { [compositeKey('h1', 's1')]: { receivedAt, raw } } })

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(NOW)
  useAgentStore.setState({ ccStatus: {} })
  mockFetchNexHost.mockReset()
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

describe('CcUsageSegments', () => {
  it('renders nothing without a snapshot or without usable fields', () => {
    const { container, rerender } = render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(container.innerHTML).toBe('')
    seed({ model: { id: 'x' } })
    rerender(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(container.innerHTML).toBe('')
  })

  it('shows context, 5h and weekly with tone and the reset time in the tooltip', () => {
    seed(payload())
    render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    const ctx = screen.getByTestId('status-seg-usage-context')
    expect(ctx.textContent).toBe('ctx 23%')
    expect(ctx.dataset.tone).toBe('ok')
    const five = screen.getByTestId('status-seg-usage-five-hour')
    expect(five.textContent).toBe('5h 24%')
    expect(five.title).toContain('resets in 2h13m')
    const week = screen.getByTestId('status-seg-usage-seven-day')
    expect(week.textContent).toBe('7d 93%')
    expect(week.dataset.tone).toBe('danger')
    expect(week.title).toContain('resets in 3d0h')
  })

  it('shifts the context tone at 70 and 90', () => {
    seed(payload({ context_window: { used_percentage: 70 } }))
    const { rerender } = render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(screen.getByTestId('status-seg-usage-context').dataset.tone).toBe('warn')
    seed(payload({ context_window: { used_percentage: 90 } }))
    rerender(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(screen.getByTestId('status-seg-usage-context').dataset.tone).toBe('danger')
  })

  it('omits the limit segments when rate_limits is absent', () => {
    seed({ context_window: { used_percentage: 10 } })
    render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(screen.getByTestId('status-seg-usage-context')).toBeTruthy()
    expect(screen.queryByTestId('status-seg-usage-five-hour')).toBeNull()
    expect(screen.queryByTestId('status-seg-usage-seven-day')).toBeNull()
  })

  it('dims a snapshot older than 10 minutes, and ages into dim while mounted', () => {
    seed(payload(), NOW - 11 * 60_000)
    render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(screen.getByTestId('status-seg-usage-context').dataset.dim).toBe('true')
    cleanup()
    seed(payload(), NOW - 9 * 60_000)
    render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(screen.getByTestId('status-seg-usage-context').dataset.dim).toBeUndefined()
    act(() => { vi.advanceTimersByTime(2 * 60_000) })
    expect(screen.getByTestId('status-seg-usage-context').dataset.dim).toBe('true')
  })

  it('drops its low-priority segments with the container-query classes the bar uses', () => {
    seed(payload())
    render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(screen.getByTestId('status-seg-usage-context').className).toContain('max-[600px]:hidden')
    expect(screen.getByTestId('status-seg-usage-five-hour').className).toContain('max-[700px]:hidden')
  })

  it('reads the pane\'s own session only', () => {
    seed(payload())
    const { container } = render(<CcUsageSegments hostId="h1" sessionCode="other" />)
    expect(container.innerHTML).toBe('')
  })
})

describe('HostQuotaSegments', () => {
  const host = (quota: NexHostInfo['quota']): NexHostInfo => ({ active_account: 'a', quota } as NexHostInfo)

  it('shows the host 5h and weekly quota once fetched', async () => {
    mockFetchNexHost.mockResolvedValue(host({ five_hour_pct: 41, seven_day_pct: 72, resets_at: (NOW + 90 * 60_000) / 1000, source: 'usage_api' }))
    render(<HostQuotaSegments hostId="h1" />)
    await act(async () => {})
    expect(mockFetchNexHost).toHaveBeenCalledWith('h1')
    const five = screen.getByTestId('status-seg-quota-five-hour')
    expect(five.textContent).toBe('5h 41%')
    expect(five.title).toContain('resets in 1h30m')
    const week = screen.getByTestId('status-seg-quota-seven-day')
    expect(week.textContent).toBe('7d 72%')
    expect(week.dataset.tone).toBe('warn')
  })

  it('is hidden when the host has no quota, a non-finite one, or the fetch fails', async () => {
    mockFetchNexHost.mockResolvedValue(host(null))
    const { container, rerender } = render(<HostQuotaSegments hostId="h1" />)
    await act(async () => {})
    expect(container.innerHTML).toBe('')
    mockFetchNexHost.mockResolvedValue(host({ five_hour_pct: NaN, seven_day_pct: 1, resets_at: 0, source: 'x' }))
    rerender(<HostQuotaSegments hostId="h2" />)
    await act(async () => {})
    expect(container.innerHTML).toBe('')
    mockFetchNexHost.mockRejectedValue(new Error('down'))
    rerender(<HostQuotaSegments hostId="h3" />)
    await act(async () => {})
    expect(container.innerHTML).toBe('')
  })

  it('never shows host A\'s quota under host B while B is pending', async () => {
    mockFetchNexHost.mockResolvedValueOnce(host({ five_hour_pct: 10, seven_day_pct: 20, resets_at: 0, source: 'x' }))
    const { container, rerender } = render(<HostQuotaSegments hostId="A" />)
    await act(async () => {})
    expect(screen.getByTestId('status-seg-quota-five-hour')).toBeTruthy()
    mockFetchNexHost.mockReturnValue(new Promise(() => {}))
    rerender(<HostQuotaSegments hostId="B" />)
    expect(container.innerHTML).toBe('')
  })
})
