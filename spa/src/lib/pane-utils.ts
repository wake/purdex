import type { PaneContent } from '../types/tab'

export function contentMatches(a: PaneContent, b: PaneContent): boolean {
  if (a.kind !== b.kind) return false
  if (a.kind === 'session') return false // sessions are never singletons
  if (a.kind === 'settings' && b.kind === 'settings') {
    return JSON.stringify(a.scope) === JSON.stringify(b.scope)
  }
  return true
}
