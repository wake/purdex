// spa/src/lib/task-groups/apply-drop.test.ts — a dropped row becomes writes (TG-3e).
import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('./session-actions', () => ({
  fileSession: vi.fn(async () => ({ ok: true })),
  takeSessionOut: vi.fn(async () => ({ ok: true })),
}))

import { applyGroupDrop } from './apply-drop'
import { fileSession, takeSessionOut } from './session-actions'
import { useTabStore } from '../../stores/useTabStore'
import { createTab } from '../../types/tab'
import { manualGroupKey, UNGROUPED } from './group-key'
import type { GroupOrderCtx } from './group-order'

const mk = (code: string) => createTab({ kind: 'tmux-session', hostId: 'h', sessionCode: code, mode: 'terminal', cachedName: '', tmuxInstance: '' })
const G = manualGroupKey('aaaaaaaa')

function ctx(): GroupOrderCtx {
  return {
    tabs: useTabStore.getState().tabs,
    index: { seatByTabId: new Map(), byKey: new Map() },
    manual: { order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 } } },
    rootOf: () => null,
  }
}

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  vi.clearAllMocks()
})

describe('applyGroupDrop', () => {
  it('a plain tab onto a manual group gets its group, onto 「未分組」 loses it', () => {
    const a = mk('a')
    useTabStore.getState().addTab(a)
    const deps = () => ({ ctx: ctx(), sessionOf: () => null, onResult: vi.fn() })
    applyGroupDrop({ tabId: a.id, groupKey: UNGROUPED, kind: 'plain' }, { kind: 'target', target: { kind: 'group', groupId: 'aaaaaaaa' } }, deps())
    expect(useTabStore.getState().tabs[a.id].group).toBe(G)
    applyGroupDrop({ tabId: a.id, groupKey: G, kind: 'plain' }, { kind: 'target', target: { kind: 'ungrouped' } }, deps())
    expect(useTabStore.getState().tabs[a.id].group).toBeUndefined()
  })

  it('a session onto a group is filed there, onto 「未分組」 taken out; the result goes to onResult', async () => {
    const t = mk('t')
    useTabStore.getState().addTab(t)
    const ref = { hostId: 'h', agentSessionId: 's', tabId: t.id }
    const onResult = vi.fn()
    applyGroupDrop({ tabId: t.id, groupKey: UNGROUPED, kind: 'session' }, { kind: 'target', target: { kind: 'group', groupId: 'aaaaaaaa' } }, { ctx: ctx(), sessionOf: () => ref, onResult })
    expect(fileSession).toHaveBeenCalledWith(ref, 'aaaaaaaa')
    // it is in the group now (a member whose root the cache knows)
    useTabStore.getState().setTabGroup(t.id, G)
    applyGroupDrop({ tabId: t.id, groupKey: G, kind: 'session' }, { kind: 'target', target: { kind: 'ungrouped' } }, { ctx: ctx(), sessionOf: () => ref, onResult })
    expect(takeSessionOut).toHaveBeenCalledWith(ref)
    await Promise.resolve()
    await Promise.resolve()
    expect(onResult).toHaveBeenCalledTimes(2)
  })

  it('a drop that means nothing writes nothing', () => {
    const a = mk('a')
    useTabStore.getState().addTab(a)
    const before = useTabStore.getState().tabs
    applyGroupDrop({ tabId: a.id, groupKey: UNGROUPED, kind: 'session' }, { kind: 'target', target: { kind: 'ungrouped' } }, { ctx: ctx(), sessionOf: () => null, onResult: vi.fn() })
    expect(useTabStore.getState().tabs).toBe(before)
    expect(fileSession).not.toHaveBeenCalled()
  })

  it('a row whose kind or group changed while it was dragged is not written from the old picture', () => {
    const a = mk('a')
    useTabStore.getState().addTab(a)
    const seat = { key: 'h\u0000t', role: 'member', seat: {} } as never
    const c = ctx()
    c.index.seatByTabId.set(a.id, seat) // the tab became a team seat meanwhile
    const deps = { ctx: c, sessionOf: () => null, onResult: vi.fn() }
    applyGroupDrop({ tabId: a.id, groupKey: UNGROUPED, kind: 'plain' }, { kind: 'target', target: { kind: 'group', groupId: 'aaaaaaaa' } }, deps)
    expect(useTabStore.getState().tabs[a.id].group).toBeUndefined()
    // and one that is no longer listed anywhere
    applyGroupDrop({ tabId: 'gone', groupKey: UNGROUPED, kind: 'plain' }, { kind: 'target', target: { kind: 'group', groupId: 'aaaaaaaa' } }, deps)
    expect(fileSession).not.toHaveBeenCalled()
  })

  it('a tab that became a session (same group) or moved to another group meanwhile is not written from the drag-start picture', () => {
    const a = mk('a')
    useTabStore.getState().addTab(a)
    const ref = { hostId: 'h', agentSessionId: 's', tabId: a.id }
    const target = { kind: 'target' as const, target: { kind: 'group' as const, groupId: 'aaaaaaaa' } }
    // it was a plain tab when the drag began, a session now: it is neither written as plain nor filed from the old kind
    applyGroupDrop({ tabId: a.id, groupKey: UNGROUPED, kind: 'plain' }, target, { ctx: ctx(), sessionOf: () => ref, onResult: vi.fn() })
    expect(useTabStore.getState().tabs[a.id].group).toBeUndefined()
    expect(fileSession).not.toHaveBeenCalled()
    // a session that was in 「未分組」 when the drag began and is in the group now
    useTabStore.getState().setTabGroup(a.id, G)
    applyGroupDrop({ tabId: a.id, groupKey: UNGROUPED, kind: 'session' }, target, { ctx: ctx(), sessionOf: () => ref, onResult: vi.fn() })
    expect(fileSession).not.toHaveBeenCalled()
  })

  it('a drop on a row of its own group reorders by rank', () => {
    const a = mk('a')
    const b = mk('b')
    const c = mk('c')
    for (const x of [a, b, c]) useTabStore.getState().addTab(x)
    const tabs = useTabStore.getState().tabs
    for (const [i, x] of [a, b, c].entries()) (tabs[x.id] as { createdAt: number }).createdAt = i + 1
    applyGroupDrop({ tabId: c.id, groupKey: UNGROUPED, kind: 'plain' }, { kind: 'row', tabId: a.id, groupKey: UNGROUPED }, { ctx: ctx(), sessionOf: () => null, onResult: vi.fn() })
    const after = useTabStore.getState().tabs
    expect(after[c.id].groupRank).toBeDefined()
  })
})
