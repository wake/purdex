// spa/src/lib/task-groups/add-group.test.ts — 「新增任務編組」 (task groups spec §6.1): creates 「任務編組 N」 (the first N no group uses), appends it, selects it and starts its rename. Real stores.
import { beforeEach, describe, expect, it } from 'vitest'
import { addTaskGroup } from './add-group'
import { manualGroupKey } from './group-key'
import { useRenameRequestStore } from './rename-request'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useGroupViewStore } from '../../stores/useGroupViewStore'
import { useTabStore } from '../../stores/useTabStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'

const names = () => useManualGroupsStore.getState().order.map((id) => useManualGroupsStore.getState().groups[id].name)

beforeEach(() => {
  localStorage.clear()
  useI18nStore.getState().setLocale('zh-TW')
  useTeamRosterStore.getState().reset()
  useManualGroupsStore.setState({ order: [], groups: {} })
  useGroupViewStore.setState({ selectedGroup: null, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
  useTabStore.setState({ tabs: {}, activeTabId: null } as never)
  useRenameRequestStore.setState({ groupId: null, nonce: 0 })
})

describe('addTaskGroup', () => {
  it('makes 「任務編組 1」, appends it, selects it and asks for its rename', () => {
    const id = addTaskGroup()
    expect(id).not.toBeNull()
    expect(names()).toEqual(['任務編組 1'])
    expect(useGroupViewStore.getState().selectedGroup).toBe(manualGroupKey(id!))
    expect(useRenameRequestStore.getState()).toMatchObject({ groupId: id, nonce: 1 })
  })

  it('takes the first N no group uses: 1, 2, then 1 again once it is gone', () => {
    const a = addTaskGroup()
    addTaskGroup()
    expect(names()).toEqual(['任務編組 1', '任務編組 2'])
    useManualGroupsStore.getState().removeGroup(a!)
    addTaskGroup()
    expect(names()).toEqual(['任務編組 2', '任務編組 1'])
  })

  it('a new group is empty, so the active tab becomes none', () => {
    useTabStore.setState({ tabs: { t: { id: 't', pinned: false, locked: false, createdAt: 1, layout: { type: 'leaf', pane: { id: 'p', content: { kind: 'dashboard' } } } } }, activeTabId: 't' } as never)
    addTaskGroup()
    expect(useTabStore.getState().activeTabId).toBeNull()
  })
})
