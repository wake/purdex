import type { PaneContent } from '../types/tab'

export type ParsedRoute =
  | { kind: 'history' }
  | { kind: 'hosts' }
  | { kind: 'settings'; scope: 'global' }
  | { kind: 'session-tab'; tabId: string; mode: 'terminal' | 'stream' }
  | { kind: 'workspace'; workspaceId: string }
  | { kind: 'workspace-settings'; workspaceId: string }
  | { kind: 'workspace-session-tab'; workspaceId: string; tabId: string; mode: 'terminal' | 'stream' }

const ID_PATTERN = /^[0-9a-z]{6}$/

function validateMode(mode: string): 'terminal' | 'stream' {
  return mode === 'stream' ? 'stream' : 'terminal'
}

export function parseRoute(path: string): ParsedRoute | null {
  if (path === '/') return null // no-op — preserves persisted tab state
  if (path === '/history') return { kind: 'history' }
  if (path === '/hosts') return { kind: 'hosts' }
  if (path === '/settings' || path.startsWith('/settings/')) return { kind: 'settings', scope: 'global' }

  const segments = path.split('/').filter(Boolean)

  if (segments[0] === 't' && segments.length === 3) {
    if (!ID_PATTERN.test(segments[1])) return null
    return { kind: 'session-tab', tabId: segments[1], mode: validateMode(segments[2]) }
  }

  if (segments[0] === 'w' && segments.length === 2) {
    if (!ID_PATTERN.test(segments[1])) return null
    return { kind: 'workspace', workspaceId: segments[1] }
  }

  if (segments[0] === 'w' && segments[2] === 'settings' && segments.length === 3) {
    if (!ID_PATTERN.test(segments[1])) return null
    return { kind: 'workspace-settings', workspaceId: segments[1] }
  }

  if (segments[0] === 'w' && segments[2] === 't' && segments.length === 5) {
    if (!ID_PATTERN.test(segments[1]) || !ID_PATTERN.test(segments[3])) return null
    return {
      kind: 'workspace-session-tab',
      workspaceId: segments[1],
      tabId: segments[3],
      mode: validateMode(segments[4]),
    }
  }

  return null
}

export function tabToUrl(tabId: string, content: PaneContent, workspaceId?: string): string {
  switch (content.kind) {
    case 'new-tab': return '/'
    case 'dashboard': return '/'
    case 'history': return '/history'
    case 'settings':
      if (content.scope === 'global') return '/settings'
      return `/w/${content.scope.workspaceId}/settings`
    case 'tmux-session':
      if (workspaceId) return `/w/${workspaceId}/t/${tabId}/${content.mode}`
      return `/t/${tabId}/${content.mode}`
    case 'hosts':
      return '/hosts'
    case 'browser':
      return '/'
    case 'memory-monitor':
      return '/'
  }
}
