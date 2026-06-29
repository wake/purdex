import { describe, it, expect, beforeEach, vi } from 'vitest'
import type { RestoreResult, RestoreSnapshotDeps } from './restore'

const restoreSnapshot = vi.fn<(deps: RestoreSnapshotDeps) => Promise<RestoreResult>>()
vi.mock('./restore', () => ({ restoreSnapshot: (deps: RestoreSnapshotDeps) => restoreSnapshot(deps) }))

const applyReconciliation = vi.fn()
vi.mock('./reconcile-panes', () => ({ applyReconciliation: (...a: unknown[]) => applyReconciliation(...a) }))

const fakeBackend = {
  read: vi.fn(async () => new TextEncoder().encode('hi')),
  stat: vi.fn(async () => ({ mtime: 5, size: 2, isDirectory: false, isFile: true })),
  replaceTree: vi.fn(),
  getRevision: vi.fn(),
}
vi.mock('../fs-backend', () => ({ getFsBackend: () => fakeBackend }))

import { runRestore } from './restore-wiring'
import { useBackupStore } from '../../stores/useBackupStore'
import { useTabStore } from '../../stores/useTabStore'
import { useEditorStore } from '../../stores/useEditorStore'
import { bufferKey } from '../editor-buffer-key'

beforeEach(() => {
  restoreSnapshot.mockReset()
  applyReconciliation.mockReset()
  applyReconciliation.mockResolvedValue({ failed: [] }) // best-effort default
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useEditorStore.setState({ buffers: {}, paneStates: {} })
})

describe('runRestore', () => {
  it('runs pane reconciliation with the changed diff on a done result', async () => {
    const changed = { added: [], removed: ['x'], modified: ['y'] }
    const restoredFiles = ['y']
    restoreSnapshot.mockResolvedValue({ status: 'done', restorePointId: 7, changed, restoredFiles })
    const result = await runRestore('host-A', 42)
    expect(result).toEqual({ status: 'done', restorePointId: 7, changed, restoredFiles })
    expect(applyReconciliation).toHaveBeenCalledTimes(1)
    expect(applyReconciliation.mock.calls[0][0]).toEqual(changed)
  })

  it('does NOT reconcile (or otherwise mutate) on a blocked result', async () => {
    const conflicts = [{ type: 'dirty' as const, tabId: 't1', filePath: '/buffer/a.md' }]
    restoreSnapshot.mockResolvedValue({ status: 'blocked', conflicts })
    const result = await runRestore('host-A', 42)
    expect(result).toEqual({ status: 'blocked', conflicts })
    expect(applyReconciliation).not.toHaveBeenCalled()
  })

  it('wires preRestore to backupNow(pre-restore, forcePost) and findConflicts to the live stores', async () => {
    restoreSnapshot.mockResolvedValue({ status: 'blocked', conflicts: [] })
    const backupNow = vi.fn().mockResolvedValue(9)
    useBackupStore.setState({ backupNow })
    // A dirty inapp buffer that findConflicts must observe.
    const tab = {
      id: 't1', pinned: false, locked: false, createdAt: 0,
      layout: { type: 'leaf' as const, pane: { id: 'p1', content: { kind: 'editor' as const, source: { type: 'inapp' as const }, filePath: '/buffer/a.md' } } },
    }
    useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1' })
    useEditorStore.setState({ buffers: { [bufferKey({ type: 'inapp' }, '/buffer/a.md')]: { isDirty: true } as never } })

    await runRestore('host-A', 42)
    const deps = restoreSnapshot.mock.calls[0][0]

    await deps.preRestore()
    expect(backupNow).toHaveBeenCalledWith('host-A', { trigger: 'pre-restore', forcePost: true })

    const conflicts = deps.findConflicts()
    expect(conflicts).toContainEqual({ type: 'dirty', tabId: 't1', filePath: '/buffer/a.md' })
  })
})
