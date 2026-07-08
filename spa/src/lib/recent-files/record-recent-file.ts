import { useRecentFilesStore } from '../../stores/useRecentFilesStore'
import type { PaneContent } from '../../types/tab'

/**
 * Record a user-opened file into the recent list. Only file panes
 * (editor / image-preview / pdf-preview) are recorded; a still-unsaved
 * untitled editor buffer is skipped (it enters Recent on first save via
 * saveUntitledBuffer). Safe to call from any open chokepoint.
 */
export function recordRecentFile(content: PaneContent): void {
  // Narrow to the three file kinds via explicit checks (which also lets TS see
  // `source` / `filePath` on the narrowed union — the shared FILE_KINDS set
  // does not narrow on its own).
  if (
    content.kind !== 'editor' &&
    content.kind !== 'image-preview' &&
    content.kind !== 'pdf-preview'
  ) {
    return
  }
  // A still-unsaved untitled editor buffer enters Recent on first save instead.
  if (content.kind === 'editor' && content.untitled && !content.untitled.hasBeenRenamed) return

  const name = content.filePath.split('/').pop() ?? content.filePath
  useRecentFilesStore.getState().addRecent({
    source: content.source,
    path: content.filePath,
    name,
    kind: content.kind,
    openedAt: Date.now(),
  })
}
