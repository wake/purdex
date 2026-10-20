// spa/src/lib/task-groups/drop-resolve.test.ts — reorder or move: what dropping a row in the task-group view means (TG-3e).
import { describe, it, expect } from 'vitest'
import { resolveDrop, type DraggedRow } from './drop-resolve'
import { manualGroupKey, teamGroupKey, UNGROUPED } from './group-key'

const G1 = manualGroupKey('aaaaaaaa')
const G2 = manualGroupKey('bbbbbbbb')
const TEAM = teamGroupKey('h1\u0000a')
const row = (tabId: string, groupKey: string, kind: DraggedRow['kind'] = 'session'): DraggedRow => ({ tabId, groupKey, kind })
const onRow = (tabId: string, groupKey: string) => ({ kind: 'row' as const, tabId, groupKey })
const IGNORE = { kind: 'ignore' }

describe('resolveDrop', () => {
  it('on a row of its own group: a reorder, the side is the direction of travel', () => {
    const order = ['a', 'b', 'c']
    expect(resolveDrop(row('a', G1), onRow('c', G1), order)).toEqual({ kind: 'reorder', groupKey: G1, overId: 'c', side: 'after' })
    expect(resolveDrop(row('c', G1), onRow('a', G1), order)).toEqual({ kind: 'reorder', groupKey: G1, overId: 'a', side: 'before' })
  })

  it('on itself, or on a row the group does not list: nothing', () => {
    expect(resolveDrop(row('a', G1), onRow('a', G1), ['a', 'b'])).toEqual(IGNORE)
    expect(resolveDrop(row('a', G1), onRow('x', G1), ['a', 'b'])).toEqual(IGNORE)
  })

  it('a session on a row or the body of another manual group: a move there', () => {
    expect(resolveDrop(row('a', G1), onRow('z', G2), ['a'])).toEqual({ kind: 'move', target: { kind: 'group', groupId: 'bbbbbbbb' } })
    expect(resolveDrop(row('a', G1), { kind: 'target', target: { kind: 'group', groupId: 'bbbbbbbb' } }, ['a'])).toEqual({ kind: 'move', target: { kind: 'group', groupId: 'bbbbbbbb' } })
  })

  it('a session in a group onto 「未分組」 is taken out; one already there is not moved', () => {
    expect(resolveDrop(row('a', G1), onRow('u', UNGROUPED), ['a'])).toEqual({ kind: 'move', target: { kind: 'ungrouped' } })
    expect(resolveDrop(row('a', UNGROUPED), { kind: 'target', target: { kind: 'ungrouped' } }, ['a'])).toEqual(IGNORE)
  })

  it('the group a session is already in takes nothing from its own body', () => {
    expect(resolveDrop(row('a', G1), { kind: 'target', target: { kind: 'group', groupId: 'aaaaaaaa' } }, ['a'])).toEqual(IGNORE)
  })

  it('a team takes no drop', () => {
    expect(resolveDrop(row('a', G1), onRow('t', TEAM), ['a'])).toEqual(IGNORE)
    expect(resolveDrop(row('p', G1, 'plain'), onRow('t', TEAM), ['p'])).toEqual(IGNORE)
  })

  it('a team seat never leaves its team, but reorders inside it', () => {
    expect(resolveDrop(row('s', TEAM, 'seat'), onRow('z', G1), ['s'])).toEqual(IGNORE)
    expect(resolveDrop(row('s', TEAM, 'seat'), { kind: 'target', target: { kind: 'ungrouped' } }, ['s'])).toEqual(IGNORE)
    expect(resolveDrop(row('s', TEAM, 'seat'), onRow('t', TEAM), ['s', 't'])).toEqual({ kind: 'reorder', groupKey: TEAM, overId: 't', side: 'after' })
  })

  it('a plain tab moves between groups, also out of a team, and not onto where it is', () => {
    expect(resolveDrop(row('p', TEAM, 'plain'), onRow('z', G1), ['p'])).toEqual({ kind: 'move', target: { kind: 'group', groupId: 'aaaaaaaa' } })
    expect(resolveDrop(row('p', TEAM, 'plain'), { kind: 'target', target: { kind: 'ungrouped' } }, ['p'])).toEqual({ kind: 'move', target: { kind: 'ungrouped' } })
    expect(resolveDrop(row('p', G1, 'plain'), { kind: 'target', target: { kind: 'group', groupId: 'aaaaaaaa' } }, ['p'])).toEqual(IGNORE)
    expect(resolveDrop(row('p', UNGROUPED, 'plain'), { kind: 'target', target: { kind: 'ungrouped' } }, ['p'])).toEqual(IGNORE)
  })
})
