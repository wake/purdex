import { Plus, GearSix, HardDrives, SquaresFour } from '@phosphor-icons/react'
import type { Workspace } from '../../../types/tab'
import { useI18nStore } from '../../../stores/useI18nStore'
import { WorkspaceIcon } from './WorkspaceIcon'
import { workspaceColorStyle } from '../lib/workspace-colors'

interface Props {
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  activeStandaloneTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onSelectHome: () => void
  standaloneTabCount: number
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
  standaloneTabCount,
  onAddWorkspace,
  onContextMenuWorkspace,
  onOpenHosts,
  onOpenSettings,
}: Props) {
  const t = useI18nStore((s) => s.t)
  return (
    <div className="hidden lg:flex w-11 flex-col items-center bg-surface-tertiary border-r border-border-subtle py-2 gap-2 flex-shrink-0">
      {/* Home — standalone tabs */}
      <button
        title={t('nav.home')}
        onClick={onSelectHome}
        className={`relative w-8 h-8 rounded-lg flex items-center justify-center cursor-pointer transition-all ${
          !activeWorkspaceId
            ? 'bg-accent text-white'
            : 'bg-surface-secondary text-text-secondary hover:text-text-primary hover:bg-surface-tertiary'
        }`}
      >
        <SquaresFour size={18} weight={!activeWorkspaceId ? 'fill' : 'regular'} />
        {activeWorkspaceId && standaloneTabCount > 0 && (
          <span className="absolute -top-1 -right-1 min-w-[16px] h-4 rounded-full bg-red-500 text-white text-[10px] font-bold flex items-center justify-center px-0.5">
            {standaloneTabCount}
          </span>
        )}
      </button>

      {workspaces.length > 0 && <div className="w-5 h-px bg-border-default my-0.5" />}

      {/* Workspaces */}
      {workspaces.map((ws) => {
        const cs = workspaceColorStyle(ws.color)
        const isActive = activeWorkspaceId === ws.id && !activeStandaloneTabId
        return (
          <button
            key={ws.id}
            title={ws.name}
            onClick={() => onSelectWorkspace(ws.id)}
            onContextMenu={(e) => {
              e.preventDefault()
              onContextMenuWorkspace?.(e, ws.id)
            }}
            className={`w-8 h-8 rounded-md flex items-center justify-center text-xs cursor-pointer transition-all ${
              isActive ? 'ring-2' : 'opacity-70 hover:opacity-100'
            }`}
            style={{
              backgroundColor: cs.bg,
              color: cs.fg,
              ...(isActive ? { '--tw-ring-color': cs.border } as React.CSSProperties : {}),
            }}
          >
            <WorkspaceIcon icon={ws.icon} name={ws.name} size={14} weight={ws.iconWeight} />
          </button>
        )
      })}

      {/* Add + Settings */}
      <div className="mt-auto flex flex-col items-center gap-2 pb-1">
        <button
          title={t('nav.new_workspace')}
          onClick={onAddWorkspace}
          className="w-8 h-8 rounded-md flex items-center justify-center text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer"
        >
          <Plus size={16} />
        </button>
        <button
          title={t('nav.hosts')}
          onClick={onOpenHosts}
          className="w-8 h-8 rounded-md flex items-center justify-center text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer"
        >
          <HardDrives size={16} />
        </button>
        <button
          title={t('nav.settings')}
          onClick={onOpenSettings}
          className="w-8 h-8 rounded-md flex items-center justify-center text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer"
        >
          <GearSix size={16} />
        </button>
      </div>
    </div>
  )
}
