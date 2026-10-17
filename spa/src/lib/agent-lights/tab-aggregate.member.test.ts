// spa/src/lib/agent-lights/tab-aggregate.member.test.ts — member spec R3: a tab is unread when any NON-member pane is;
// the light's status is not touched by membership.
import { describe, expect, it } from 'vitest'
import { aggregateTabAgents, type TabAgentData, type TabAgentPane } from './tab-aggregate'

const data = (over: Partial<TabAgentData> = {}): TabAgentData => ({ statuses: {}, unread: {}, agentTypes: {}, lastEvents: {}, ...over })
const panes = (...keys: string[]): TabAgentPane[] => keys.map((key) => ({ key, hostId: 'h1' }))
const none = new Set<string>()

describe('aggregateTabAgents — team members (R3)', () => {
  it('a member pane\'s unread does not make the tab unread', () => {
    const a = aggregateTabAgents(panes('h1:a'), data({ unread: { 'h1:a': true } }), none, new Set(['h1:a']))
    expect(a.isUnread).toBe(false)
  })

  it('a non-member pane of the same tab still does', () => {
    const a = aggregateTabAgents(panes('h1:a', 'h1:b'), data({ unread: { 'h1:a': true, 'h1:b': true } }), none, new Set(['h1:a']))
    expect(a.isUnread).toBe(true)
  })

  it('without a member set the tab is unread as before', () => {
    expect(aggregateTabAgents(panes('h1:a'), data({ unread: { 'h1:a': true } })).isUnread).toBe(true)
  })

  it('status and representative pane are untouched by membership', () => {
    const d = data({ statuses: { 'h1:a': 'waiting', 'h1:b': 'running' }, unread: { 'h1:a': true } })
    const a = aggregateTabAgents(panes('h1:a', 'h1:b'), d, none, new Set(['h1:a']))
    expect(a.status).toBe('waiting')
    expect(a.repKey).toBe('h1:a')
    expect(a.isUnread).toBe(false)
  })

  it('the approval hand is untouched by membership', () => {
    const a = aggregateTabAgents(panes('h1:a'), data(), new Set(['h1:a']), new Set(['h1:a']))
    expect(a.isAwaitingApproval).toBe(true)
    expect(a.status).toBe('waiting')
  })
})
