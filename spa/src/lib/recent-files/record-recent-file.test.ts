import { describe, it, expect, beforeEach } from 'vitest'
import { recordRecentFile } from './record-recent-file'
import { useRecentFilesStore } from '../../stores/useRecentFilesStore'
import type { PaneContent } from '../../types/tab'

describe('recordRecentFile', () => {
  beforeEach(() => useRecentFilesStore.setState({ files: [] }))

  it('records an editor file with basename + kind', () => {
    recordRecentFile({ kind: 'editor', source: { type: 'inapp' }, filePath: '/docs/a.md' } as PaneContent)
    const f = useRecentFilesStore.getState().files
    expect(f).toHaveLength(1)
    expect(f[0]).toMatchObject({ kind: 'editor', name: 'a.md', path: '/docs/a.md' })
    expect(f[0].openedAt).toBeGreaterThan(0)
  })

  it('records image-preview and pdf-preview', () => {
    recordRecentFile({ kind: 'image-preview', source: { type: 'local' }, filePath: '/i.png' } as PaneContent)
    recordRecentFile({ kind: 'pdf-preview', source: { type: 'daemon', hostId: 'h' }, filePath: '/d.pdf' } as PaneContent)
    expect(useRecentFilesStore.getState().files.map((f) => f.kind)).toEqual(['pdf-preview', 'image-preview'])
  })

  it('ignores non-file pane kinds', () => {
    recordRecentFile({ kind: 'new-tab' } as PaneContent)
    recordRecentFile({ kind: 'tmux-session', sessionCode: 'x', mode: 'terminal' } as PaneContent)
    expect(useRecentFilesStore.getState().files).toHaveLength(0)
  })

  it('skips an unsaved untitled editor buffer', () => {
    recordRecentFile({
      kind: 'editor', source: { type: 'inapp' }, filePath: '/buffer/Untitled.md',
      untitled: { name: 'Untitled.md', hasBeenRenamed: false },
    } as PaneContent)
    expect(useRecentFilesStore.getState().files).toHaveLength(0)
  })
})
