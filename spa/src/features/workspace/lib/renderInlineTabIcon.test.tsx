import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { Terminal } from '@phosphor-icons/react'
import { renderInlineTabIcon } from './renderInlineTabIcon'
import type { SubagentRef } from '../../../stores/useAgentStore'

const EMPTY: SubagentRef[] = []

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

describe('renderInlineTabIcon', () => {
  it("renders icon-only when style='icon'", () => {
    const { container } = render(
      renderInlineTabIcon({
        IconComponent: Terminal,
        agentStatus: 'running',
        tabIndicatorStyle: 'icon',
        isActive: false,
        subagentRefs: EMPTY,
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
        subagentRefs: EMPTY,
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
        subagentRefs: EMPTY,
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
        subagentRefs: EMPTY,
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
        subagentRefs: EMPTY,
      }),
    )
    expect(container.querySelector('svg')).toBeInTheDocument()
    expect(container.querySelector('[data-testid="inline-tab-dot-overlay"]')).toBeNull()
  })

  it('dot mode forwards subagentRefs to SubagentDots', () => {
    const refs = [makeRef({ id: 'a', type: 'cc' })]
    const { container } = render(
      renderInlineTabIcon({
        IconComponent: Terminal,
        agentStatus: 'running',
        tabIndicatorStyle: 'dot',
        isActive: false,
        subagentRefs: refs,
      }),
    )
    expect(container.querySelector('[data-testid="subagent-dot"]')).toBeInTheDocument()
  })

  it('iconDot mode forwards subagentRefs to SubagentDots', () => {
    const refs = [makeRef({ id: 'a', type: 'cc' })]
    const { container } = render(
      renderInlineTabIcon({
        IconComponent: Terminal,
        agentStatus: 'running',
        tabIndicatorStyle: 'iconDot',
        isActive: false,
        subagentRefs: refs,
      }),
    )
    expect(container.querySelector('[data-testid="subagent-dot"]')).toBeInTheDocument()
  })

  it('badge mode forwards subagentRefs to SubagentDots', () => {
    const refs = [makeRef({ id: 'a', type: 'cc' })]
    const { container } = render(
      renderInlineTabIcon({
        IconComponent: Terminal,
        agentStatus: 'running',
        tabIndicatorStyle: 'badge',
        isActive: false,
        subagentRefs: refs,
      }),
    )
    expect(container.querySelector('[data-testid="subagent-dot"]')).toBeInTheDocument()
  })

  it('proxy ref renders outlined dot (end-to-end prop flow)', () => {
    const refs = [makeRef({ id: 'p', type: 'codex', is_proxy: true })]
    const { container } = render(
      renderInlineTabIcon({
        IconComponent: Terminal,
        agentStatus: 'running',
        tabIndicatorStyle: 'dot',
        isActive: false,
        subagentRefs: refs,
      }),
    )
    const dot = container.querySelector<HTMLElement>('[data-testid="subagent-dot"]')!
    expect(dot.getAttribute('data-is-proxy')).toBe('true')
    expect(dot.getAttribute('data-subagent-type')).toBe('codex')
  })
})
