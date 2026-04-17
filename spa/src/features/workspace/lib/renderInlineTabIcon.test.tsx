import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { Terminal } from '@phosphor-icons/react'
import { renderInlineTabIcon } from './renderInlineTabIcon'

describe('renderInlineTabIcon', () => {
  it("renders icon-only when style='icon'", () => {
    const { container } = render(
      renderInlineTabIcon({
        IconComponent: Terminal,
        agentStatus: 'running',
        tabIndicatorStyle: 'icon',
        isActive: false,
        subagentCount: 0,
      }),
    )
    expect(container.querySelector('svg')).toBeInTheDocument()
    expect(container.querySelector('[data-testid="inline-tab-dot"]')).toBeNull()
  })

  it("renders dot-only when style='dot' and agent is active", () => {
    const { container } = render(
      renderInlineTabIcon({
        IconComponent: Terminal,
        agentStatus: 'running',
        tabIndicatorStyle: 'dot',
        isActive: false,
        subagentCount: 0,
      }),
    )
    expect(container.querySelector('svg')).toBeNull()
    expect(container.querySelector('[data-testid="inline-tab-dot"]')).toBeInTheDocument()
  })

  it("renders icon + dot when style='iconDot'", () => {
    const { container } = render(
      renderInlineTabIcon({
        IconComponent: Terminal,
        agentStatus: 'running',
        tabIndicatorStyle: 'iconDot',
        isActive: false,
        subagentCount: 0,
      }),
    )
    expect(container.querySelector('svg')).toBeInTheDocument()
    expect(container.querySelector('[data-testid="inline-tab-dot"]')).toBeInTheDocument()
  })

  it("renders icon with overlay dot when style='badge'", () => {
    const { container } = render(
      renderInlineTabIcon({
        IconComponent: Terminal,
        agentStatus: 'running',
        tabIndicatorStyle: 'badge',
        isActive: false,
        subagentCount: 0,
      }),
    )
    expect(container.querySelector('svg')).toBeInTheDocument()
    expect(container.querySelector('[data-testid="inline-tab-dot-overlay"]')).toBeInTheDocument()
  })

  it('falls back to icon when agentStatus is undefined regardless of style', () => {
    const { container } = render(
      renderInlineTabIcon({
        IconComponent: Terminal,
        agentStatus: undefined,
        tabIndicatorStyle: 'badge',
        isActive: false,
        subagentCount: 0,
      }),
    )
    expect(container.querySelector('svg')).toBeInTheDocument()
    expect(container.querySelector('[data-testid="inline-tab-dot-overlay"]')).toBeNull()
  })
})
