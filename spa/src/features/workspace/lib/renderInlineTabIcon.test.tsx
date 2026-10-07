import { describe, it, expect, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import { Terminal } from '@phosphor-icons/react'
import { renderInlineTabIcon } from './renderInlineTabIcon'
import type { AgentStatus, SubagentRef } from '../../../stores/useAgentStore'
import type { TabIndicatorStyle } from '../../../stores/useUISettingsStore'
import { useI18nStore } from '../../../stores/useI18nStore'

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

  // Permission channel PC2, user decision 2026-10-08: 「等待核准」 is the hand at the tab light, in every indicator
  // style. Coordinator ruling (PR #1792 review): the overlay ('badge') dot stays put with the hand beside it; the
  // replace styles draw the hand in the dot's slot; unread keeps marking the tab as for any waiting light.
  describe('awaiting approval', () => {
    afterEach(() => { act(() => { useI18nStore.getState().setLocale('en') }) })

    const renderAwaiting = (style: TabIndicatorStyle, opts: { agentStatus?: AgentStatus; isUnread?: boolean; awaitingApproval?: boolean } = {}) =>
      render(
        renderInlineTabIcon({
          IconComponent: Terminal,
          agentStatus: 'agentStatus' in opts ? opts.agentStatus : 'waiting',
          tabIndicatorStyle: style,
          isActive: false,
          subagentRefs: EMPTY,
          isUnread: opts.isUnread,
          awaitingApproval: opts.awaitingApproval ?? true,
        }),
      )
    const q = (c: HTMLElement, id: string) => c.querySelector<HTMLElement>(`[data-testid="${id}"]`)

    it.each(['dot', 'iconDot'] as const)('%s: awaitingApproval reaches the indicator (hand in the dot\'s slot)', (style) => {
      const { container } = renderAwaiting(style)
      expect(q(container, 'tab-status-awaiting-hand')).toBeInTheDocument()
      expect(q(container, 'tab-status-indicator')).toBeNull()
    })

    it('badge: awaitingApproval reaches the indicator (the dot stays, the hand beside it)', () => {
      const { container } = renderAwaiting('badge')
      expect(q(container, 'tab-status-awaiting-hand')).toBeInTheDocument()
      expect(q(container, 'tab-status-indicator')).toBeInTheDocument()
    })

    it.each(['dot', 'iconDot'] as const)('%s + unread: the hand AND the red unread pip (same pip as a plain waiting light)', (style) => {
      const plain = renderAwaiting(style, { isUnread: true, awaitingApproval: false })
      const plainPip = q(plain.container, 'inline-tab-unread-pip')!.getAttribute('style')
      plain.unmount()
      const { container } = renderAwaiting(style, { isUnread: true })
      expect(q(container, 'tab-status-awaiting-hand')).toBeInTheDocument()
      expect(q(container, 'inline-tab-unread-pip')!.getAttribute('style')).toBe(plainPip)
    })

    it('badge + unread: the dot turns red (as for any unread waiting light) and the hand stays beside it', () => {
      const { container } = renderAwaiting('badge', { isUnread: true })
      expect(q(container, 'tab-status-indicator')!.style.backgroundColor).toBe('rgb(239, 68, 68)')
      expect(q(container, 'tab-status-awaiting-hand')!.getAttribute('fill')).toBe('#facc15')
    })

    // First paint after a cold load: the summary already has the pending request, useWorkerAgentProjection (an
    // App-level effect) has not written `agentStatus` yet.
    it.each(['dot', 'iconDot', 'badge'] as const)('%s: awaiting with no agentStatus yet still shows the hand, titled 等待核准', (style) => {
      act(() => { useI18nStore.getState().setLocale('zh-TW') })
      const { container } = renderAwaiting(style, { agentStatus: undefined })
      expect(q(container, 'tab-status-awaiting-hand')).toBeInTheDocument()
      const light = q(container, 'tab-status-awaiting')!
      expect(light).toHaveAttribute('title', '等待核准')
      expect(light).toHaveAttribute('aria-label', '等待核准')
    })

    it('icon style shows no light at all, awaiting or not (the user turned indicators off)', () => {
      const { container } = renderAwaiting('icon')
      expect(q(container, 'tab-status-awaiting')).toBeNull()
      expect(q(container, 'tab-status-indicator')).toBeNull()
    })

    it('without the flag a waiting light stays the dot', () => {
      const { container } = render(
        renderInlineTabIcon({
          IconComponent: Terminal,
          agentStatus: 'waiting',
          tabIndicatorStyle: 'badge',
          isActive: false,
          subagentRefs: EMPTY,
        }),
      )
      expect(container.querySelector('[data-testid="tab-status-indicator"]')).toBeInTheDocument()
      expect(container.querySelector('[data-testid="tab-status-awaiting"]')).toBeNull()
    })
  })
})
