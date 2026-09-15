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

/**
 * Identity of one pane's content, or `null` when it can never match another
 * pane (an untitled editor). A settings pane with a workspace scope resolves
 * through `wsNameById`; an id missing from it (dangling scope) keys as global,
 * mirroring the merge clone rewrite (spec §5.3 step 5).
 */
export function paneKey(content: PaneContent, wsNameById: ReadonlyMap<string, string>): string | null {
  switch (content.kind) {
    case 'tmux-session':
      return `tmux:${content.hostId}:${content.cachedName}`
    case 'editor':
      if (content.untitled) return null
      return `editor:${sourceKey(content.source)}:${content.filePath}`
    case 'image-preview':
    case 'pdf-preview':
      return `${content.kind}:${sourceKey(content.source)}:${content.filePath}`
    case 'browser':
      return `browser:${content.url}`
    case 'execution':
      return `execution:${content.host ?? ''}:${content.executionId}`
    case 'settings': {
      if (content.scope === 'global') return 'settings:global'
      const name = wsNameById.get(content.scope.workspaceId)
      return name === undefined ? 'settings:global' : `settings:ws:${name}`
    }
    case 'new-tab':
    case 'dashboard':
    case 'hosts':
    case 'history':
    case 'memory-monitor':
    case 'editor-buffers':
      return content.kind
    default: {
      const unreachable: never = content
      return unreachable
    }
  }
}

/** Leaf pane keys in pre-order joined with `|`; any `null` leaf → `null`. */
export function tabKey(tab: Tab, wsNameById: ReadonlyMap<string, string>): string | null {
  const keys: string[] = []
  let hasNull = false
  scanPaneTree(tab.layout, (pane) => {
    const key = paneKey(pane.content, wsNameById)
    if (key === null) hasNull = true
    else keys.push(key)
  })
  return hasNull ? null : keys.join('|')
}
