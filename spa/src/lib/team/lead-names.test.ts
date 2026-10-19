// spa/src/lib/team/lead-names.test.ts — the lead name candidates on the wire (name pool v2 spec §5.3): the parser and the payload field.
import { describe, it, expect } from 'vitest'
import { leadNamesOf, leadPayloadOf, type Approval } from './types'

describe('leadNamesOf', () => {
  it('is undefined for anything that is not a list (a daemon without lead names)', () => {
    for (const v of [undefined, null, 'x', 3, {}]) expect(leadNamesOf(v)).toBeUndefined()
  })

  it('keeps well-formed entries, reads an unknown kind as other, and drops the broken ones', () => {
    expect(leadNamesOf([
      { code: '', name: '', kind: 'keep', last_at: 0 },
      { code: 'freya', name: 'Freya', kind: 'former', last_at: 5 },
      { code: 'kenji', name: 'Kenji', kind: 'someday' },
      { code: '', name: 'No code', kind: 'lead' },
      { code: 7, name: 'x', kind: 'lead' },
      'junk',
    ])).toEqual([
      { code: '', name: '', kind: 'keep', last_at: 0 },
      { code: 'freya', name: 'Freya', kind: 'former', last_at: 5 },
      { code: 'kenji', name: 'Kenji', kind: 'other', last_at: 0 },
    ])
  })

  it('the lead payload carries the list only when the daemon sent one', () => {
    const a = (payload: Record<string, unknown>) => ({ origin: { cwd: '/w' }, payload }) as unknown as Approval
    expect(leadPayloadOf(a({})).lead_names).toBeUndefined()
    expect(leadPayloadOf(a({ lead_names: [] })).lead_names).toEqual([])
  })
})
