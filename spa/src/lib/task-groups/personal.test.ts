// spa/src/lib/task-groups/personal.test.ts — the personal panel's target and group (TG-5b).
import { describe, it, expect } from 'vitest'
import { personalTargetOf, personalGroupOf } from './personal'
import type { Tab } from '../../types/tab'

const pane = (over: Record<string, unknown> = {}, agent: unknown = { type: 'cc', sessionId: 'A1' }) => ({
  id: 'p', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'c1', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i', rebuild: agent === null ? undefined : { agent }, ...over },
})
const tab = (id: string, p: unknown, group?: string): Tab => ({ id, layout: { type: 'leaf', pane: p }, ...(group ? { group } : {}) } as unknown as Tab)
const idx = { seatByTabId: new Map(), byKey: new Map() }
const ctx = (members: { host: string; root: string }[], roots: Record<string, string> = {}) => ({
  index: idx as never,
  manual: { order: ['g1'], groups: { g1: { name: 'iOS', color: '#fb923c', members, createdAt: 0 } } },
  rootOf: (h: string, s: string) => roots[`${h}/${s}`] ?? null,
})

describe('personalTargetOf', () => {
  it('is the first pane that recorded an agent session id, ended or not', () => {
    expect(personalTargetOf(tab('t1', pane()))).toEqual({ tabId: 't1', hostId: 'h1', sessionCode: 'c1', agentSessionId: 'A1', agentType: 'cc', ended: false })
    expect(personalTargetOf(tab('t1', pane({ terminated: 'exited' })))?.ended).toBe(true)
  })
  it('a relay (the pane\'s agent session id changes, the pane is not terminated) never reads as ended, before or after', () => {
    const before = personalTargetOf(tab('t1', pane({}, { type: 'cc', sessionId: 'A1' })))
    const after = personalTargetOf(tab('t1', pane({}, { type: 'cc', sessionId: 'A2' })))
    expect([before?.ended, after?.ended]).toEqual([false, false])
    expect(after?.agentSessionId).toBe('A2')
  })
  it('a codex pane counts; a pane with no agent session has none', () => {
    expect(personalTargetOf(tab('t1', pane({}, { type: 'codex', sessionId: 'X' })))?.agentType).toBe('codex')
    expect(personalTargetOf(tab('t1', pane({}, null)))).toBeNull()
    expect(personalTargetOf(tab('t1', { id: 'p', content: { kind: 'settings', scope: 'global' } }))).toBeNull()
  })
})

describe('personalGroupOf', () => {
  const t = tab('t1', pane())
  it('names the manual group the session\'s root is a member of, with the sessions it holds', () => {
    const c = ctx([{ host: 'h1', root: 'R' }], { 'h1/A1': 'R' })
    expect(personalGroupOf(t, c, [], { t1: t })).toEqual({ kind: 'manual', id: 'g1', name: 'iOS', color: '#fb923c', count: 1 })
  })
  it('a root that is not read yet is 未分組 — never guessed (R23)', () => {
    expect(personalGroupOf(t, ctx([{ host: 'h1', root: 'R' }]), [], { t1: t })).toEqual({ kind: 'ungrouped' })
  })
  it('never takes the session\'s own id for its root: a group listing that id does not claim it while the root is unread', () => {
    expect(personalGroupOf(t, ctx([{ host: 'h1', root: 'A1' }]), [], { t1: t })).toEqual({ kind: 'ungrouped' })
  })
})
