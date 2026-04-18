import { SortableContext, verticalListSortingStrategy } from '@dnd-kit/sortable'
import type { Tab } from '../../../types/tab'
import { InlineTab } from './InlineTab'
import { useI18nStore } from '../../../stores/useI18nStore'

interface Props {
  tabIds: string[]
  tabsById: Record<string, Tab>
  activeTabId: string | null
  sourceWsId: string | null
  onSelect: (tabId: string) => void
  onClose: (tabId: string) => void
  onMiddleClick: (tabId: string) => void
  onContextMenu: (e: React.MouseEvent, tabId: string) => void
  onRename?: (tabId: string) => void
}

export function InlineTabList({
  tabIds,
  tabsById,
  activeTabId,
  sourceWsId,
  onSelect,
  onClose,
  onMiddleClick,
  onContextMenu,
  onRename,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const validIds = tabIds.filter((id) => !!tabsById[id])

  if (validIds.length === 0) {
    return (
      <div className="pl-7 pr-3 py-1 text-[11px] text-text-muted italic">
        {t('nav.workspace_empty')}
      </div>
    )
  }

  return (
    <SortableContext items={validIds} strategy={verticalListSortingStrategy}>
      <div className="flex flex-col gap-0.5 py-0.5">
        {validIds.map((id) => (
          <InlineTab
            key={id}
            tab={tabsById[id]}
            isActive={activeTabId === id}
            sourceWsId={sourceWsId}
            onSelect={onSelect}
            onClose={onClose}
            onMiddleClick={onMiddleClick}
            onContextMenu={onContextMenu}
            onRename={onRename}
          />
        ))}
      </div>
    </SortableContext>
  )
}
