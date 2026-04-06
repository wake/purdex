import { Plus, GearSix, HardDrives } from '@phosphor-icons/react'
import type { Tab, Workspace } from '../../../types/tab'
import { getPrimaryPane } from '../../../lib/pane-tree'
import { getPaneLabel } from '../../../lib/pane-labels'
import { useI18nStore } from '../../../stores/useI18nStore'

const emptySessionLookup = { getByCode: () => undefined }
const emptyWorkspaceLookup = { getById: () => undefined }

interface Props {
  workspaces: Workspace[]
  standaloneTabs: Tab[]
  activeWorkspaceId: string | null
  activeStandaloneTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onSelectStandaloneTab: (tabId: string) => void
  onAddWorkspace: () => void
  onContextMenuWorkspace?: (e: React.MouseEvent, wsId: string) => void
  onOpenHosts: () => void
  onOpenSettings: () => void
}

export function ActivityBar({
  workspaces,
  standaloneTabs,
  activeWorkspaceId,
  activeStandaloneTabId,
  onSelectWorkspace,
  onSelectStandaloneTab,
  onAddWorkspace,
  onContextMenuWorkspace,
  onOpenHosts,
  onOpenSettings,
}: Props) {
  const t = useI18nStore((s) => s.t)
  return (
    <div className="hidden lg:flex w-11 flex-col items-center bg-surface-tertiary border-r border-border-subtle py-2 gap-2 flex-shrink-0">
      {/* Workspaces */}
      {workspaces.map((ws) => (
        <button
          key={ws.id}
          title={ws.name}
          onClick={() => onSelectWorkspace(ws.id)}
          onContextMenu={(e) => {
            e.preventDefault()
            onContextMenuWorkspace?.(e, ws.id)
          }}
          className={`w-8 h-8 rounded-md flex items-center justify-center text-xs cursor-pointer transition-all ${
            activeWorkspaceId === ws.id && !activeStandaloneTabId
              ? 'ring-2 ring-purple-400'
              : 'opacity-70 hover:opacity-100'
          }`}
          style={{ backgroundColor: ws.color + '33', color: ws.color }}
        >
          {ws.icon ?? ws.name.charAt(0)}
        </button>
      ))}

      {/* Separator */}
      {standaloneTabs.length > 0 && (
        <div className="w-5 h-px bg-border-default my-1" />
      )}

      {/* Standalone tabs */}
      {standaloneTabs.map((tab) => {
        const label = getPaneLabel(
          getPrimaryPane(tab.layout).content,
          emptySessionLookup,
          emptyWorkspaceLookup,
          t,
        )
        return (
          <button
            key={tab.id}
            title={label}
            onClick={() => onSelectStandaloneTab(tab.id)}
            className={`w-8 h-8 rounded-md flex items-center justify-center text-xs cursor-pointer transition-all ${
              activeStandaloneTabId === tab.id
                ? 'ring-2 ring-accent bg-surface-secondary'
                : 'bg-surface-tertiary opacity-70 hover:opacity-100'
            }`}
          >
            {label.charAt(0).toUpperCase()}
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
