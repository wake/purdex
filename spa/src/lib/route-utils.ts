import type { PaneContent } from '../types/tab'
import { decodeHostRouteId, isHostSubPage, type HostSubPage } from './host-routes'
import { SETTINGS_LOCAL_ID_RE } from './settings-contribution-types'

export type ParsedRoute =
  | { kind: 'history' }
  | { kind: 'hosts'; hostId?: string; subPage?: HostSubPage }
  | { kind: 'hosts-invalid'; hostId?: string; subPage?: string }
  | { kind: 'settings'; scope: 'global'; section?: string; subsection?: string }
  | { kind: 'session-tab'; tabId: string; mode: 'terminal' | 'stream' }
  | { kind: 'workspace'; workspaceId: string }
  | { kind: 'workspace-settings'; workspaceId: string }
  | { kind: 'workspace-session-tab'; workspaceId: string; tabId: string; mode: 'terminal' | 'stream' }

const ID_PATTERN = /^[0-9a-z]{6}$/
// F6: share the source of truth for settings id grammar with the
// contribution registry so a registration that passes `assertValid…` is
// also guaranteed to round-trip through parseRoute(). Subsection uses
// the same pattern today.
const SETTINGS_SECTION_PATTERN = SETTINGS_LOCAL_ID_RE
const SETTINGS_SUBSECTION_PATTERN = SETTINGS_LOCAL_ID_RE

function validateMode(mode: string): 'terminal' | 'stream' {
  return mode === 'stream' ? 'stream' : 'terminal'
}

export function parseRoute(path: string): ParsedRoute | null {
  if (path === '/') return null // no-op — preserves persisted tab state
  if (path === '/history') return { kind: 'history' }
  if (path === '/hosts') return { kind: 'hosts' }
  if (path === '/hosts/') return { kind: 'hosts' }
  if (path.startsWith('/hosts/')) {
    const segments = path.split('/').filter(Boolean)
    const hostId = segments[1] ? decodeHostRouteId(segments[1]) : null
    const subPage = segments[2]

    if (segments.length === 2) {
      return hostId ? { kind: 'hosts-invalid', hostId } : null
    }

    if (segments.length === 3 && hostId && subPage && isHostSubPage(subPage)) {
      return { kind: 'hosts', hostId, subPage }
    }

    if (hostId && subPage) {
      return { kind: 'hosts-invalid', hostId, subPage }
    }

    return hostId ? { kind: 'hosts-invalid', hostId } : null
  }
  if (path === '/settings') return { kind: 'settings', scope: 'global' }
  if (path.startsWith('/settings/')) {
    const rest = path.slice('/settings/'.length)
    const parts = rest.split('/')
    if (parts.length === 1 && SETTINGS_SECTION_PATTERN.test(parts[0])) {
      return { kind: 'settings', scope: 'global', section: parts[0] }
    }
    if (
      parts.length === 2 &&
      SETTINGS_SECTION_PATTERN.test(parts[0]) &&
      SETTINGS_SUBSECTION_PATTERN.test(parts[1])
    ) {
      return { kind: 'settings', scope: 'global', section: parts[0], subsection: parts[1] }
    }
    return { kind: 'settings', scope: 'global' }
  }

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
    case 'editor':
      return '/'
    case 'editor-buffers':
      return '/'
    case 'image-preview':
      return '/'
    case 'pdf-preview':
      return '/'
  }
}
