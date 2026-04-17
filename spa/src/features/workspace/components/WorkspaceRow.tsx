import { CaretRight, CaretDown, Plus } from '@phosphor-icons/react'
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

  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: workspace.id,
    data: { type: 'workspace', wsId: workspace.id },
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
        {...attributes}
        {...listeners}
        className={`mx-2 flex items-center gap-1 pr-1.5 rounded-md text-sm transition-colors ${
          isActive
            ? 'bg-[#8b5cf6]/25 text-text-primary ring-1 ring-purple-400'
            : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
        }`}
      >
        <button
          type="button"
          aria-label={chevronLabel}
          aria-expanded={expanded}
          onClick={(e) => {
            e.stopPropagation()
            toggleExpanded(workspace.id)
          }}
          onMouseDown={(e) => e.stopPropagation()}
          className="p-1 rounded hover:bg-surface-secondary text-text-muted cursor-pointer"
        >
          <Chevron size={12} />
        </button>
        <button
          type="button"
          onClick={() => onSelectWorkspace(workspace.id)}
          onContextMenu={(e) => {
            e.preventDefault()
            onContextMenuWorkspace?.(e, workspace.id)
          }}
          className="flex-1 flex items-center gap-2 py-1.5 text-left cursor-pointer"
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
      </div>

      {expanded && (
        <div className="flex flex-col">
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
          <button
            type="button"
            aria-label={t('nav.add_tab_to_workspace', { name: workspace.name })}
            title={t('nav.add_tab_to_workspace', { name: workspace.name })}
            onClick={() => onAddTabToWorkspace(workspace.id)}
            className="mx-2 pl-5 pr-1.5 py-1 rounded-md text-xs text-text-muted hover:bg-surface-hover hover:text-text-primary flex items-center gap-1.5 cursor-pointer"
          >
            <Plus size={12} />
            <span>{t('nav.new_tab')}</span>
          </button>
        </div>
      )}
    </div>
  )
}
