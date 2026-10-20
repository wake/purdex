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
    const deps = { ctx: ctx(), sessionOf: () => null, onResult: vi.fn() }
    applyGroupDrop({ tabId: a.id, groupKey: UNGROUPED, kind: 'plain' }, { kind: 'target', target: { kind: 'group', groupId: 'aaaaaaaa' } }, deps)
    expect(useTabStore.getState().tabs[a.id].group).toBe(G)
    applyGroupDrop({ tabId: a.id, groupKey: G, kind: 'plain' }, { kind: 'target', target: { kind: 'ungrouped' } }, deps)
    expect(useTabStore.getState().tabs[a.id].group).toBeUndefined()
  })

  it('a session onto a group is filed there, onto 「未分組」 taken out; the result goes to onResult', async () => {
    const ref = { hostId: 'h', agentSessionId: 's', tabId: 't' }
    const onResult = vi.fn()
    const deps = { ctx: ctx(), sessionOf: () => ref, onResult }
    applyGroupDrop({ tabId: 't', groupKey: UNGROUPED, kind: 'session' }, { kind: 'target', target: { kind: 'group', groupId: 'aaaaaaaa' } }, deps)
    expect(fileSession).toHaveBeenCalledWith(ref, 'aaaaaaaa')
    applyGroupDrop({ tabId: 't', groupKey: G, kind: 'session' }, { kind: 'target', target: { kind: 'ungrouped' } }, deps)
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
