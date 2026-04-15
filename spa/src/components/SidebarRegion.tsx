import { Fragment, useState } from 'react'
import { CaretLeft, CaretRight, GearSix, Plus } from '@phosphor-icons/react'
import { useLayoutStore } from '../stores/useLayoutStore'
import { getViewDefinition } from '../lib/module-registry'
import { RegionResize } from './RegionResize'
import { RegionManager } from './RegionManager'
import { RegionContextMenu } from './RegionContextMenu'
import type { SidebarRegion as SidebarRegionType } from '../types/layout'
import { useWorkspaceStore } from '../features/workspace/store'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'

interface Props {
  region: SidebarRegionType
  resizeEdge: 'left' | 'right'
}

export function SidebarRegion({ region, resizeEdge }: Props) {
  const regionState = useLayoutStore((s) => s.regions[region])
  const setRegionWidth = useLayoutStore((s) => s.setRegionWidth)
  const toggleRegion = useLayoutStore((s) => s.toggleRegion)
  const setActiveView = useLayoutStore((s) => s.setActiveView)

  const { views, activeViewId, width, mode } = regionState
  const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)
  const activeHostId = useHostStore((s) => s.activeHostId ?? s.hostOrder[0] ?? '')
  const activeTabId = useTabStore((s) => s.activeTabId) ?? undefined

  const [managing, setManaging] = useState(false)
  const [contextMenuPos, setContextMenuPos] = useState<{ x: number; y: number } | null>(null)

  if (mode === 'hidden') return null

  const handleContextMenu = (e: React.MouseEvent) => {
    e.preventDefault()
    setContextMenuPos({ x: e.clientX, y: e.clientY })
  }

  const openManageMode = () => {
    if (mode === 'collapsed') {
      toggleRegion(region)
    }
    setManaging(true)
  }

  // Fallback to first view if activeViewId is unset
  const resolvedActiveViewId = activeViewId ?? views[0]
  const activeView = resolvedActiveViewId ? getViewDefinition(resolvedActiveViewId) : undefined

  if (mode === 'collapsed') {
    return (
      <Fragment>
        <div
          data-testid="collapsed-bar"
          className="shrink-0 w-6 bg-surface-tertiary border-border-subtle flex flex-col items-center pt-2 gap-1 cursor-pointer hover:bg-surface-hover transition-colors"
          style={{ borderLeftWidth: resizeEdge === 'left' ? 1 : 0, borderRightWidth: resizeEdge === 'right' ? 1 : 0 }}
          onClick={() => toggleRegion(region)}
          onContextMenu={handleContextMenu}
        >
          {views.map((viewId) => {
            const viewDef = getViewDefinition(viewId)
            if (!viewDef) return null
            const Icon = viewDef.icon
            return (
              <div
                key={viewId}
                className={`w-5 h-5 flex items-center justify-center rounded ${
                  viewId === resolvedActiveViewId ? 'text-text-primary' : 'text-text-muted'
                }`}
                title={viewDef.label}
              >
                <Icon size={14} />
              </div>
            )
          })}
          <button
            data-testid="add-view-button"
            className="w-5 h-5 flex items-center justify-center rounded text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors mt-auto mb-1"
            title="管理 views"
            onClick={(e) => {
              e.stopPropagation()
              openManageMode()
            }}
          >
            <Plus size={12} />
          </button>
        </div>
        {contextMenuPos && (
          <RegionContextMenu
            region={region}
            position={contextMenuPos}
            onClose={() => setContextMenuPos(null)}
          />
        )}
      </Fragment>
    )
  }

  const ActiveComponent = activeView?.component
  const CollapseIcon = resizeEdge === 'right' ? CaretLeft : CaretRight

  const resizeHandle = (
    <RegionResize
      resizeEdge={resizeEdge}
      onResize={(delta) => setRegionWidth(region, width + delta)}
    />
  )

  return (
    <div className="shrink-0 flex" style={{ width }}>
      {resizeEdge === 'left' && resizeHandle}
      <div
        className="flex-1 flex flex-col min-w-0 bg-surface-tertiary border-border-subtle"
        style={{ borderLeftWidth: resizeEdge === 'right' ? 1 : 0, borderRightWidth: resizeEdge === 'left' ? 1 : 0 }}
      >
        <div className="shrink-0 flex items-center gap-0.5 px-1 py-1 border-b border-border-subtle">
          {views.length > 1 && views.map((viewId) => {
            const viewDef = getViewDefinition(viewId)
            if (!viewDef) return null
            return (
              <button
                key={viewId}
                className={`px-2 py-0.5 rounded text-xs transition-colors ${
                  viewId === resolvedActiveViewId
                    ? 'bg-surface-active text-text-primary'
                    : 'text-text-muted hover:text-text-primary hover:bg-surface-hover'
                }`}
                onClick={() => setActiveView(region, viewId)}
              >
                {viewDef.label}
              </button>
            )
          })}
          <div className="flex-1" />
          <button
            data-testid="manage-button"
            className={`p-0.5 rounded transition-colors ${
              managing
                ? 'text-accent-base bg-accent-base/10'
                : 'text-text-muted hover:text-text-primary hover:bg-surface-hover'
            }`}
            onClick={() => setManaging((m) => !m)}
            title="管理 views"
          >
            <GearSix size={12} />
          </button>
          <button
            data-testid="collapse-button"
            className="p-0.5 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors"
            onClick={() => {
              setManaging(false)
              toggleRegion(region)
            }}
            title="Collapse"
          >
            <CollapseIcon size={12} />
          </button>
        </div>
        <div
          className="flex-1 overflow-hidden overflow-y-auto"
          onContextMenu={handleContextMenu}
        >
          {managing ? (
            <RegionManager region={region} />
          ) : views.length === 0 ? (
            <div className="flex items-center justify-center h-full text-text-muted text-xs p-4 text-center">
              加入 views
            </div>
          ) : (
            ActiveComponent && (
              <ActiveComponent
                isActive={true}
                region={region}
                workspaceId={activeWorkspaceId ?? undefined}
                hostId={activeHostId || undefined}
                tabId={activeTabId}
              />
            )
          )}
        </div>
      </div>
      {resizeEdge === 'right' && resizeHandle}
      {contextMenuPos && (
        <RegionContextMenu
          region={region}
          position={contextMenuPos}
          onClose={() => setContextMenuPos(null)}
        />
      )}
    </div>
  )
}
