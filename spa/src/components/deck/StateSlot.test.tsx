import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { StateSlot, type SlotState } from './StateSlot'
import { formatElapsed, formatExit } from './status-row-model'

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
      expect(opt(container, k).classList.contains('col-start-1')).toBe(true)
      expect(opt(container, k).classList.contains('row-start-1')).toBe(true)
    }
    const text = slot.textContent!
    for (const w of ['Idle', 'Working', 'Failed', 'Denied', 'exit']) expect(text).toContain(w)
  })

  it.each(STATES)('%s: exactly one option is visible, the others are opacity-0 + inert + aria-hidden', (state) => {
    const { container } = render(<StateSlot state={state} />)
    const visible = STATES.filter((k) => !opt(container, k).classList.contains('opacity-0'))
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

  it('an active running / exit option keeps an invisible sizer of the widest text, so a short clock or exit 2 cannot shrink the slot', () => {
    const { container, rerender } = render(<StateSlot state="running" elapsedMs={42_000} />)
    const sizer = (k: string) => opt(container, k).querySelector('[data-sizer]') as HTMLElement | null
    expect(sizer('running')!.textContent).toContain('88:88')
    expect(sizer('running')!.className).toContain('invisible')
    expect(sizer('running')!.getAttribute('aria-hidden')).toBe('true')
    expect(sizer('idle')).toBeNull()
    rerender(<StateSlot state="exit" exitCode={2} />)
    expect(sizer('exit')!.textContent).toContain('exit 255')
    expect(sizer('running')).toBeNull() // inactive options already carry the widest text themselves
  })

  it('clock is capped at 99:59 (then 99:59+), exit code is 0-255 (then 255+), garbage is ?, and the sizer is the widest of each', () => {
    expect(formatElapsed(99 * 60_000 + 59_000)).toBe('99:59')
    expect(formatElapsed(100 * 60_000)).toBe('99:59+')
    expect(formatElapsed(10 ** 12)).toBe('99:59+')
    expect(formatElapsed(NaN)).toBe('0:00')
    expect(formatElapsed(Infinity)).toBe('99:59+')
    expect([0, 2, 255, 256, 1000, 7.9, -1, NaN, Infinity].map(formatExit)).toEqual(['0', '2', '255', '255+', '255+', '7', '?', '?', '?'])
    const { container, rerender } = render(<StateSlot state="running" elapsedMs={100 * 60_000} />)
    expect(opt(container, 'running').textContent).toContain('99:59+')
    expect(opt(container, 'running').querySelector('[data-sizer]')!.textContent).toContain('88:88+')
    for (const [code, text] of [[256, 'exit 255+'], [-3, 'exit ?'], [4.7, 'exit 4'], [NaN, 'exit ?']] as const) {
      rerender(<StateSlot state="exit" exitCode={code} />)
      expect(opt(container, 'exit').textContent).toContain(text)
      expect(opt(container, 'exit').querySelector('[data-sizer]')!.textContent).toContain('exit 255+')
    }
  })

  it('formats the clock', () => {
    expect(formatElapsed(42_000)).toBe('0:42')
    expect(formatElapsed(725_000)).toBe('12:05')
    expect(formatElapsed(-5)).toBe('0:00')
  })
})
