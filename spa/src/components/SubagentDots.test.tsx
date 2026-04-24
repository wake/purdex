import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { SubagentDots } from './SubagentDots'
import type { SubagentRef } from '../stores/useAgentStore'

function makeRef(partial: Partial<SubagentRef>): SubagentRef {
  return {
    id: partial.id ?? 'r1',
    type: partial.type ?? 'cc',
    started_at: partial.started_at ?? 0,
    source_pid: partial.source_pid ?? 0,
    source_start_time: partial.source_start_time ?? '',
    is_proxy: partial.is_proxy,
  }
}

describe('SubagentDots', () => {
  it('renders one dot per ref up to 3', () => {
    const refs = [1, 2, 3].map((i) => makeRef({ id: `r${i}` }))
    const { container } = render(<SubagentDots refs={refs} />)
    expect(container.querySelectorAll('[data-testid="subagent-dot"]').length).toBe(3)
  })

  it('renders 0 dots when refs is empty', () => {
    const { container } = render(<SubagentDots refs={[]} />)
    expect(container.querySelectorAll('[data-testid="subagent-dot"]').length).toBe(0)
  })

  it('colors dots by ref.type — cc blue / codex yellow / opencode orange', () => {
    const refs = [
      makeRef({ id: 'a', type: 'cc' }),
      makeRef({ id: 'b', type: 'codex' }),
      makeRef({ id: 'c', type: 'opencode' }),
    ]
    const { container } = render(<SubagentDots refs={refs} />)
    const dots = Array.from(container.querySelectorAll<HTMLElement>('[data-testid="subagent-dot"]'))
    expect(dots.length).toBe(3)
    // rgb() form — jsdom normalizes hex to rgb.
    expect(dots[0].style.backgroundColor).toBe('rgb(96, 165, 250)') // #60a5fa
    expect(dots[1].style.backgroundColor).toBe('rgb(250, 204, 21)') // #facc15
    expect(dots[2].style.backgroundColor).toBe('rgb(249, 115, 22)') // #f97316
  })

  it('unknown type falls back to cc blue', () => {
    const refs = [makeRef({ id: 'x', type: 'unknown-future-agent' })]
    const { container } = render(<SubagentDots refs={refs} />)
    const dot = container.querySelector<HTMLElement>('[data-testid="subagent-dot"]')!
    expect(dot.style.backgroundColor).toBe('rgb(96, 165, 250)')
  })

  it('proxy ref renders as hollow outline (transparent bg + 1px border in type color)', () => {
    const refs = [makeRef({ id: 'p', type: 'codex', is_proxy: true })]
    const { container } = render(<SubagentDots refs={refs} />)
    const dot = container.querySelector<HTMLElement>('[data-testid="subagent-dot"]')!
    expect(dot.getAttribute('data-is-proxy')).toBe('true')
    expect(dot.style.backgroundColor).toBe('transparent')
    expect(dot.style.border).toBe('1px solid rgb(250, 204, 21)')
  })

  it('non-proxy (is_proxy=false or undefined) renders solid bg, no border', () => {
    const refs = [makeRef({ id: 'n', type: 'codex', is_proxy: false })]
    const { container } = render(<SubagentDots refs={refs} />)
    const dot = container.querySelector<HTMLElement>('[data-testid="subagent-dot"]')!
    expect(dot.getAttribute('data-is-proxy')).toBe('false')
    expect(dot.style.backgroundColor).toBe('rgb(250, 204, 21)')
    expect(dot.style.border).toBe('')
  })
})
