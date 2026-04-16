import { useRef, useState } from 'react'
import { Plus, GearSix, HardDrives } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { WorkspaceIcon } from './WorkspaceIcon'
import { CollapseButton } from './CollapseButton'
import { RegionResize } from '../../../components/RegionResize'
import type { ActivityBarProps } from './activity-bar-props'

const MIN_WIDE_SIZE = 120
const MAX_WIDE_SIZE = 600

export function ActivityBarWide(props: ActivityBarProps) {
  const {
    workspaces,
    activeWorkspaceId,
    activeStandaloneTabId,
    onSelectWorkspace,
    onSelectHome,
    onAddWorkspace,
    onContextMenuWorkspace,
    onOpenHosts,
    onOpenSettings,
  } = props
  const t = useI18nStore((s) => s.t)
  const wideSize = useLayoutStore((s) => s.activityBarWideSize)
  const setWideSize = useLayoutStore((s) => s.setActivityBarWideSize)
  const isHomeActive = !activeWorkspaceId

  // Ephemeral drag state — avoid persisting + broadcasting on every mousemove.
  // Commit to store only on mouseup (see RegionResize.onResizeEnd).
  const [draftSize, setDraftSize] = useState<number | null>(null)
  const draftSizeRef = useRef<number | null>(null)
  const renderedSize = draftSize ?? wideSize

  return (
    <>
    <div
      className="hidden lg:flex flex-col bg-surface-tertiary border-r border-border-subtle py-2 gap-1 flex-shrink-0"
      style={{ width: renderedSize }}
    >
      {/* Home row */}
      <button
        onClick={onSelectHome}
        className={`mx-2 flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-left cursor-pointer transition-all ${
          isHomeActive && !activeStandaloneTabId
            ? 'bg-surface-hover text-text-primary ring-1 ring-purple-400'
            : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
        }`}
      >
        <img src="/icons/logo-transparent.png" alt="" width={18} height={18} className="rounded-sm" />
        <span className="truncate">{t('nav.home')}</span>
      </button>

      {workspaces.length > 0 && <div className="mx-3 my-1 h-px bg-border-default" />}

      {/* Workspace rows */}
      <div className="flex flex-col gap-0.5">
        {workspaces.map((ws) => {
          const isActive = activeWorkspaceId === ws.id && !activeStandaloneTabId
          return (
            <button
              key={ws.id}
              onClick={() => onSelectWorkspace(ws.id)}
              onContextMenu={(e) => {
                e.preventDefault()
                onContextMenuWorkspace?.(e, ws.id)
              }}
              className={`mx-2 flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-left cursor-pointer transition-all ${
                isActive
                  ? 'bg-[#8b5cf6]/25 text-text-primary ring-1 ring-purple-400'
                  : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
              }`}
            >
              <WorkspaceIcon icon={ws.icon} name={ws.name} size={16} weight={ws.iconWeight} />
              <span className="truncate" title={ws.name}>{ws.name}</span>
            </button>
          )
        })}
      </div>

      {/* Bottom controls */}
      <div className="mt-auto flex flex-col gap-1 px-2 pb-1">
        <div className="flex items-center justify-end">
          <CollapseButton />
        </div>
        <button
          title={t('nav.new_workspace')}
          onClick={onAddWorkspace}
          className="flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
        >
          <Plus size={16} />
          <span className="truncate">{t('nav.new_workspace')}</span>
        </button>
        <button
          title={t('nav.hosts')}
          onClick={onOpenHosts}
          className="flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
        >
          <HardDrives size={16} />
          <span className="truncate">{t('nav.hosts')}</span>
        </button>
        <button
          title={t('nav.settings')}
          onClick={onOpenSettings}
          className="flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
        >
          <GearSix size={16} />
          <span className="truncate">{t('nav.settings')}</span>
        </button>
      </div>
    </div>
    <div data-testid="activity-bar-resize" className="hidden lg:block">
      <RegionResize
        resizeEdge="right"
        onResize={(delta) => {
          // Read the latest committed value rather than a stale closure;
          // accumulate into an ephemeral local value while dragging.
          const base = draftSizeRef.current ?? useLayoutStore.getState().activityBarWideSize
          const next = Math.max(MIN_WIDE_SIZE, Math.min(MAX_WIDE_SIZE, base + delta))
          draftSizeRef.current = next
          setDraftSize(next)
        }}
        onResizeEnd={() => {
          if (draftSizeRef.current !== null) {
            setWideSize(draftSizeRef.current)
            draftSizeRef.current = null
            setDraftSize(null)
          }
        }}
      />
    </div>
    </>
  )
}
