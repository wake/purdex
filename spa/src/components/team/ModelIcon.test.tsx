import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ContextRing } from './ModelIcon'

describe('ContextRing', () => {
  it.each([[15, 'ok', 'stroke-status-success'], [75, 'warn', 'stroke-status-warning'], [95, 'danger', 'stroke-status-error']])(
    'used %i: counterclockwise, share = used, tone %s', (pct, tone, cls) => {
      render(<ContextRing pct={pct} model="opus" size={22} />)
      const arc = screen.getByTestId('context-ring-arc')
      expect(arc.getAttribute('data-direction')).toBe('ccw')
      expect(arc.getAttribute('data-shown')).toBe(String(pct))
      expect(arc.getAttribute('data-tone')).toBe(tone)
      expect(arc.getAttribute('class')).toContain(cls)
    })
  it('draws no arc without a reading', () => {
    render(<ContextRing pct={undefined} model="opus" />)
    expect(screen.queryByTestId('context-ring-arc')).toBeNull()
  })
})
