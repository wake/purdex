// spa/src/lib/notify/group-labels.test.ts — the projection of group names per host (notify title spec §3.2 (a)(b)).
import { describe, it, expect } from 'vitest'
import { projectGroupLabels } from './group-labels'
import { manualGroupKey, teamGroupKey } from '../task-groups/group-key'
import type { TeamView } from '../team/team-views'
import type { Tab } from '../../types/tab'
import type { ManualGroups } from '../../stores/useManualGroupsStore'

const tabOf = (id: string, hostId: string, agent: string | null, group?: string): Tab => ({
  id, createdAt: 1, ...(group ? { group } : {}),
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId, sessionCode: id, mode: 'terminal', cachedName: id, tmuxInstance: '', ...(agent ? { rebuild: { agent: { sessionId: agent } } } : {}) } } },
}) as never
const manual = { order: ['g1', 'g2'], groups: {
  g1: { name: 'Alpha', members: [{ host: 'h1', root: 'R1' }, { host: 'h2', root: 'R2' }], createdAt: 1 },
  g2: { name: 'Beta', members: [{ host: 'h1', root: 'R1' }, { host: 'h1', root: 'R3' }], createdAt: 2 },
} }
const teamView = (name: string, lead = 'lead-title', seats: string[] = []): TeamView => ({
  key: 'h1\u0000T1', hostId: 'h1', teamId: 'T1', createdAt: 1, name, label: '', colorIndex: 0,
  lead: { label: lead, hostId: 'h1', session: { session_id: 'LEAD' } } as never,
  members: seats.map((id) => ({ hostId: 'h1', session: { session_id: id } }) as never),
})
const roots: Record<string, string> = {}
const rootOf = (h: string, s: string) => roots[`${h}:${s}`] ?? null
const run = (hostId: string, tabs: Tab[], views: TeamView[] = [], m: ManualGroups = manual) =>
  projectGroupLabels({ hostId, manual: m, tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), views, rootOf })

describe('(a) manual group members', () => {
  it('writes every member of this host, first group in order wins, other hosts left out', () => {
    expect(run('h1', []).labels).toEqual([{ root_session_id: 'R1', group: 'Alpha' }, { root_session_id: 'R3', group: 'Beta' }])
    expect(run('h2', []).labels).toEqual([{ root_session_id: 'R2', group: 'Alpha' }])
  })
  it('no groups → an empty set (the writer sends it to clear the daemon)', () => {
    expect(run('h1', [], [], { order: [], groups: {} }).labels).toEqual([])
  })
})

describe('(b) tab.group', () => {
  it('a manual group key → the group name, through the cached root', () => {
    roots['h1:S9'] = 'R9'
    const p = run('h1', [tabOf('t1', 'h1', 'S9', manualGroupKey('g2'))])
    expect(p.labels).toContainEqual({ root_session_id: 'R9', group: 'Beta' })
    expect(p.wanted).toEqual([{ hostId: 'h1', agentSessionId: 'S9' }])
  })
  it('a team key whose tab holds no seat → the team name; an unnamed team → the lead label', () => {
    roots['h1:S8'] = 'R8'
    const tabs = [tabOf('t1', 'h1', 'S8', teamGroupKey('h1\u0000T1'))]
    expect(run('h1', tabs, [teamView('Crew')]).labels).toContainEqual({ root_session_id: 'R8', group: 'Crew' })
    expect(run('h1', tabs, [teamView('')]).labels).toContainEqual({ root_session_id: 'R8', group: 'lead-title' })
  })
  it('a session with a team seat is not written (the daemon names the team)', () => {
    roots['h1:S7'] = 'R7'
    expect(run('h1', [tabOf('t1', 'h1', 'S7', teamGroupKey('h1\u0000T1'))], [teamView('Crew', 'x', ['S7'])]).labels.find((l) => l.root_session_id === 'R7')).toBeUndefined()
  })
  it('a seat on another host does not hide the same session id here (codex R1)', () => {
    roots['h2:S7'] = 'R7'
    const p = run('h2', [tabOf('t1', 'h2', 'S7', manualGroupKey('g1'))], [teamView('Crew', 'x', ['S7'])])
    expect(p.labels).toContainEqual({ root_session_id: 'R7', group: 'Alpha' })
  })
  it('a seat on a host this Mac does not know hides nothing (codex R2)', () => {
    roots['h1:S7'] = 'R7'
    const v = teamView('Crew', 'x', ['S7'])
    ;(v.members[0] as { hostId: string | null }).hostId = null
    expect(run('h1', [tabOf('t1', 'h1', 'S7', manualGroupKey('g1'))], [v]).labels).toContainEqual({ root_session_id: 'R7', group: 'Alpha' })
  })
  it('a group that no longer exists is no placement', () => {
    roots['h1:S6'] = 'R6'
    expect(run('h1', [tabOf('t1', 'h1', 'S6', manualGroupKey('gone'))]).labels.find((l) => l.root_session_id === 'R6')).toBeUndefined()
    expect(run('h1', [tabOf('t1', 'h1', 'S6', teamGroupKey('h1\u0000T1'))], []).labels.find((l) => l.root_session_id === 'R6')).toBeUndefined()
  })
  it('an unknown root is deferred: not written, still wanted', () => {
    const p = run('h1', [tabOf('t1', 'h1', 'NOROOT', manualGroupKey('g1'))])
    expect(p.labels).toEqual([{ root_session_id: 'R1', group: 'Alpha' }, { root_session_id: 'R3', group: 'Beta' }])
    expect(p.wanted).toEqual([{ hostId: 'h1', agentSessionId: 'NOROOT' }])
  })
  it('(a) beats (b) for the same root; a tab of another host or without an agent session is skipped', () => {
    roots['h1:S5'] = 'R1'
    roots['h2:S4'] = 'R4'
    const p = run('h1', [tabOf('t1', 'h1', 'S5', manualGroupKey('g2')), tabOf('t2', 'h2', 'S4', manualGroupKey('g2')), tabOf('t3', 'h1', null, manualGroupKey('g2'))])
    expect(p.labels).toEqual([{ root_session_id: 'R1', group: 'Alpha' }, { root_session_id: 'R3', group: 'Beta' }])
  })
  it('two tabs claiming one root: the lower tab id wins', () => {
    roots['h1:Sa'] = 'RX'
    roots['h1:Sb'] = 'RX'
    const p = run('h1', [tabOf('tb', 'h1', 'Sb', manualGroupKey('g2')), tabOf('ta', 'h1', 'Sa', manualGroupKey('g1'))])
    expect(p.labels).toContainEqual({ root_session_id: 'RX', group: 'Alpha' })
  })
})
