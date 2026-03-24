import type { PaneContent } from '../types/tab'

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
): string {
  switch (content.kind) {
    case 'new-tab':
      return 'New Tab'
    case 'session': {
      const session = sessionStore.getByCode(content.sessionCode)
      return session?.name ?? content.sessionCode
    }
    case 'dashboard':
      return 'Dashboard'
    case 'history':
      return 'History'
    case 'settings':
      if (content.scope === 'global') return 'Settings'
      const ws = workspaceStore.getById(content.scope.workspaceId)
      return `Settings — ${ws?.name ?? content.scope.workspaceId}`
  }
}

export function getPaneIcon(content: PaneContent): string {
  switch (content.kind) {
    case 'new-tab':
      return 'Plus'
    case 'session':
      return content.mode === 'terminal' ? 'TerminalWindow' : 'ChatCircleDots'
    case 'dashboard':
      return 'House'
    case 'history':
      return 'ClockCounterClockwise'
    case 'settings':
      return 'GearSix'
  }
}
