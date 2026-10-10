import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import { Clock } from '@phosphor-icons/react'
import { CcUsageSegments, HostQuotaSegments, UsageSegment } from './UsageSegments'
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

  it('shows icon + ring + number (ring: context = used, limits = remaining; number: all remaining), with no text label, and names the limit in tooltip and aria-label', () => {
    seed(payload())
    render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    const ctx = screen.getByTestId('status-seg-usage-context')
    expect(ctx.textContent).toBe('77%')
    expect(ctx.title).toBe('Context window: 77% left (23% used)')
    expect(ctx.getAttribute('aria-label')).toBe(ctx.title)
    const five = screen.getByTestId('status-seg-usage-five-hour')
    expect(five.textContent).toBe('76%')
    expect(five.title).toContain('5-hour limit: 76% left (24% used)')
    expect(five.title).toContain('resets in 2h13m')
    const week = screen.getByTestId('status-seg-usage-seven-day')
    expect(week.textContent).toBe('7%')
    expect(week.title).toContain('Weekly limit: 7% left (93% used)')
    expect(week.title).toContain('resets in 3d0h')
    for (const el of [ctx, five, week]) {
      expect(el.textContent).not.toMatch(/ctx|5h|7d/)
      expect(el.querySelector('svg')).toBeTruthy()
    }
  })

  it('context number shows what is LEFT: used 37 -> 63%, used 15 -> 85%, used 120 -> 0%, used -5 -> 100%', () => {
    for (const [used, shown] of [[37, '63%'], [15, '85%'], [120, '0%'], [-5, '100%']] as const) {
      cleanup()
      seed(payload({ context_window: { used_percentage: used } }))
      render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
      expect(screen.getByTestId('status-seg-usage-context').textContent).toBe(shown)
    }
  })

  it('limits show what is LEFT: used 12 -> 88%, used 61 -> 39%, used 120 -> 0%, used -5 -> 100%', () => {
    for (const [used, shown] of [[12, '88%'], [61, '39%'], [120, '0%'], [-5, '100%']] as const) {
      cleanup()
      seed(payload({ rate_limits: { five_hour: { used_percentage: used }, seven_day: { used_percentage: used } } }))
      render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
      expect(screen.getByTestId('status-seg-usage-five-hour').textContent).toBe(shown)
      expect(screen.getByTestId('status-seg-usage-seven-day').textContent).toBe(shown)
    }
  })

  it('context ring lit = USED (37 -> 37% arc); limit rings lit = REMAINING; tone follows used', () => {
    seed(payload({ context_window: { used_percentage: 37 }, rate_limits: { five_hour: { used_percentage: 12 }, seven_day: { used_percentage: 93 } } }))
    render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    const arcOf = (id: string) => screen.getByTestId(id).querySelector('[data-testid="usage-ring-arc"]')!
    const frac = (a: Element) => { const [on, total] = a.getAttribute('stroke-dasharray')!.split(' ').map(Number); return on / total }
    const ctx = arcOf('status-seg-usage-context')
    expect(ctx.getAttribute('data-shown')).toBe('37')
    expect(frac(ctx)).toBeCloseTo(0.37, 5)
    expect(ctx.getAttribute('data-tone')).toBe('ok')
    expect(ctx.getAttribute('data-direction')).toBe('ccw') // a used ring grows counterclockwise
    const five = arcOf('status-seg-usage-five-hour')
    expect(five.getAttribute('data-used')).toBe('12')
    expect(five.getAttribute('data-shown')).toBe('88')
    expect(frac(five)).toBeCloseTo(0.88, 5)
    const week = arcOf('status-seg-usage-seven-day')
    expect(week.getAttribute('data-shown')).toBe('7')
    expect(frac(week)).toBeCloseTo(0.07, 5)
    expect(week.getAttribute('data-tone')).toBe('danger')
    expect(week.getAttribute('data-direction')).toBe('cw') // remaining: bright part runs clockwise from 12, so it shrinks counterclockwise
    expect(week.getAttribute('class')).toContain('stroke-status-error')
  })

  it('limit ring clamps: used 120 -> 0% lit, used -5 -> 100% lit', () => {
    seed(payload({ rate_limits: { five_hour: { used_percentage: 120 }, seven_day: { used_percentage: -5 } } }))
    render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    const shown = (id: string) => screen.getByTestId(id).querySelector('[data-testid="usage-ring-arc"]')!.getAttribute('data-shown')
    expect(shown('status-seg-usage-five-hour')).toBe('0')
    expect(shown('status-seg-usage-seven-day')).toBe('100')
  })

  it.each([[69, 'ok', 'stroke-status-success'], [70, 'warn', 'stroke-status-warning'], [89, 'warn', 'stroke-status-warning'], [90, 'danger', 'stroke-status-error']])(
    'ring tone at used %i is %s', (used, tone, cls) => {
      seed(payload({ context_window: { used_percentage: used } }))
      render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
      const arc = screen.getByTestId('status-seg-usage-context').querySelector('[data-testid="usage-ring-arc"]')!
      expect(arc.getAttribute('data-tone')).toBe(tone)
      expect(arc.getAttribute('class')).toContain(cls)
    })

  it('omits the limit segments when rate_limits is absent', () => {
    seed({ context_window: { used_percentage: 10 } })
    render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(screen.getByTestId('status-seg-usage-context')).toBeTruthy()
    expect(screen.queryByTestId('status-seg-usage-five-hour')).toBeNull()
    expect(screen.queryByTestId('status-seg-usage-seven-day')).toBeNull()
  })

  it('dims a snapshot older than 10 minutes (opacity-50), and ages into dim while mounted', () => {
    seed(payload(), NOW - 11 * 60_000)
    render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(screen.getByTestId('status-seg-usage-context').dataset.dim).toBe('true')
    expect(screen.getByTestId('status-seg-usage-context').className).toContain('opacity-50')
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

describe('one normalised used value', () => {
  it.each([
    [69.4, 69, 'ok', 31], [69.6, 70, 'warn', 30], [89.6, 90, 'danger', 10], [99.6, 100, 'danger', 0], [120, 100, 'danger', 0], [-5, 0, 'ok', 100],
  ])('used %f -> used %i, %s; context ring shows used, number shows left, tooltip names both (left %i)', (raw, used, tone, left) => {
    seed(payload({ context_window: { used_percentage: raw } }))
    render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    const seg = screen.getByTestId('status-seg-usage-context')
    const arc = seg.querySelector('[data-testid="usage-ring-arc"]')!
    expect(arc.getAttribute('data-used')).toBe(String(used))
    expect(arc.getAttribute('data-tone')).toBe(tone)
    expect(seg.textContent).toBe(`${left}%`)
    expect(arc.getAttribute('data-shown')).toBe(String(used))
    expect(seg.title).toBe(`Context window: ${left}% left (${used}% used)`)
  })

  it('the limit tooltips use the same normalised value', () => {
    seed(payload({ rate_limits: { five_hour: { used_percentage: 120 }, seven_day: { used_percentage: -5 } } }))
    render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(screen.getByTestId('status-seg-usage-five-hour').title).toBe('5-hour limit: 0% left (100% used)')
    expect(screen.getByTestId('status-seg-usage-seven-day').title).toBe('Weekly limit: 100% left (0% used)')
  })
})

describe('accessible names and hiding', () => {
  it('each usage is an img named with the limit, what is left and what is used', () => {
    seed(payload())
    render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(screen.getByRole('img', { name: /Context window: 77% left \(23% used\)/ })).toBeTruthy()
    expect(screen.getByRole('img', { name: /5-hour limit: 76% left \(24% used\)/ })).toBeTruthy()
    expect(screen.getByRole('img', { name: /Weekly limit: 7% left \(93% used\)/ })).toBeTruthy()
  })

  it('the wrapper hides at the breakpoint where all its children are hidden', async () => {
    seed(payload())
    const { unmount } = render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(screen.getByTestId('status-usage').className).toContain('max-[600px]:hidden')
    unmount()
    seed({ rate_limits: { five_hour: { used_percentage: 5 } } })
    const r2 = render(<CcUsageSegments hostId="h1" sessionCode="s1" />)
    expect(screen.getByTestId('status-usage').className).toContain('max-[700px]:hidden')
    r2.unmount()
    mockFetchNexHost.mockResolvedValue({ active_account: 'a', quota: { five_hour_pct: 1, seven_day_pct: 2, resets_at: 0, source: 'x' } } as NexHostInfo)
    render(<HostQuotaSegments hostId="h1" />)
    await act(async () => {})
    expect(screen.getByTestId('status-usage').className).toContain('max-[700px]:hidden')
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
    expect(five.textContent).toBe('59%')
    expect(five.title).toContain('resets in 1h30m')
    const week = screen.getByTestId('status-seg-quota-seven-day')
    expect(week.textContent).toBe('28%')
    // Nexen reports one reset time; it is not claimed for the weekly window.
    expect(week.title).not.toContain('resets in')
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

describe('UsageSegment missing value (status row)', () => {
  const base = { testId: 'seg', icon: Clock, ring: 'remaining' as const, number: 'remaining' as const, title: 'tip', stale: false }
  it('a null used reads the dash, with no ring and no percent sign', () => {
    render(<UsageSegment {...base} used={null} />)
    const seg = screen.getByTestId('seg')
    expect(seg.textContent).toBe('—')
    expect(seg.querySelector('[data-testid="usage-ring-arc"]')).toBeNull()
    expect(seg.title).toBe('tip')
    expect(seg.dataset.missing).toBe('true')
  })
  it('0 and a missing value are different: 0 used is 100% left with a full ring', () => {
    render(<UsageSegment {...base} used={0} />)
    const seg = screen.getByTestId('seg')
    expect(seg.textContent).toBe('100%')
    expect(seg.dataset.missing).toBeUndefined()
    expect(seg.querySelector('[data-testid="usage-ring-arc"]')!.getAttribute('data-shown')).toBe('100')
  })
  it('a present value keeps the ring rules (93 used, ring used -> 93 lit, number 7%)', () => {
    render(<UsageSegment {...base} ring="used" used={93} />)
    const seg = screen.getByTestId('seg')
    expect(seg.textContent).toBe('7%')
    const arc = seg.querySelector('[data-testid="usage-ring-arc"]')!
    expect(arc.getAttribute('data-shown')).toBe('93')
    expect(arc.getAttribute('data-tone')).toBe('danger')
  })
})
