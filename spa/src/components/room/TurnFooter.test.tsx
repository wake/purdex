// spa/src/components/room/TurnFooter.test.tsx — spec §7.2, F1–F3.
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import TurnFooter from './TurnFooter'
import type { TurnMeta } from '../../lib/nex/event-reducer'

const timeFormat = new Intl.DateTimeFormat('en-GB', { hour: 'numeric', minute: '2-digit', timeZone: 'UTC' })
const endAt = Date.UTC(2026, 0, 15, 17, 48, 0)

const meta = (patch: Partial<TurnMeta>): TurnMeta => ({ startAt: 0, endAt: null, outcome: null, durationMs: null, ...patch })

describe('TurnFooter', () => {
  it('renders the ok footer text with a title containing the date, and no search-unit attribute', () => {
    render(<TurnFooter meta={meta({ endAt, outcome: 'ok', durationMs: 2_282_000 })} timeFormat={timeFormat} />)
    const el = screen.getByTestId('turn-footer')
    expect(el).toHaveTextContent('✻ Worked for 38m 2s · done 17:48')
    expect(el.getAttribute('title')).toMatch(/2026/)
    expect(el).not.toHaveAttribute('data-search-unit')
  })

  it('uses the error-tone color class for a failed turn', () => {
    render(<TurnFooter meta={meta({ endAt, outcome: 'failed', durationMs: 12_000 })} timeFormat={timeFormat} />)
    const el = screen.getByTestId('turn-footer')
    expect(el).toHaveTextContent('✻ Failed after 12s · 17:48')
    expect(el.className).toContain('text-[var(--wt-footer-error-color)]')
    expect(el.className).not.toContain('text-[var(--wt-footer-color)]')
  })

  it('uses the normal-tone color class for an ok turn', () => {
    render(<TurnFooter meta={meta({ endAt, outcome: 'ok', durationMs: 1000 })} timeFormat={timeFormat} />)
    expect(screen.getByTestId('turn-footer').className).toContain('text-[var(--wt-footer-color)]')
  })

  it('renders nothing for an interrupted turn', () => {
    const { container } = render(<TurnFooter meta={meta({ endAt, outcome: 'interrupted', durationMs: 1000 })} timeFormat={timeFormat} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders nothing while the turn is still live (no endAt)', () => {
    const { container } = render(<TurnFooter meta={meta({ outcome: null })} timeFormat={timeFormat} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('falls back to the default (system) time format when none is injected', () => {
    render(<TurnFooter meta={meta({ endAt, outcome: 'ok', durationMs: 1000 })} />)
    expect(screen.getByTestId('turn-footer')).toBeInTheDocument()
  })
})
