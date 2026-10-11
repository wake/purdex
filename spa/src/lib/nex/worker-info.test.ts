import { describe, it, expect } from 'vitest'
import { profileText, workerInfoText } from './worker-info'
import type { ExecutionSummary } from './types'

const t = (k: string) => ({ 'room.info.provider': 'Provider', 'room.info.profile': 'Profile', 'room.info.cwd': 'Directory', 'room.info.session': 'Session', 'room.info.archived': 'Archived' })[k] ?? k
const base = { id: 'e', provider: 'claude', cwd: '/w/purdex', effective_profile: 'standard', archived: false, session_id: 'sid-1' } as unknown as ExecutionSummary

describe('workerInfoText', () => {
  it('says provider, profile, the full cwd and the session, a fact per line', () => {
    expect(workerInfoText(base, t)).toBe('Provider: claude\nProfile: standard\nDirectory: /w/purdex\nSession: sid-1')
  })
  it('reads the session it resumes when it has none of its own, and says when archived', () => {
    const s = { ...base, session_id: undefined, resume_session_id: 'sid-0', archived: true } as unknown as ExecutionSummary
    expect(workerInfoText(s, t)).toBe('Provider: claude\nProfile: standard\nDirectory: /w/purdex\nSession: sid-0\nArchived')
  })
  it('no session yet: no session line; a changed profile shows both', () => {
    const s = { ...base, session_id: undefined, requested_profile: 'strict' } as unknown as ExecutionSummary
    expect(workerInfoText(s, t)).not.toContain('Session')
    expect(profileText(s)).toBe('strict → standard')
  })
})
