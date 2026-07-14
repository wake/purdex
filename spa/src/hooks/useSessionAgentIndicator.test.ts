import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useSessionAgentIndicator } from './useSessionAgentIndicator'
import { useAgentStore } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { compositeKey } from '../lib/composite-key'
import type { SubagentRef } from '../stores/useAgentStore'

const ref: SubagentRef = { id: 'a', type: 'cc', started_at: 0, source_pid: 0, source_start_time: '' }

beforeEach(() => {
  useAgentStore.setState({ statuses: {}, agentTypes: {}, subagents: {}, unread: {} })
  useUISettingsStore.setState({ tabIndicatorStyle: 'iconDot', ccIconVariant: 'bot', codexIconVariant: 'openai' })
})

describe('useSessionAgentIndicator', () => {
  it('returns empty indicator when sessionCode is undefined', () => {
    const { result } = renderHook(() => useSessionAgentIndicator('h1', undefined))
    expect(result.current.agentIcon).toBeUndefined()
    expect(result.current.agentStatus).toBeUndefined()
    expect(result.current.subagentRefs).toEqual([])
    expect(result.current.isUnread).toBe(false)
    expect(result.current.tabIndicatorStyle).toBe('iconDot')
  })

  it('resolves agent icon + status + refs + unread for a live session', () => {
    const ck = compositeKey('h1', 's1')
    useAgentStore.setState({
      statuses: { [ck]: 'running' },
      agentTypes: { [ck]: 'cc' }, // getAgentIcon only knows 'cc' | 'codex' | 'opencode'
      subagents: { [ck]: [ref] },
      unread: { [ck]: true },
    })
    const { result } = renderHook(() => useSessionAgentIndicator('h1', 's1'))
    expect(result.current.agentStatus).toBe('running')
    expect(result.current.agentIcon).toBeTypeOf('function') // getAgentIcon('cc', { ccVariant:'bot', ... }) resolved a component
    expect(result.current.subagentRefs).toHaveLength(1)
    expect(result.current.isUnread).toBe(true)
  })

  it('suppresses the agent icon when terminated', () => {
    const ck = compositeKey('h1', 's1')
    useAgentStore.setState({ agentTypes: { [ck]: 'cc' } })
    const { result } = renderHook(() => useSessionAgentIndicator('h1', 's1', { isTerminated: true }))
    expect(result.current.agentIcon).toBeUndefined()
  })
})
