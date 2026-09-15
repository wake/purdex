// spa/src/lib/device-state/identity.ts — identity keys for device-state merge
// (spec §5.2). Pure: two tabs with the same key are "the same tab" for merge.

import type { FileSource } from '../../types/fs'
import type { PaneContent, Tab } from '../../types/tab'
import { scanPaneTree } from '../pane-tree'

export function sourceKey(source: FileSource): string {
  switch (source.type) {
    case 'daemon':
      return `daemon:${source.hostId}`
    case 'local':
      return 'local'
    case 'inapp':
      return 'inapp'
  }
}

/** Collision-free encoding: each part is a JSON string, so no delimiter can leak across parts. */
function encode(...parts: string[]): string {
  return JSON.stringify(parts)
}

const SETTINGS_GLOBAL = encode('settings', 'global')

/**
 * Identity of one pane's content, or `null` when it can never match another
 * pane (an untitled editor). A settings pane with a workspace scope resolves
 * through `wsNameById` by trimmed name (merge matches workspaces by trimmed
 * name, spec §5.3); an id missing from it, or a name empty after trim
 * (dangling scope), keys as global, mirroring the merge clone rewrite
 * (spec §5.3 step 5).
 */
export function paneKey(content: PaneContent, wsNameById: ReadonlyMap<string, string>): string | null {
  switch (content.kind) {
    case 'tmux-session':
      return encode('tmux', content.hostId, content.cachedName)
    case 'editor':
      if (content.untitled) return null
      return encode('editor', sourceKey(content.source), content.filePath)
    case 'image-preview':
    case 'pdf-preview':
      return encode(content.kind, sourceKey(content.source), content.filePath)
    case 'browser':
      return encode('browser', content.url)
    case 'execution':
      return encode('execution', content.host ?? '', content.executionId)
    case 'settings': {
      if (content.scope === 'global') return SETTINGS_GLOBAL
      const name = wsNameById.get(content.scope.workspaceId)?.trim()
      return name ? encode('settings', 'ws', name) : SETTINGS_GLOBAL
    }
    case 'new-tab':
    case 'dashboard':
    case 'hosts':
    case 'history':
    case 'memory-monitor':
    case 'editor-buffers':
      return encode(content.kind)
    default: {
      const unreachable: never = content
      return unreachable
    }
  }
}

/** JSON array of leaf pane keys in pre-order; any `null` leaf → `null`. */
export function tabKey(tab: Tab, wsNameById: ReadonlyMap<string, string>): string | null {
  const keys: string[] = []
  let hasNull = false
  scanPaneTree(tab.layout, (pane) => {
    const key = paneKey(pane.content, wsNameById)
    if (key === null) hasNull = true
    else keys.push(key)
  })
  return hasNull ? null : JSON.stringify(keys)
}
