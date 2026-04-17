import { CaretRight, CaretDown } from '@phosphor-icons/react'
import { useDroppable } from '@dnd-kit/core'
import type { Tab } from '../../../types/tab'
import { useLayoutStore, HOME_WS_KEY } from '../../../stores/useLayoutStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { InlineTabList } from './InlineTabList'

interface Props {
  isActive: boolean
  standaloneTabIds: string[]
  tabsById: Record<string, Tab>
  activeTabId: string | null
  onSelectHome: () => void
  onSelectTab: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  onMiddleClickTab: (tabId: string) => void
  onContextMenuTab: (e: React.MouseEvent, tabId: string) => void
}

export function HomeRow(props: Props) {
  const {
    isActive,
    standaloneTabIds,
    tabsById,
    activeTabId,
    onSelectHome,
    onSelectTab,
    onCloseTab,
    onMiddleClickTab,
    onContextMenuTab,
  } = props
  const t = useI18nStore((s) => s.t)
  const expanded = useLayoutStore((s) => !!s.workspaceExpanded[HOME_WS_KEY])
  const toggleExpanded = useLayoutStore((s) => s.toggleWorkspaceExpanded)

  const { setNodeRef: setHeaderDropRef, isOver: isHeaderOver } = useDroppable({
    id: 'home-header',
    data: { type: 'home-header' },
  })

  const Chevron = expanded ? CaretDown : CaretRight
  const chevronLabel = expanded ? 'Collapse Home' : 'Expand Home'

  return (
    <div className="flex flex-col">
      <div
        ref={setHeaderDropRef}
        data-testid="home-header"
        className={`mx-2 flex items-center gap-1 pr-1.5 rounded-md text-sm transition-colors ${
          isActive
            ? 'bg-surface-hover text-text-primary ring-1 ring-purple-400'
            : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
        } ${isHeaderOver ? 'ring-2 ring-purple-400/80 bg-surface-hover' : ''}`}
      >
        <button
          type="button"
          aria-label={chevronLabel}
          aria-expanded={expanded}
          onClick={(e) => {
            e.stopPropagation()
            toggleExpanded(HOME_WS_KEY)
          }}
          className="p-1 rounded hover:bg-surface-secondary text-text-muted cursor-pointer"
        >
          <Chevron size={12} />
        </button>
        <button
          type="button"
          onClick={onSelectHome}
          className="flex-1 flex items-center gap-2 py-1.5 text-left cursor-pointer"
        >
          <img
            src="/icons/logo-transparent.png"
            alt=""
            width={16}
            height={16}
            className="rounded-sm"
          />
          <span className="truncate">{t('nav.home')}</span>
        </button>
      </div>

      {expanded && (
        <InlineTabList
          tabIds={standaloneTabIds}
          tabsById={tabsById}
          activeTabId={activeTabId}
          sourceWsId={null}
          onSelect={onSelectTab}
          onClose={onCloseTab}
          onMiddleClick={onMiddleClickTab}
          onContextMenu={onContextMenuTab}
        />
      )}
    </div>
  )
}
