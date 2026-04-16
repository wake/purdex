import type { PaneContent } from '../types/tab'

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
  return true
}
