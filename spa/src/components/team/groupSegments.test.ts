// spa/src/components/team/groupSegments.test.ts — how the normal zone splits into plain tabs and team runs (plan TI-2).
import { describe, it, expect } from 'vitest'
import { groupSegments } from './groupSegments'
import type { TeamTabMark } from './team-display'

const mark = (teamKey: string, role: 'lead' | 'member'): TeamTabMark => ({
  teamKey, color: '#a78bfa', role,
  first: false, last: false, collapsed: false, hidden: false, seatState: 'active', hostAlias: '',
})
const marks: Record<string, TeamTabMark> = {
  lead: mark('T', 'lead'), a: mark('T', 'member'), b: mark('T', 'member'),
  lead2: mark('U', 'lead'), c: mark('U', 'member'),
}
const tabMark = (id: string) => marks[id] ?? null
const t = (...ids: string[]) => ids.map((id) => ({ id }))
const shape = (segs: ReturnType<typeof groupSegments<{ id: string }>>) =>
  segs.map((s) => (s.kind === 'tab' ? s.tab.id : `[${s.tabs.map((x) => x.id).join(',')}]`))

describe('groupSegments', () => {
  it('a team run is the lead plus the members after it; other tabs are single segments', () => {
    expect(shape(groupSegments(t('x', 'lead', 'a', 'b', 'y'), tabMark, {}))).toEqual(['x', '[lead,a,b]', 'y'])
  })

  it('two teams side by side are two runs', () => {
    expect(shape(groupSegments(t('lead', 'a', 'lead2', 'c'), tabMark, {}))).toEqual(['[lead,a]', '[lead2,c]'])
  })

  it('collapsed: the lead only', () => {
    expect(shape(groupSegments(t('lead', 'a', 'b', 'y'), tabMark, { T: true }))).toEqual(['[lead]', 'y'])
  })

  it('a member with no lead right before it stays an ordinary tab', () => {
    expect(shape(groupSegments(t('a', 'x', 'b'), tabMark, {}))).toEqual(['a', 'x', 'b'])
  })

  it('a member of another team does not join the run', () => {
    expect(shape(groupSegments(t('lead', 'c', 'a'), tabMark, {}))).toEqual(['[lead]', 'c', 'a'])
  })

  it('no marks at all: every tab is single', () => {
    expect(shape(groupSegments(t('x', 'y'), () => null, {}))).toEqual(['x', 'y'])
  })
})
