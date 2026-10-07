// spa/src/components/TabIcon.test.tsx — the awaitingApproval flag reaches TabStatusIndicator in every style.
import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { Terminal } from '@phosphor-icons/react'
import { TabIcon } from './TabIcon'
import type { AgentStatus, SubagentRef } from '../stores/useAgentStore'
import type { TabIndicatorStyle } from '../stores/useUISettingsStore'
import { useI18nStore } from '../stores/useI18nStore'

const EMPTY: SubagentRef[] = []

function renderIcon(style: TabIndicatorStyle, opts: { agentStatus?: AgentStatus; isUnread?: boolean; awaitingApproval?: boolean } = {}) {
  return render(
    <TabIcon IconComponent={Terminal} agentStatus={'agentStatus' in opts ? opts.agentStatus : 'waiting'} tabIndicatorStyle={style}
      isActive={false} iconSize={14} subagentRefs={EMPTY} isUnread={opts.isUnread ?? false} awaitingApproval={opts.awaitingApproval} />,
  )
}

// Permission channel PC2, user decision 2026-10-08: 「等待核准」 is the hand at the tab light, not label text.
// Coordinator ruling (PR #1792 review): the overlay ('badge') dot stays put with the hand beside it; the replace
// styles ('dot' / 'iconDot') draw the hand in the dot's slot; unread keeps marking the tab as for any waiting light.
describe('TabIcon — awaiting approval', () => {
  afterEach(() => { act(() => { useI18nStore.getState().setLocale('en') }) })

  it.each(['dot', 'iconDot'] as const)('%s: awaitingApproval reaches the indicator (hand in the dot\'s slot)', (style) => {
    renderIcon(style, { awaitingApproval: true })
    expect(screen.getByTestId('tab-status-awaiting-hand')).toBeTruthy()
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
  })

  it('badge: awaitingApproval reaches the indicator (the dot stays, the hand beside it)', () => {
    renderIcon('badge', { awaitingApproval: true })
    expect(screen.getByTestId('tab-status-awaiting-hand')).toBeTruthy()
    expect(screen.getByTestId('tab-status-indicator')).toBeTruthy()
  })

  // useAgentStore marks a `waiting` agent unread; the hand does not cancel that (a tab that needs an answer is
  // exactly the tab the user has not looked at).
  it.each(['dot', 'iconDot'] as const)('%s + unread: the hand AND the red unread pip', (style) => {
    renderIcon(style, { isUnread: true, awaitingApproval: true })
    expect(screen.getByTestId('tab-status-awaiting-hand')).toBeTruthy()
    expect(screen.getByTestId('tab-unread-pip')).toBeTruthy()
  })

  it.each(['dot', 'iconDot'] as const)('%s + unread: the same pip a plain waiting light gets', (style) => {
    const { unmount } = renderIcon(style, { isUnread: true })
    const plainPip = screen.getByTestId('tab-unread-pip').getAttribute('style')
    unmount()
    renderIcon(style, { isUnread: true, awaitingApproval: true })
    expect(screen.getByTestId('tab-unread-pip').getAttribute('style')).toBe(plainPip)
  })

  it('badge + unread: the dot turns red (as for any unread waiting light) and the hand stays beside it', () => {
    renderIcon('badge', { isUnread: true, awaitingApproval: true })
    expect(screen.getByTestId('tab-status-indicator').style.backgroundColor).toBe('rgb(239, 68, 68)')
    expect(screen.getByTestId('tab-status-awaiting-hand').getAttribute('fill')).toBe('#facc15')
  })

  // First paint after a cold load: the summary already has the pending request, useWorkerAgentProjection (an
  // App-level effect) has not written `agentStatus` yet.
  it.each(['dot', 'iconDot', 'badge'] as const)('%s: awaiting with no agentStatus yet still shows the hand, titled 等待核准', (style) => {
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
    renderIcon(style, { agentStatus: undefined, awaitingApproval: true })
    expect(screen.getByTestId('tab-status-awaiting-hand')).toBeTruthy()
    const light = screen.getByTestId('tab-status-awaiting')
    expect(light).toHaveAttribute('title', '等待核准')
    expect(light).toHaveAttribute('aria-label', '等待核准')
  })

  it('no agentStatus and not awaiting: the plain icon, no light', () => {
    renderIcon('badge', { agentStatus: undefined })
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
    expect(screen.queryByTestId('tab-status-awaiting')).toBeNull()
  })

  it('without the flag a waiting light stays the dot', () => {
    renderIcon('badge')
    expect(screen.getByTestId('tab-status-indicator')).toBeTruthy()
    expect(screen.queryByTestId('tab-status-awaiting')).toBeNull()
  })

  // User decision 2026-10-08: awaiting approval is a must-show exception that overrides the lights-off ('icon') choice.
  it('icon style: an awaiting worker still shows the hand, titled 等待核准', () => {
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
    renderIcon('icon', { awaitingApproval: true })
    expect(screen.getByTestId('tab-status-awaiting-hand')).toBeTruthy()
    const light = screen.getByTestId('tab-status-awaiting')
    expect(light).toHaveAttribute('title', '等待核准')
    expect(light).toHaveAttribute('aria-label', '等待核准')
  })

  it('icon style + awaiting + no agentStatus yet (cold start): the hand', () => {
    renderIcon('icon', { agentStatus: undefined, awaitingApproval: true })
    expect(screen.getByTestId('tab-status-awaiting-hand')).toBeTruthy()
  })

  it.each(['running', 'idle', 'error', 'waiting'] as const)('icon style + %s, not awaiting: nothing', (agentStatus) => {
    renderIcon('icon', { agentStatus, isUnread: true })
    expect(screen.queryByTestId('tab-status-awaiting')).toBeNull()
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
    expect(screen.queryByTestId('tab-unread-pip')).toBeNull()
  })
})
