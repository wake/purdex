import type { PaneContent } from '../types/tab'
import { fileIconForPath } from './file-icon'
import { oneLine } from './nex/worker-tab-title'

export type TFunction = (key: string, params?: Record<string, string | number>) => string

interface SessionLookup {
  getByCode(code: string): { name: string } | undefined
}

interface WorkspaceLookup {
  getById(id: string): { name: string } | undefined
}

export function getPaneLabel(
  content: PaneContent,
  sessionStore: SessionLookup,
  workspaceStore: WorkspaceLookup,
  t: TFunction,
): string {
  switch (content.kind) {
    case 'new-tab':
      return t('page.pane.new_tab')
    case 'tmux-session': {
      if (content.terminated) {
        // A conversation's rebuild tab (conversation entity spec §13.4) is named by its conversation: it never had a
        // session of its own, and its generated session name says nothing the user would recognise.
        const title = content.terminated === 'conversation-ended' ? oneLine(content.conversation?.title) : ''
        return t('page.pane.terminated', { name: title || content.cachedName || content.sessionCode })
      }
      const session = sessionStore.getByCode(content.sessionCode)
      return session?.name ?? (content.cachedName || content.sessionCode)
    }
    case 'dashboard':
      return t('page.pane.dashboard')
    case 'history':
      return t('page.pane.history')
    case 'settings': {
      if (content.scope === 'global') return t('page.pane.settings')
      const ws = workspaceStore.getById(content.scope.workspaceId)
      return t('page.pane.settings_ws', { name: ws?.name ?? content.scope.workspaceId })
    }
    case 'browser': {
      try { return new URL(content.url).hostname } catch { return content.url }
    }
    case 'hosts':
      return t('page.pane.hosts')
    case 'memory-monitor':
      return t('performance_monitor.title')
    case 'editor-buffers':
      return t('editor.buffers.tab_title')
    case 'editor': {
      const name = content.untitled?.name ?? (content.filePath.split('/').pop() ?? content.filePath)
      return content.diff ? `${name} (Diff)` : name
    }
    case 'image-preview':
    case 'pdf-preview':
      return content.filePath.split('/').pop() ?? content.filePath
    case 'execution':
      return t('page.pane.execution')
  }
}

export function getPaneIcon(content: PaneContent): string {
  switch (content.kind) {
    case 'new-tab':
      return 'Plus'
    case 'tmux-session':
      if (content.terminated) return 'SmileySad'
      return 'TerminalWindow'
    case 'dashboard':
      return 'House'
    case 'history':
      return 'ClockCounterClockwise'
    case 'settings':
      return 'Sliders'
    case 'hosts':
      return 'HardDrives'
    case 'browser':
      return 'Globe'
    case 'memory-monitor':
      return 'ChartBar'
    case 'editor-buffers':
      return 'Stack'
    case 'editor':
      if (content.diff) return 'GitDiff'
      if (content.source.type === 'inapp') return fileIconForPath(content.filePath)
      return 'TextAlignLeft'
    case 'image-preview':
      if (content.source.type === 'inapp') return fileIconForPath(content.filePath)
      return 'Image'
    case 'pdf-preview':
      if (content.source.type === 'inapp') return fileIconForPath(content.filePath)
      return 'FilePdf'
    case 'execution':
      return 'Robot'
  }
}
