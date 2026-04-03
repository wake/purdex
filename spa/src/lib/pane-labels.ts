import type { PaneContent } from '../types/tab'

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
      return t('monitor.title')
  }
}

export function getPaneIcon(content: PaneContent): string {
  switch (content.kind) {
    case 'new-tab':
      return 'Plus'
    case 'tmux-session':
      return content.mode === 'terminal' ? 'TerminalWindow' : 'ChatCircleDots'
    case 'dashboard':
      return 'House'
    case 'history':
      return 'ClockCounterClockwise'
    case 'settings':
      return 'GearSix'
    case 'hosts':
      return 'HardDrives'
    case 'browser':
      return 'Globe'
    case 'memory-monitor':
      return 'ChartBar'
  }
}
