import type { PaneContent } from '../types/tab'

export type ParsedRoute =
  | { kind: 'dashboard' }
  | { kind: 'history' }
  | { kind: 'settings'; scope: 'global' }
  | { kind: 'session-tab'; tabId: string; mode: 'terminal' | 'stream' }
  | { kind: 'workspace'; workspaceId: string }
  | { kind: 'workspace-settings'; workspaceId: string }
  | { kind: 'workspace-session-tab'; workspaceId: string; tabId: string; mode: 'terminal' | 'stream' }

function validateMode(mode: string): 'terminal' | 'stream' {
  return mode === 'stream' ? 'stream' : 'terminal'
}

export function parseRoute(path: string): ParsedRoute | null {
  if (path === '/') return { kind: 'dashboard' }
  if (path === '/history') return { kind: 'history' }
  if (path === '/settings') return { kind: 'settings', scope: 'global' }

  const segments = path.split('/').filter(Boolean)

  if (segments[0] === 't' && segments.length === 3) {
    return { kind: 'session-tab', tabId: segments[1], mode: validateMode(segments[2]) }
  }

  if (segments[0] === 'w' && segments.length === 2) {
    return { kind: 'workspace', workspaceId: segments[1] }
  }

  if (segments[0] === 'w' && segments[2] === 'settings' && segments.length === 3) {
    return { kind: 'workspace-settings', workspaceId: segments[1] }
  }

  if (segments[0] === 'w' && segments[2] === 't' && segments.length === 5) {
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
    case 'new-tab': return `/t/${tabId}/terminal` // fallback URL for new-tab
    case 'dashboard': return '/'
    case 'history': return '/history'
    case 'settings':
      if (content.scope === 'global') return '/settings'
      return `/w/${content.scope.workspaceId}/settings`
    case 'session':
      if (workspaceId) return `/w/${workspaceId}/t/${tabId}/${content.mode}`
      return `/t/${tabId}/${content.mode}`
  }
}
