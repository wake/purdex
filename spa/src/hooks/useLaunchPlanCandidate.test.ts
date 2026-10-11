// spa/src/hooks/useLaunchPanelGate.test.ts — the shared "launch panel applies" rule (NA-1 follow-up).
import { describe, it, expect } from 'vitest'
import { launchPanelApplies } from './useLaunchPanelGate'

const closed = (reason: 'not_session' | 'terminated' | 'host_hidden' | 'not_agent' | 'no_conversations') => ({ ok: false, reason }) as const

describe('launchPanelApplies', () => {
  it('not_agent on a host serving session.launch.v1 applies', () => {
    expect(launchPanelApplies(closed('not_agent'), true)).toBe(true)
  })
  it('no_conversations on a host serving session.launch.v1 applies', () => {
    expect(launchPanelApplies(closed('no_conversations'), true)).toBe(true)
  })
  it('does not apply when the host does not serve session.launch.v1', () => {
    expect(launchPanelApplies(closed('not_agent'), false)).toBe(false)
    expect(launchPanelApplies(closed('no_conversations'), false)).toBe(false)
  })
  it.each(['terminated', 'host_hidden', 'not_session'] as const)('never applies for %s', (reason) => {
    expect(launchPanelApplies(closed(reason), true)).toBe(false)
  })
  it('an open gate is a conversation, not a launch case', () => {
    expect(launchPanelApplies({ ok: true, reason: null }, true)).toBe(false)
  })
})
