import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import WorkingLine, { WORKING_FRAME_MS } from './WorkingLine'
import { pickWorkingVerb, WORKING_FRAMES } from '../../lib/nex/working-line'

const T0 = 1_700_000_000_000

function mockReducedMotion(reduce: boolean) {
  window.matchMedia = vi.fn().mockImplementation((q: string) => ({
    matches: reduce && q.includes('reduce'), media: q, addEventListener: vi.fn(), removeEventListener: vi.fn(),
  })) as never
}

describe('WorkingLine', () => {
  beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(T0); mockReducedMotion(false) })
  afterEach(() => { vi.useRealTimers(); delete (window as { matchMedia?: unknown }).matchMedia })

  it('draws nothing when not visible', () => {
    render(<WorkingLine visible={false} turnKey="k" />)
    expect(screen.queryByTestId('thinking-indicator')).toBeNull()
  })

  it('shows glyph, a per-turn verb with an ellipsis, and the elapsed time since startedAt', () => {
    render(<WorkingLine visible turnKey="exc:2" startedAt={T0 - 100_000} />)
    expect(screen.getByTestId('thinking-indicator').dataset.mode).toBe('room')
    expect(screen.getByTestId('working-verb')).toHaveTextContent(`${pickWorkingVerb('exc:2')}…`)
    expect(screen.getByTestId('working-elapsed')).toHaveTextContent('(1m 40s)')
    // The text-line form, not chat's dots.
    expect(screen.getByTestId('thinking-indicator').querySelector('.animate-pulse')).toBeNull()
  })

  it('the elapsed time advances each second', () => {
    render(<WorkingLine visible turnKey="k" startedAt={T0} />)
    expect(screen.getByTestId('working-elapsed')).toHaveTextContent('(0s)')
    act(() => { vi.advanceTimersByTime(3_000) })
    expect(screen.getByTestId('working-elapsed')).toHaveTextContent('(3s)')
  })

  it('without a start it counts from when the line appeared', () => {
    render(<WorkingLine visible turnKey="k" />)
    act(() => { vi.advanceTimersByTime(5_000) })
    expect(screen.getByTestId('working-elapsed')).toHaveTextContent('(5s)')
  })

  it('keeps its verb across re-renders and ticks', () => {
    const { rerender } = render(<WorkingLine visible turnKey="exc:7" startedAt={T0} />)
    const verb = screen.getByTestId('working-verb').textContent
    act(() => { vi.advanceTimersByTime(10_000) })
    rerender(<WorkingLine visible turnKey="exc:7" startedAt={T0} />)
    expect(screen.getByTestId('working-verb').textContent).toBe(verb)
  })

  it('cycles the glyph', () => {
    render(<WorkingLine visible turnKey="k" startedAt={T0} />)
    const seen = new Set<string>()
    for (let i = 0; i < WORKING_FRAMES.length; i++) {
      seen.add(screen.getByTestId('working-glyph').textContent ?? '')
      act(() => { vi.advanceTimersByTime(WORKING_FRAME_MS) })
    }
    expect(seen.size).toBe(WORKING_FRAMES.length)
  })

  it('holds the glyph still under prefers-reduced-motion', () => {
    mockReducedMotion(true)
    render(<WorkingLine visible turnKey="k" startedAt={T0} />)
    expect(screen.getByTestId('working-glyph').textContent).toBe('✻')
    act(() => { vi.advanceTimersByTime(WORKING_FRAME_MS * 4) })
    expect(screen.getByTestId('working-glyph').textContent).toBe('✻')
  })
})
