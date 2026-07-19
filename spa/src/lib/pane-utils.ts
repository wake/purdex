import type { PaneContent } from '../types/tab'

/**
 * Pane kinds that hold an open file from a `FileSource` + `filePath`: the
 * editor plus the file-preview kinds the opener registry produces (png →
 * image-preview, pdf → pdf-preview). Storage rename/delete must treat all
 * three uniformly so a renamed/deleted file doesn't strand a preview tab.
 */
export type FilePaneContent = Extract<
  PaneContent,
  { kind: 'editor' | 'image-preview' | 'pdf-preview' }
>

export function isFilePaneContent(content: PaneContent): content is FilePaneContent {
  return (
    content.kind === 'editor' ||
    content.kind === 'image-preview' ||
    content.kind === 'pdf-preview'
  )
}

export function contentMatches(a: PaneContent, b: PaneContent): boolean {
  if (a.kind !== b.kind) return false
  if (a.kind === 'tmux-session') return false // sessions are never singletons
  if (a.kind === 'browser') return false // browser panes are never singletons
  if (a.kind === 'settings' && b.kind === 'settings') {
    return JSON.stringify(a.scope) === JSON.stringify(b.scope)
  }
  if (a.kind === 'editor' && b.kind === 'editor') {
    if (a.source.type !== b.source.type) return false
    if (a.source.type === 'daemon' && b.source.type === 'daemon') {
      return a.filePath === b.filePath && a.source.hostId === b.source.hostId
    }
    return a.filePath === b.filePath
  }
  if (a.kind === 'image-preview' && b.kind === 'image-preview') {
    if (a.source.type !== b.source.type) return false
    if (a.source.type === 'daemon' && b.source.type === 'daemon') {
      return a.filePath === b.filePath && a.source.hostId === b.source.hostId
    }
    return a.filePath === b.filePath
  }
  if (a.kind === 'pdf-preview' && b.kind === 'pdf-preview') {
    if (a.source.type !== b.source.type) return false
    if (a.source.type === 'daemon' && b.source.type === 'daemon') {
      return a.filePath === b.filePath && a.source.hostId === b.source.hostId
    }
    return a.filePath === b.filePath
  }
  // Execution detail pages are singletons per execution id, so a second deeplink
  // to the same execution reuses its tab instead of stacking duplicates.
  if (a.kind === 'execution' && b.kind === 'execution') {
    return a.executionId === b.executionId
  }
  return true
}
