import { describe, it, expect } from 'vitest'
import { matchesConversationQuery, shortenHome } from './conversation-search'
import type { ConversationRow } from './conversations-api'

const HOME = '/Users/wake'
const row = (o: Partial<ConversationRow> = {}): ConversationRow => ({
  session_id: '0b4a1c2d-1111-2222-3333-444455556666',
  title: 'Fix the Login Flow',
  title_source: 'ai',
  first_prompt: 'please look at the OAuth callback',
  cwd: '/Users/wake/Workspace/purdex',
  cwd_exists: true,
  last_activity_at: 1,
  last_in: 'terminal',
  ...o,
})

describe('shortenHome', () => {
  it('the home directory itself is ~', () => {
    expect(shortenHome('/Users/wake', HOME)).toBe('~')
  })
  it('a path under home starts with ~/', () => {
    expect(shortenHome('/Users/wake/Workspace/purdex', HOME)).toBe('~/Workspace/purdex')
  })
  it('a path outside home is unchanged', () => {
    expect(shortenHome('/tmp/x', HOME)).toBe('/tmp/x')
  })
  it('a sibling that only shares the prefix is unchanged', () => {
    expect(shortenHome('/Users/wakeup/a', HOME)).toBe('/Users/wakeup/a')
  })
  it('a home with a trailing slash shortens the same way', () => {
    expect(shortenHome('/Users/wake/a', '/Users/wake/')).toBe('~/a')
    expect(shortenHome('/Users/wake', '/Users/wake/')).toBe('~')
  })
  it('an unknown or root home shortens nothing', () => {
    expect(shortenHome('/Users/wake/a', '')).toBe('/Users/wake/a')
    expect(shortenHome('/a', '/')).toBe('/a')
  })
})

describe('matchesConversationQuery', () => {
  it('an empty or blank query matches every row', () => {
    expect(matchesConversationQuery(row(), '', HOME)).toBe(true)
    expect(matchesConversationQuery(row(), '   ', HOME)).toBe(true)
  })
  it('matches the title, case-insensitively', () => {
    expect(matchesConversationQuery(row(), 'login FLOW', HOME)).toBe(true)
  })
  it('matches the displayed (~) cwd', () => {
    expect(matchesConversationQuery(row(), '~/workspace/pur', HOME)).toBe(true)
  })
  it('matches the raw cwd', () => {
    expect(matchesConversationQuery(row(), '/users/wake/workspace', HOME)).toBe(true)
  })
  it('matches the first prompt', () => {
    expect(matchesConversationQuery(row(), 'oauth callback', HOME)).toBe(true)
  })
  it('matches the session id', () => {
    expect(matchesConversationQuery(row(), '0B4A1C2D', HOME)).toBe(true)
  })
  it('the query is trimmed', () => {
    expect(matchesConversationQuery(row(), '  login  ', HOME)).toBe(true)
  })
  it('no field holds the query → no match', () => {
    expect(matchesConversationQuery(row(), 'nothing-like-this', HOME)).toBe(false)
  })
  it('a row without cwd or first prompt is searched by what it has', () => {
    const r = row({ cwd: undefined, first_prompt: undefined })
    expect(matchesConversationQuery(r, 'login', HOME)).toBe(true)
    expect(matchesConversationQuery(r, '~', HOME)).toBe(false)
  })
})
