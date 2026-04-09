import { Plus, GearSix, HardDrives, SquaresFour } from '@phosphor-icons/react'
import type { Workspace } from '../../../types/tab'
import { useI18nStore } from '../../../stores/useI18nStore'
import { WorkspaceIcon } from './WorkspaceIcon'
import { useWorkspaceIndicators } from '../useWorkspaceIndicators'
import type { ActiveStatus } from '../workspace-indicators'

interface WorkspaceButtonProps {
  workspace: Workspace
  isActive: boolean
  onSelect: (wsId: string) => void
  onContextMenu?: (e: React.MouseEvent, wsId: string) => void
}

const PILL_COLORS: Record<ActiveStatus, string> = {
  running: '#4ade80',
  waiting: '#facc15',
  error: '#ef4444',
}

function WorkspaceButton({ workspace: ws, isActive, onSelect, onContextMenu }: WorkspaceButtonProps) {
  const { unreadCount, aggregatedStatus } = useWorkspaceIndicators(ws.tabs)
  const showBadge = !isActive && unreadCount > 0

  return (
    <div className="relative group">
      {aggregatedStatus && !isActive && (
        <span
          className={`absolute rounded-full ${aggregatedStatus === 'running' ? 'animate-breathe' : ''}`}
          style={{
            width: '5px',
            height: '5px',
            left: '-1px',
            top: '50%',
            transform: 'translateY(calc(-50% - 1px))',
            backgroundColor: PILL_COLORS[aggregatedStatus],
            boxShadow: '0 0 0 1.5px var(--surface-tertiary)',
            '--breathe-color': PILL_COLORS[aggregatedStatus],
            '--breathe-bg': 'var(--surface-tertiary)',
          } as React.CSSProperties}
        />
      )}
      <button
        aria-label={[
          ws.name,
          showBadge && `${unreadCount} unread`,
          aggregatedStatus && aggregatedStatus !== 'idle' && aggregatedStatus,
        ].filter(Boolean).join(', ')}
        onClick={() => onSelect(ws.id)}
        onContextMenu={(e) => {
          e.preventDefault()
          onContextMenu?.(e, ws.id)
        }}
        className={`w-[30px] h-[30px] rounded-md flex items-center justify-center text-sm cursor-pointer transition-all ${
          isActive
            ? 'bg-[#8b5cf6]/35 text-text-primary ring-2 ring-purple-400'
            : 'bg-surface-secondary text-text-secondary hover:bg-surface-hover hover:text-text-primary'
        }`}
      >
        <WorkspaceIcon icon={ws.icon} name={ws.name} size={16} weight={ws.iconWeight} />
      </button>
      {showBadge && (
        <span
          data-testid="ws-unread-badge"
          className="absolute -top-[5px] -right-[6px] min-w-[15px] h-[15px] rounded-full flex items-center justify-center text-white text-[9px] font-bold px-[3px] leading-none z-10"
          style={{ backgroundColor: '#dc2626', boxShadow: '0 0 0 2px var(--surface-tertiary)' }}
        >
          {unreadCount}
        </span>
      )}
      <span className="pointer-events-none absolute left-full ml-2 top-1/2 -translate-y-1/2 whitespace-nowrap rounded bg-surface-secondary border border-border-default px-2 py-1 text-xs text-text-primary shadow-lg opacity-0 group-hover:opacity-100 transition-opacity z-50">
        {ws.name}
      </span>
    </div>
  )
}

interface Props {
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  activeStandaloneTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onSelectHome: () => void
  standaloneTabIds: string[]
  onAddWorkspace: () => void
  onContextMenuWorkspace?: (e: React.MouseEvent, wsId: string) => void
  onOpenHosts: () => void
  onOpenSettings: () => void
}

export function ActivityBar({
  workspaces,
  activeWorkspaceId,
  activeStandaloneTabId,
  onSelectWorkspace,
  onSelectHome,
  standaloneTabIds,
  onAddWorkspace,
  onContextMenuWorkspace,
  onOpenHosts,
  onOpenSettings,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const { unreadCount: homeUnreadCount, aggregatedStatus: homeStatus } = useWorkspaceIndicators(standaloneTabIds)
  const isHomeActive = !activeWorkspaceId
  const showHomeBadge = !isHomeActive && homeUnreadCount > 0
  return (
    <div className="hidden lg:flex w-11 flex-col items-center bg-surface-tertiary border-r border-border-subtle py-2 px-px gap-2.5 flex-shrink-0">
      {/* Home — standalone tabs */}
      <div className="relative group">
        {homeStatus && !isHomeActive && (
          <span
            className={`absolute rounded-full ${homeStatus === 'running' ? 'animate-breathe' : ''}`}
            style={{
              width: '5px',
              height: '5px',
              left: '-1px',
              top: '50%',
              transform: 'translateY(calc(-50% - 1px))',
              backgroundColor: PILL_COLORS[homeStatus],
              boxShadow: '0 0 0 1.5px var(--surface-tertiary)',
              '--breathe-color': PILL_COLORS[homeStatus],
              '--breathe-bg': 'var(--surface-tertiary)',
            } as React.CSSProperties}
          />
        )}
        <button
          title={t('nav.home')}
          onClick={onSelectHome}
          className={`w-[30px] h-[30px] rounded-lg flex items-center justify-center cursor-pointer transition-all ${
            isHomeActive
              ? 'bg-accent text-white'
              : 'bg-surface-secondary text-text-secondary hover:text-text-primary hover:bg-surface-tertiary'
          }`}
        >
          <SquaresFour size={18} weight={isHomeActive ? 'fill' : 'regular'} />
        </button>
        {showHomeBadge && (
          <span
            data-testid="home-unread-badge"
            className="absolute -top-[5px] -right-[6px] min-w-[15px] h-[15px] rounded-full flex items-center justify-center text-white text-[9px] font-bold px-[3px] leading-none z-10"
            style={{ backgroundColor: '#dc2626', boxShadow: '0 0 0 2px var(--surface-tertiary)' }}
          >
            {homeUnreadCount}
          </span>
        )}
      </div>

      {workspaces.length > 0 && <div className="w-5 h-px bg-border-default my-0.5" />}

      {/* Workspaces */}
      {workspaces.map((ws) => (
        <WorkspaceButton
          key={ws.id}
          workspace={ws}
          isActive={activeWorkspaceId === ws.id && !activeStandaloneTabId}
          onSelect={onSelectWorkspace}
          onContextMenu={onContextMenuWorkspace}
        />
      ))}

      {/* Add + Settings */}
      <div className="mt-auto flex flex-col items-center gap-2 pb-1">
        <button
          title={t('nav.new_workspace')}
          onClick={onAddWorkspace}
          className="w-[30px] h-[30px] rounded-md flex items-center justify-center text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer"
        >
          <Plus size={16} />
        </button>
        <button
          title={t('nav.hosts')}
          onClick={onOpenHosts}
          className="w-[30px] h-[30px] rounded-md flex items-center justify-center text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer"
        >
          <HardDrives size={16} />
        </button>
        <button
          title={t('nav.settings')}
          onClick={onOpenSettings}
          className="w-[30px] h-[30px] rounded-md flex items-center justify-center text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer"
        >
          <GearSix size={16} />
        </button>
      </div>
    </div>
  )
}
