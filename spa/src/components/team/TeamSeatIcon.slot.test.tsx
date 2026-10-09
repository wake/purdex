// The one-line cell's subagent slot (WA-2a′ item 6): a fixed-width room left of the icon, reserved with or without subagents,
// so a cell's width never depends on them; the dots themselves are drawn only when there are subagents.
import { describe, it, expect, beforeEach } from 'vitest'
import { render } from '@testing-library/react'
import { TeamSeatIcon } from './TeamSeatIcon'
import { SUBAGENT_SLOT_W } from './panel-layout'
import { useAgentStore } from '../../stores/useAgentStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { compositeKey } from '../../lib/composite-key'

const ck = compositeKey('h1', 's1')
const ref = (id: string) => ({ id, type: 'x', started_at: 0, source_pid: 0, source_start_time: '' })

describe('TeamSeatIcon subagentSlot', () => {
  beforeEach(() => {
    useAgentStore.setState({ statuses: { [ck]: 'running' }, agentTypes: { [ck]: 'cc' }, subagents: {} } as never)
    useUISettingsStore.setState({ tabIndicatorStyle: 'badge' } as never)
  })

  const slotOf = (c: HTMLElement) => c.querySelector<HTMLElement>('[data-testid="seat-subagent-slot"]')

  it('is the same fixed-width box whether the seat has subagents or not (and empty of dots without)', () => {
    const none = render(<TeamSeatIcon hostId="h1" sessionCode="s1" size={12} compact subagents subagentSlot />)
    expect(slotOf(none.container)?.style.width).toBe(`${SUBAGENT_SLOT_W}px`)
    expect(none.container.querySelectorAll('[data-testid="subagent-dot"]')).toHaveLength(0)
    none.unmount()
    useAgentStore.setState({ subagents: { [ck]: [ref('a'), ref('b')] } } as never)
    const some = render(<TeamSeatIcon hostId="h1" sessionCode="s1" size={12} compact subagents subagentSlot />)
    expect(slotOf(some.container)?.style.width).toBe(`${SUBAGENT_SLOT_W}px`)
    expect(some.container.querySelectorAll('[data-testid="subagent-dot"]')).toHaveLength(2)
  })

  it('is reserved for a seat with no agent light at all (an unknown seat), and absent when not asked for', () => {
    useAgentStore.setState({ statuses: {}, agentTypes: {} } as never)
    const a = render(<TeamSeatIcon hostId="h1" sessionCode="s1" size={12} compact subagents subagentSlot />)
    expect(slotOf(a.container)?.style.width).toBe(`${SUBAGENT_SLOT_W}px`)
    a.unmount()
    const b = render(<TeamSeatIcon hostId="h1" sessionCode="s1" size={12} compact />)
    expect(slotOf(b.container)).toBeNull()
  })
})
