// spa/src/lib/task-groups/drag.test.ts — what a drag of a solo session onto the task-group view means (name pool v2 spec §7, NA-6c): onto a manual group of ITS OWN host files it there (L9: a group holds one host's
// sessions), onto 「未分組」 takes it out of its group; nothing else takes a drop. A host whose groups cannot be edited, or a session whose lineage root is not known yet, still receives the drop, and says why instead of
// writing: the drop must not look accepted and then do nothing silently. Nothing is ever written without the real root.
import { describe, it, expect } from 'vitest'
import { acceptsDrag, decideDrop, type DragSource, type DropTarget } from './drag'
import type { TaskGroup } from './api'
import type { TaskGroupsEntry } from '../../stores/useTaskGroupsStore'

const g = (id: string, roots: string[] = []): TaskGroup => ({ id, name: `G ${id}`, roots, created_at: 1, updated_at: 1 })
const entry = (groups: TaskGroup[], over: Partial<TaskGroupsEntry> = {}): TaskGroupsEntry => ({ status: 'ready', groups, revision: 1, loaded: true, dropped: null, invalid: false, ...over })
const t = (key: string, vars?: Record<string, unknown>) => (vars ? `${key}:${JSON.stringify(vars)}` : key)
const src = (over: Partial<DragSource> = {}): DragSource => ({ hostId: 'h1', root: 'R1', currentGroupId: null, ...over })
const group = (groupId = 'a', hostId = 'h1'): DropTarget => ({ kind: 'group', hostId, groupId })
const ungrouped: DropTarget = { kind: 'ungrouped' }
const entries = (e: Record<string, TaskGroupsEntry | undefined>) => (h: string) => e[h]

describe('acceptsDrag', () => {
  it('a manual group takes a session of its own host only', () => {
    expect(acceptsDrag(group('a', 'h1'), src({ hostId: 'h1' }))).toBe(true)
    expect(acceptsDrag(group('a', 'h2'), src({ hostId: 'h1' }))).toBe(false)
  })
  it('a session already in that group is not offered it again', () => {
    expect(acceptsDrag(group('a'), src({ currentGroupId: 'a' }))).toBe(false)
    expect(acceptsDrag(group('b'), src({ currentGroupId: 'a' }))).toBe(true)
  })
  it('「未分組」 takes only a session that is in a group', () => {
    expect(acceptsDrag(ungrouped, src({ currentGroupId: 'a' }))).toBe(true)
    expect(acceptsDrag(ungrouped, src({ currentGroupId: null }))).toBe(false)
  })
})

describe('decideDrop', () => {
  const ok = entries({ h1: entry([g('a'), g('b')]) })

  it('files the session in the group it was dropped on, with its root', () => {
    expect(decideDrop(group('a'), src(), ok, t)).toEqual({ kind: 'put', hostId: 'h1', root: 'R1', groupId: 'a' })
  })
  it('takes it out when dropped on 「未分組」', () => {
    expect(decideDrop(ungrouped, src({ currentGroupId: 'a' }), ok, t)).toEqual({ kind: 'out', hostId: 'h1', root: 'R1' })
  })
  it('ignores a drop nobody accepts (another host, the group it is in, 「未分組」 for an ungrouped one)', () => {
    expect(decideDrop(group('a', 'h2'), src(), ok, t)).toEqual({ kind: 'ignore' })
    expect(decideDrop(group('a'), src({ currentGroupId: 'a' }), ok, t)).toEqual({ kind: 'ignore' })
    expect(decideDrop(ungrouped, src(), ok, t)).toEqual({ kind: 'ignore' })
  })

  it('an unsafe host: no write, and the same sentence as everywhere else (no count when it has none)', () => {
    const unsafe = entries({ h1: entry([g('a')], { dropped: { count: 2, reasons: [] } }) })
    expect(decideDrop(group('a'), src(), unsafe, t)).toEqual({ kind: 'notice', message: 'taskgroups.unsafe_edit:{"n":2}' })
    const bad = entries({ h1: entry([], { invalid: true }) })
    expect(decideDrop(ungrouped, src({ currentGroupId: 'a' }), bad, t)).toEqual({ kind: 'notice', message: 'taskgroups.unsafe_edit_nocount' })
  })
  it('groups not read yet, or the root not known: a notice, never a write — and never the session\'s own id', () => {
    expect(decideDrop(group('a'), src(), entries({}), t)).toEqual({ kind: 'notice', message: 'taskgroups.groups_reading' })
    expect(decideDrop(group('a'), src({ root: null }), ok, t)).toEqual({ kind: 'notice', message: 'taskgroups.root_reading' })
    expect(decideDrop(ungrouped, src({ root: null, currentGroupId: 'a' }), ok, t)).toEqual({ kind: 'notice', message: 'taskgroups.root_reading' })
  })
})
