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
    delegating: partial.delegating,
    delegating_tool_use_ids: partial.delegating_tool_use_ids,
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

  it('colors dots by is_proxy: native (false/undefined) → blue, proxy (true) → orange', () => {
    const refs = [
      makeRef({ id: 'a', type: 'cc', is_proxy: false }),
      makeRef({ id: 'b', type: 'codex', is_proxy: true }),
      makeRef({ id: 'c', type: 'opencode' }),  // is_proxy undefined → native
    ]
    const { container } = render(<SubagentDots refs={refs} />)
    const dots = Array.from(container.querySelectorAll<HTMLElement>('[data-testid="subagent-dot"]'))
    expect(dots.length).toBe(3)
    // rgb() form — jsdom normalizes hex to rgb.
    expect(dots[0].style.backgroundColor).toBe('rgb(96, 165, 250)')   // native blue #60a5fa
    expect(dots[1].style.backgroundColor).toBe('rgb(249, 115, 22)')   // proxy orange #f97316
    expect(dots[2].style.backgroundColor).toBe('rgb(96, 165, 250)')   // native blue (undefined)
  })

  it('color is independent of ref.type — codex non-proxy stays native blue', () => {
    const refs = [makeRef({ id: 'x', type: 'codex', is_proxy: false })]
    const { container } = render(<SubagentDots refs={refs} />)
    const dot = container.querySelector<HTMLElement>('[data-testid="subagent-dot"]')!
    expect(dot.style.backgroundColor).toBe('rgb(96, 165, 250)')
  })

  it('proxy ref renders as solid orange (no border, no transparent bg)', () => {
    const refs = [makeRef({ id: 'p', type: 'codex', is_proxy: true })]
    const { container } = render(<SubagentDots refs={refs} />)
    const dot = container.querySelector<HTMLElement>('[data-testid="subagent-dot"]')!
    expect(dot.getAttribute('data-is-proxy')).toBe('true')
    expect(dot.style.backgroundColor).toBe('rgb(249, 115, 22)')
    expect(dot.style.border).toBe('')
  })

  it('non-proxy ref renders as solid blue (no border)', () => {
    const refs = [makeRef({ id: 'n', type: 'codex', is_proxy: false })]
    const { container } = render(<SubagentDots refs={refs} />)
    const dot = container.querySelector<HTMLElement>('[data-testid="subagent-dot"]')!
    expect(dot.getAttribute('data-is-proxy')).toBe('false')
    expect(dot.style.backgroundColor).toBe('rgb(96, 165, 250)')
    expect(dot.style.border).toBe('')
  })

  // Phase 4 (issue #821): delegating flag composes with is_proxy via OR to
  // render orange. Both signals indicate "agent is awaiting downstream"; they
  // coexist without interference. Spec §3.6 / §6.4.

  it('delegating=true, is_proxy=false → orange + data-delegating="true"', () => {
    const refs = [makeRef({ id: 'd1', type: 'cc', is_proxy: false, delegating: true })]
    const { container } = render(<SubagentDots refs={refs} />)
    const dot = container.querySelector<HTMLElement>('[data-testid="subagent-dot"]')!
    expect(dot.style.backgroundColor).toBe('rgb(249, 115, 22)')
    expect(dot.getAttribute('data-delegating')).toBe('true')
    expect(dot.getAttribute('data-is-proxy')).toBe('false')
  })

  it('delegating=false, is_proxy=true → orange + data-delegating="false"', () => {
    const refs = [makeRef({ id: 'd2', type: 'codex', is_proxy: true, delegating: false })]
    const { container } = render(<SubagentDots refs={refs} />)
    const dot = container.querySelector<HTMLElement>('[data-testid="subagent-dot"]')!
    expect(dot.style.backgroundColor).toBe('rgb(249, 115, 22)')
    expect(dot.getAttribute('data-delegating')).toBe('false')
    expect(dot.getAttribute('data-is-proxy')).toBe('true')
  })

  it('delegating=true, is_proxy=true → orange + both attrs true (OR composition)', () => {
    const refs = [makeRef({ id: 'd3', type: 'codex', is_proxy: true, delegating: true })]
    const { container } = render(<SubagentDots refs={refs} />)
    const dot = container.querySelector<HTMLElement>('[data-testid="subagent-dot"]')!
    expect(dot.style.backgroundColor).toBe('rgb(249, 115, 22)')
    expect(dot.getAttribute('data-delegating')).toBe('true')
    expect(dot.getAttribute('data-is-proxy')).toBe('true')
  })

  it('delegating=false, is_proxy=false → blue + both attrs false', () => {
    const refs = [makeRef({ id: 'd4', type: 'cc', is_proxy: false, delegating: false })]
    const { container } = render(<SubagentDots refs={refs} />)
    const dot = container.querySelector<HTMLElement>('[data-testid="subagent-dot"]')!
    expect(dot.style.backgroundColor).toBe('rgb(96, 165, 250)')
    expect(dot.getAttribute('data-delegating')).toBe('false')
    expect(dot.getAttribute('data-is-proxy')).toBe('false')
  })
})
