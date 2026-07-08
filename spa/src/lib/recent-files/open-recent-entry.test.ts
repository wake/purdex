import { describe, it, expect, beforeEach, vi } from 'vitest'
import { openRecentEntry } from './open-recent-entry'
import { useRecentFilesStore, type RecentFileEntry } from '../../stores/useRecentFilesStore'

const hosts = vi.hoisted(() => ({ value: {} as Record<string, { name: string }> }))
const toastShow = vi.hoisted(() => vi.fn())
const daemonStat = vi.hoisted(() => vi.fn())
const inappStat = vi.hoisted(() => vi.fn())

vi.mock('../../stores/useHostStore', () => ({
  useHostStore: { getState: () => ({ hosts: hosts.value }) },
}))
vi.mock('../../stores/useUndoToast', () => ({
  useUndoToast: { getState: () => ({ show: toastShow }) },
}))
vi.mock('../fs-backend-daemon', () => ({
  createDaemonBackendForHost: () => ({ stat: daemonStat }),
}))
vi.mock('../fs-backend', () => ({
  getFsBackend: (s: { type: string }) => (s.type === 'inapp' ? { stat: inappStat } : undefined),
}))
vi.mock('../../stores/useI18nStore', () => ({
  useI18nStore: { getState: () => ({ t: (k: string) => k }) },
}))

const daemonEntry = (): RecentFileEntry => ({
  source: { type: 'daemon', hostId: 'h1' }, path: '/p/a.md', name: 'a.md', kind: 'editor', openedAt: 1,
})

describe('openRecentEntry', () => {
  beforeEach(() => {
    hosts.value = { h1: { name: 'mlab' } }
    toastShow.mockReset(); daemonStat.mockReset(); inappStat.mockReset()
  })

  it('daemon host present + stat ok → onSelect, no toast', async () => {
    daemonStat.mockResolvedValue({ isFile: true })
    const onSelect = vi.fn()
    await openRecentEntry(daemonEntry(), onSelect)
    expect(onSelect).toHaveBeenCalledWith({ kind: 'editor', source: { type: 'daemon', hostId: 'h1' }, filePath: '/p/a.md' })
    expect(toastShow).not.toHaveBeenCalled()
  })

  it('daemon host absent → toast, no onSelect, no stat', async () => {
    hosts.value = {}
    const onSelect = vi.fn()
    await openRecentEntry(daemonEntry(), onSelect)
    expect(daemonStat).not.toHaveBeenCalled()
    expect(onSelect).not.toHaveBeenCalled()
    expect(toastShow).toHaveBeenCalled()
  })

  it('daemon stat throws → toast, no onSelect', async () => {
    daemonStat.mockRejectedValue(Object.assign(new Error('x'), { status: 404 }))
    const onSelect = vi.fn()
    await openRecentEntry(daemonEntry(), onSelect)
    expect(onSelect).not.toHaveBeenCalled()
    expect(toastShow).toHaveBeenCalled()
  })

  it('inapp stat ok → onSelect; missing → toast', async () => {
    inappStat.mockResolvedValueOnce({ isFile: true })
    const onSelect = vi.fn()
    const e: RecentFileEntry = { source: { type: 'inapp' }, path: '/b.md', name: 'b.md', kind: 'editor', openedAt: 1 }
    await openRecentEntry(e, onSelect)
    expect(onSelect).toHaveBeenCalled()

    inappStat.mockRejectedValueOnce(new Error('ENOENT'))
    const onSelect2 = vi.fn()
    await openRecentEntry(e, onSelect2)
    expect(onSelect2).not.toHaveBeenCalled()
    expect(toastShow).toHaveBeenCalled()
  })

  it('local with no backend → onSelect (best-effort)', async () => {
    const onSelect = vi.fn()
    const e: RecentFileEntry = { source: { type: 'local' }, path: '/l.md', name: 'l.md', kind: 'editor', openedAt: 1 }
    await openRecentEntry(e, onSelect)
    expect(onSelect).toHaveBeenCalled()
  })

  it('re-records the entry on a successful open (recency refresh)', async () => {
    useRecentFilesStore.setState({ files: [] })
    daemonStat.mockResolvedValue({ isFile: true })
    await openRecentEntry(daemonEntry(), vi.fn())
    expect(useRecentFilesStore.getState().files.map((f) => f.path)).toContain('/p/a.md')
  })

  it('does NOT re-record when the open fails', async () => {
    useRecentFilesStore.setState({ files: [] })
    daemonStat.mockRejectedValue(new Error('missing'))
    await openRecentEntry(daemonEntry(), vi.fn())
    expect(useRecentFilesStore.getState().files).toHaveLength(0)
  })
})
