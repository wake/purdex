// spa/src/lib/task-groups/view-reconcile.test.ts — the task-group view's memory of a manual group goes when the group goes (task groups spec §6.1). Real stores.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { reconcileManualKeys, startGroupViewReconcile } from './view-reconcile'
import { useGroupViewStore } from '../../stores/useGroupViewStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { manualGroupKey, teamGroupKey } from './group-key'

const A = manualGroupKey('aaaaaaaa')
const B = manualGroupKey('bbbbbbbb')
const TEAM = teamGroupKey('h1\u0000t1')
const g = (name: string) => ({ name, members: [], createdAt: 1 })
const view = () => useGroupViewStore.getState()
let stop: () => void

beforeEach(() => {
  localStorage.clear()
  useManualGroupsStore.setState({ order: ['aaaaaaaa', 'bbbbbbbb'], groups: { aaaaaaaa: g('A'), bbbbbbbb: g('B') } })
  useGroupViewStore.setState({ selectedGroup: B, lastActiveTab: { [A]: 't1', [B]: 't2', [TEAM]: 't3' }, expanded: { [A]: false, [B]: false, [TEAM]: false }, lastByView: { workspace: null, group: B } })
  stop = startGroupViewReconcile()
})
afterEach(() => { stop() })

describe('manual keys against the store', () => {
  it('deleting a group forgets its memory — last tab, expanded, selection — and only its', () => {
    useManualGroupsStore.getState().removeGroup('bbbbbbbb')
    expect(view().lastActiveTab).toEqual({ [A]: 't1', [TEAM]: 't3' })
    expect(view().expanded).toEqual({ [A]: false, [TEAM]: false })
    expect([view().selectedGroup, view().lastByView.group]).toEqual([null, null])
  })

  it('a change that keeps every group (a rename) forgets nothing', () => {
    const before = view().lastActiveTab
    useManualGroupsStore.getState().patchGroup('aaaaaaaa', { name: 'A2' })
    expect(view().lastActiveTab).toBe(before)
  })

  it('before the manual groups hydrated, nothing is forgotten', () => {
    useManualGroupsStore.setState({ order: [], groups: {} })
    // the subscriber already ran for that write; put the memory back and call the reconcile as the start would, with hydration pending
    useGroupViewStore.setState({ selectedGroup: B, lastActiveTab: { [B]: 't2' }, expanded: {}, lastByView: { workspace: null, group: B } })
    const hydrated = useManualGroupsStore.persist.hasHydrated
    useManualGroupsStore.persist.hasHydrated = () => false
    try {
      reconcileManualKeys()
      expect(view().lastActiveTab).toEqual({ [B]: 't2' })
    } finally {
      useManualGroupsStore.persist.hasHydrated = hydrated
    }
  })
})
