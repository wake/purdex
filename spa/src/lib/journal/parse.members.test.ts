// spa/src/lib/journal/parse.members.test.ts — the strict parse of `GET /api/journal/members` (JV-2, journal-v5 spec §4.3).
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { parseJournalMembers, resetWarned } from './parse'

beforeEach(() => { resetWarned(); vi.restoreAllMocks() })

const row = (over: Record<string, unknown> = {}) => ({
  ref: '_abc123', session_id: 'S-A', title: 'member A',
  task: { number: 12, title: 'wire the login', state: 'in_progress' },
  last: { at: 1_700_000_000_000, kind: 'done', text: 'checks passed' },
  ...over,
})

describe('parseJournalMembers', () => {
  it('reads a member with its task and its last report', () => {
    expect(parseJournalMembers({ members: [row()] })).toEqual([{
      ref: '_abc123', sessionId: 'S-A', title: 'member A',
      task: { number: '12', title: 'wire the login', state: 'in_progress' },
      last: { at: 1_700_000_000_000, kind: 'done', text: 'checks passed' },
    }])
  })
  it('a task number may be a string (a team task) and a pending task is read', () => {
    const [m] = parseJournalMembers({ members: [row({ task: { number: '7bcf08-12', title: 't', state: 'pending' } })] })!
    expect(m.task).toEqual({ number: '7bcf08-12', title: 't', state: 'pending' })
  })
  it('a member with neither a task nor a last report reads with nulls', () => {
    const [m] = parseJournalMembers({ members: [row({ task: null, last: undefined })] })!
    expect(m.task).toBeNull()
    expect(m.last).toBeNull()
  })
  it('ref, title and the last kind are optional', () => {
    const [m] = parseJournalMembers({ members: [{ session_id: 'S-B', last: { at: 5, text: 'x' } }] })!
    expect(m).toMatchObject({ ref: '', title: '', task: null, last: { at: 5, kind: '', text: 'x' } })
  })
  it('an empty list is a list', () => {
    expect(parseJournalMembers({ members: [] })).toEqual([])
  })
  it.each([
    ['no session id', row({ session_id: '' })],
    ['a task state that is not in_progress / pending', row({ task: { number: 1, title: 't', state: 'completed' } })],
    ['a task without a title', row({ task: { number: 1, state: 'pending' } })],
    ['a last report with a text-less shape', row({ last: { at: 5 } })],
    ['a last report at as a string', row({ last: { at: 'yesterday', kind: 'done', text: 'x' } })],
    ['not an object', 'S-A'],
  ])('drops a row with %s, with one warning, and keeps the rest', (_n, bad) => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const out = parseJournalMembers({ members: [bad, row({ session_id: 'S-OK' })] })!
    expect(out.map((m) => m.sessionId)).toEqual(['S-OK'])
    expect(warn).toHaveBeenCalledTimes(1)
  })
  it.each([['not an object', []], ['no members list', {}], ['members not a list', { members: {} }]])('an envelope that is %s is null', (_n, v) => {
    expect(parseJournalMembers(v)).toBeNull()
  })
})
