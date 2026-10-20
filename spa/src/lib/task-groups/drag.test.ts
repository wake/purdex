// spa/src/lib/task-groups/drag.test.ts — what a drag of a solo session onto the task-group view means (task groups spec §6.1, TG-2b): onto a manual group of ANY host it is filed there, onto 「未分組」 it is taken out
// of its group; the group it is in already, and 「未分組」 for a session in no group, take nothing.
import { describe, it, expect } from 'vitest'
import { acceptsDrag, decideDrop } from './drag'

describe('acceptsDrag', () => {
  it('a group takes a session that is not in it — whatever host either is on (no same-host rule)', () => {
    expect(acceptsDrag({ kind: 'group', groupId: 'g1' }, { currentGroupId: null })).toBe(true)
    expect(acceptsDrag({ kind: 'group', groupId: 'g1' }, { currentGroupId: 'g2' })).toBe(true)
    expect(acceptsDrag({ kind: 'group', groupId: 'g1' }, { currentGroupId: 'g1' })).toBe(false)
  })

  it('未分組 takes a session that is in a group, and nothing else', () => {
    expect(acceptsDrag({ kind: 'ungrouped' }, { currentGroupId: 'g1' })).toBe(true)
    expect(acceptsDrag({ kind: 'ungrouped' }, { currentGroupId: null })).toBe(false)
  })
})

describe('decideDrop', () => {
  it('onto a group is a put, onto 未分組 is an out', () => {
    expect(decideDrop({ kind: 'group', groupId: 'g2' }, { currentGroupId: 'g1' })).toEqual({ kind: 'put', groupId: 'g2' })
    expect(decideDrop({ kind: 'ungrouped' }, { currentGroupId: 'g1' })).toEqual({ kind: 'out' })
  })

  it('a drop that changes nothing is ignored', () => {
    expect(decideDrop({ kind: 'group', groupId: 'g1' }, { currentGroupId: 'g1' })).toEqual({ kind: 'ignore' })
    expect(decideDrop({ kind: 'ungrouped' }, { currentGroupId: null })).toEqual({ kind: 'ignore' })
  })
})
