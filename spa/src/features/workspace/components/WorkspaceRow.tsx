import { CaretRight, CaretDown, Plus } from '@phosphor-icons/react'
import { useDroppable } from '@dnd-kit/core'
import { useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import type { Workspace, Tab } from '../../../types/tab'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { WorkspaceIcon } from './WorkspaceIcon'
import { InlineTabList } from './InlineTabList'

interface Props {
  workspace: Workspace
  isActive: boolean
  tabsById: Record<string, Tab>
  activeTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onContextMenuWorkspace?: (e: React.MouseEvent, wsId: string) => void
  onSelectTab: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  onMiddleClickTab: (tabId: string) => void
  onContextMenuTab: (e: React.MouseEvent, tabId: string) => void
  onAddTabToWorkspace: (wsId: string) => void
}

export function WorkspaceRow(props: Props) {
  const {
    workspace,
    isActive,
    tabsById,
    activeTabId,
    onSelectWorkspace,
    onContextMenuWorkspace,
    onSelectTab,
    onCloseTab,
    onMiddleClickTab,
    onContextMenuTab,
    onAddTabToWorkspace,
  } = props
  const t = useI18nStore((s) => s.t)
  const expanded = useLayoutStore((s) => !!s.workspaceExpanded[workspace.id])
  const toggleExpanded = useLayoutStore((s) => s.toggleWorkspaceExpanded)
  const tabPosition = useLayoutStore((s) => s.tabPosition)
  const showTabs = tabPosition !== 'top'

  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: workspace.id,
    data: { type: 'workspace', wsId: workspace.id },
  })

  const { setNodeRef: setHeaderDropRef, isOver: isHeaderOver } = useDroppable({
    id: `ws-header-${workspace.id}`,
    data: { type: 'workspace-header', wsId: workspace.id },
  })

  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  }

  const Chevron = expanded ? CaretDown : CaretRight
  const chevronLabel = expanded
    ? `Collapse ${workspace.name}`
    : `Expand ${workspace.name}`

  return (
    <div ref={setNodeRef} style={style} className="flex flex-col">
      <div
        ref={setHeaderDropRef}
        data-testid={`ws-header-${workspace.id}`}
        {...attributes}
        {...listeners}
        className={`group/ws-header mx-2 flex items-center gap-1 pl-1.5 rounded-md text-sm transition-colors focus:outline-none focus-visible:outline-none ${
          isActive
            ? 'bg-[#8b5cf6]/25 text-text-primary ring-1 ring-purple-400'
            : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
        } ${isHeaderOver ? 'ring-2 ring-purple-400/80 bg-surface-hover' : ''}`}
      >
        <button
          type="button"
          onClick={() => onSelectWorkspace(workspace.id)}
          onPointerDown={(e) => e.stopPropagation()}
          onContextMenu={(e) => {
            e.preventDefault()
            onContextMenuWorkspace?.(e, workspace.id)
          }}
          className="flex-1 flex items-center gap-2 py-1.5 text-left cursor-pointer focus:outline-none"
        >
          <WorkspaceIcon
            icon={workspace.icon}
            name={workspace.name}
            size={16}
            weight={workspace.iconWeight}
          />
          <span className="truncate" title={workspace.name}>
            {workspace.name}
          </span>
        </button>
        {showTabs && (
          <button
            type="button"
            aria-label={t('nav.add_tab_to_workspace', { name: workspace.name })}
            title={t('nav.add_tab_to_workspace', { name: workspace.name })}
            onClick={(e) => {
              e.stopPropagation()
              onAddTabToWorkspace(workspace.id)
            }}
            onPointerDown={(e) => e.stopPropagation()}
            className="p-1 rounded hover:bg-surface-secondary text-text-primary cursor-pointer opacity-0 group-hover/ws-header:opacity-100 focus:opacity-100 transition-opacity focus:outline-none"
          >
            <Plus size={14} weight="bold" />
          </button>
        )}
        {showTabs && (
          <button
            type="button"
            aria-label={chevronLabel}
            aria-expanded={expanded}
            onClick={(e) => {
              e.stopPropagation()
              toggleExpanded(workspace.id)
            }}
            className="p-1 mr-0.5 rounded hover:bg-surface-secondary text-text-muted cursor-pointer focus:outline-none"
          >
            <Chevron size={12} />
          </button>
        )}
      </div>

      {showTabs && expanded && (
        <InlineTabList
          tabIds={workspace.tabs}
          tabsById={tabsById}
          activeTabId={activeTabId}
          sourceWsId={workspace.id}
          onSelect={onSelectTab}
          onClose={onCloseTab}
          onMiddleClick={onMiddleClickTab}
          onContextMenu={onContextMenuTab}
        />
      )}
    </div>
  )
}
