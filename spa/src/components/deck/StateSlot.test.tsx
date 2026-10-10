import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { StateSlot, formatElapsed, type SlotState } from './StateSlot'

const STATES: SlotState[] = ['idle', 'running', 'failed', 'denied', 'exit']
const opt = (container: HTMLElement, k: string) => container.querySelector(`[data-state-option="${k}"]`) as HTMLElement

afterEach(cleanup)

describe('StateSlot', () => {
  it('keeps every state word in the DOM, stacked in one grid cell', () => {
    const { container } = render(<StateSlot state="idle" />)
    const slot = screen.getByTestId('state-slot')
    expect(slot.className).toContain('inline-grid')
    expect(slot.className).not.toMatch(/\bw-\d/)
    for (const k of STATES) {
      expect(opt(container, k).className).toContain('col-start-1')
      expect(opt(container, k).className).toContain('row-start-1')
    }
    const text = slot.textContent!
    for (const w of ['Idle', 'Working', 'Failed', 'Denied', 'exit']) expect(text).toContain(w)
  })

  it.each(STATES)('%s: exactly one option is visible, the others are opacity-0 + inert + aria-hidden', (state) => {
    const { container } = render(<StateSlot state={state} />)
    const visible = STATES.filter((k) => !opt(container, k).className.includes('opacity-0'))
    expect(visible).toEqual([state])
    for (const k of STATES.filter((x) => x !== state)) {
      const el = opt(container, k)
      expect(el.hasAttribute('inert')).toBe(true)
      expect(el.getAttribute('aria-hidden')).toBe('true')
    }
    expect(opt(container, state).hasAttribute('inert')).toBe(false)
    expect(opt(container, state).getAttribute('aria-hidden')).toBeNull()
  })

  it('shows the running clock and the real exit code only when active; reserves the widest text otherwise', () => {
    const { container, rerender } = render(<StateSlot state="running" elapsedMs={42_000} />)
    expect(opt(container, 'running').textContent).toContain('0:42')
    expect(opt(container, 'exit').textContent).toContain('exit 255')
    rerender(<StateSlot state="exit" exitCode={2} />)
    expect(opt(container, 'exit').textContent).toContain('exit 2')
    expect(opt(container, 'running').textContent).toContain('88:88')
  })

  it('formats the clock', () => {
    expect(formatElapsed(42_000)).toBe('0:42')
    expect(formatElapsed(725_000)).toBe('12:05')
    expect(formatElapsed(-5)).toBe('0:00')
  })
})
