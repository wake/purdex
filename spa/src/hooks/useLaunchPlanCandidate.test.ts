// spa/src/hooks/useLaunchPlanCandidate.test.ts — the shared "launch plan candidate" rule (NA-1 follow-up).
import { describe, it, expect } from 'vitest'
import { launchPlanCandidate } from './useLaunchPlanCandidate'

const closed = (reason: 'not_session' | 'terminated' | 'host_hidden' | 'not_agent' | 'no_conversations') => ({ ok: false, reason }) as const

describe('launchPlanCandidate', () => {
  it('not_agent on a host serving session.launch.v1 is a candidate', () => {
    expect(launchPlanCandidate(closed('not_agent'), true)).toBe(true)
  })
  it('no_conversations on a host serving session.launch.v1 is a candidate', () => {
    expect(launchPlanCandidate(closed('no_conversations'), true)).toBe(true)
  })
  it('is not a candidate when the host does not serve session.launch.v1', () => {
    expect(launchPlanCandidate(closed('not_agent'), false)).toBe(false)
    expect(launchPlanCandidate(closed('no_conversations'), false)).toBe(false)
  })
  it.each(['terminated', 'host_hidden', 'not_session'] as const)('is never a candidate for %s', (reason) => {
    expect(launchPlanCandidate(closed(reason), true)).toBe(false)
  })
  it('an open gate is a conversation, not a launch case', () => {
    expect(launchPlanCandidate({ ok: true, reason: null }, true)).toBe(false)
  })
})
