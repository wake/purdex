import { describe, it, expect } from 'vitest'
import { dragReorder } from './team-group-drag'
import type { TabTeamHit } from './team-runs'

// t1: lead L + members a, b (a run); t2: lead M + member c. 'x' and 'y' are plain tabs.
const HITS: Record<string, TabTeamHit> = {
  L: { key: 't1', role: 'lead' }, a: { key: 't1', role: 'member' }, b: { key: 't1', role: 'member' },
  M: { key: 't2', role: 'lead' }, c: { key: 't2', role: 'member' },
}
const of = (id: string) => HITS[id] ?? null
const ids = ['x', 'L', 'a', 'b', 'y']

describe('dragReorder (TG-1)', () => {
  it('lead dragged onto a plain tab after the group: the whole run, collapsed members included, moves behind it', () => {
    expect(dragReorder(ids, 'L', 'y', of)).toEqual(['x', 'y', 'L', 'a', 'b'])
  })
  it('lead dragged onto a plain tab before the group: the whole run lands in front of it', () => {
    expect(dragReorder(ids, 'L', 'x', of)).toEqual(['L', 'a', 'b', 'x', 'y'])
  })
  it('a plain tab dropped on the lead or a member lands before/after the whole group, never inside', () => {
    expect(dragReorder(ids, 'x', 'L', of)).toEqual(['L', 'a', 'b', 'x', 'y'])
    expect(dragReorder(ids, 'x', 'a', of)).toEqual(['L', 'a', 'b', 'x', 'y'])
    expect(dragReorder(ids, 'x', 'b', of)).toEqual(['L', 'a', 'b', 'x', 'y'])
    expect(dragReorder(ids, 'y', 'b', of)).toEqual(['x', 'y', 'L', 'a', 'b'])
    expect(dragReorder(ids, 'y', 'L', of)).toEqual(['x', 'y', 'L', 'a', 'b'])
  })
  it('a group dropped on another group lands before or after it, the two never mix', () => {
    const two = ['L', 'a', 'b', 'x', 'M', 'c']
    expect(dragReorder(two, 'L', 'c', of)).toEqual(['x', 'M', 'c', 'L', 'a', 'b'])
    expect(dragReorder(two, 'L', 'M', of)).toEqual(['x', 'M', 'c', 'L', 'a', 'b'])
    expect(dragReorder(two, 'M', 'a', of)).toEqual(['M', 'c', 'L', 'a', 'b', 'x'])
  })
  it('a member is not handled here (it never moves the group, nor leaves it): null', () => {
    expect(dragReorder(ids, 'a', 'x', of)).toBeNull()
    expect(dragReorder(ids, 'a', 'L', of)).toBeNull()
    expect(dragReorder(ids, 'b', 'y', of)).toBeNull()
  })
  it('a drop on itself or inside its own group, or on an unknown id, changes nothing', () => {
    expect(dragReorder(ids, 'L', 'a', of)).toBeNull()
    expect(dragReorder(ids, 'L', 'L', of)).toBeNull()
    expect(dragReorder(ids, 'x', 'x', of)).toBeNull()
    expect(dragReorder(ids, 'L', 'nope', of)).toBeNull()
    expect(dragReorder(ids, 'nope', 'L', of)).toBeNull()
  })
  it('plain tabs among themselves reorder as arrayMove, the group staying whole', () => {
    expect(dragReorder(ids, 'x', 'y', of)).toEqual(['L', 'a', 'b', 'y', 'x'])
  })
  it('a member left outside a run (its lead is elsewhere) is an ordinary tab', () => {
    expect(dragReorder(['x', 'a', 'y'], 'a', 'y', of)).toEqual(['x', 'y', 'a'])
  })
})
