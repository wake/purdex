// spa/src/components/TabIcon.test.tsx — the awaitingApproval flag reaches TabStatusIndicator in every style.
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Terminal } from '@phosphor-icons/react'
import { TabIcon } from './TabIcon'
import type { SubagentRef } from '../stores/useAgentStore'

const EMPTY: SubagentRef[] = []

// Permission channel PC2, user decision 2026-10-08: 「等待核准」 is the hand on the tab light, not label text.
describe('TabIcon — awaiting approval', () => {
  it.each(['dot', 'iconDot', 'badge'] as const)('%s: awaitingApproval reaches the indicator (hand, not dot)', (style) => {
    render(
      <TabIcon IconComponent={Terminal} agentStatus="waiting" tabIndicatorStyle={style} isActive={false}
        iconSize={14} subagentRefs={EMPTY} isUnread={false} awaitingApproval />,
    )
    expect(screen.getByTestId('tab-status-awaiting')).toBeTruthy()
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
  })

  it.each(['dot', 'iconDot'] as const)('%s + unread: no red pip on the hand', (style) => {
    render(
      <TabIcon IconComponent={Terminal} agentStatus="waiting" tabIndicatorStyle={style} isActive={false}
        iconSize={14} subagentRefs={EMPTY} isUnread awaitingApproval />,
    )
    expect(screen.getByTestId('tab-status-awaiting')).toBeTruthy()
    expect(screen.queryByTestId('tab-unread-pip')).toBeNull()
  })

  it('without the flag a waiting light stays the dot', () => {
    render(
      <TabIcon IconComponent={Terminal} agentStatus="waiting" tabIndicatorStyle="badge" isActive={false}
        iconSize={14} subagentRefs={EMPTY} isUnread={false} />,
    )
    expect(screen.getByTestId('tab-status-indicator')).toBeTruthy()
    expect(screen.queryByTestId('tab-status-awaiting')).toBeNull()
  })

  it('icon style shows no light at all, awaiting or not (the user turned indicators off)', () => {
    render(
      <TabIcon IconComponent={Terminal} agentStatus="waiting" tabIndicatorStyle="icon" isActive={false}
        iconSize={14} subagentRefs={EMPTY} isUnread={false} awaitingApproval />,
    )
    expect(screen.queryByTestId('tab-status-awaiting')).toBeNull()
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
  })
})
