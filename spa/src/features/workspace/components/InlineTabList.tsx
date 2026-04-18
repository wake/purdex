import { SortableContext, verticalListSortingStrategy } from '@dnd-kit/sortable'
import type { Tab } from '../../../types/tab'
import { InlineTab } from './InlineTab'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useSessionStore } from '../../../stores/useSessionStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'
import { getPaneLabel } from '../../../lib/pane-labels'
import { getPrimaryPane } from '../../../lib/pane-tree'

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
  const sessionsByHost = useSessionStore((s) => s.sessions)
  const workspaces = useWorkspaceStore((s) => s.workspaces)

  // Sessions are stored nested by hostId — flatten and search across all hosts
  // for name lookup. Collisions (same code across hosts) are rare and the
  // label falls back gracefully to cachedName/sessionCode when missing.
  const sessionLookup = {
    getByCode: (code: string) => {
      for (const hostId in sessionsByHost) {
        const found = sessionsByHost[hostId]?.find((s) => s.code === code)
        if (found) return found
      }
      return undefined
    },
  }
  const workspaceLookup = {
    getById: (id: string) => workspaces.find((w) => w.id === id),
  }

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
        {validIds.map((id) => {
          const tab = tabsById[id]
          const primaryContent = getPrimaryPane(tab.layout).content
          const title = getPaneLabel(primaryContent, sessionLookup, workspaceLookup, t)
          return (
            <InlineTab
              key={id}
              tab={tab}
              title={title}
              isActive={activeTabId === id}
              sourceWsId={sourceWsId}
              onSelect={onSelect}
              onClose={onClose}
              onMiddleClick={onMiddleClick}
              onContextMenu={onContextMenu}
              onRename={onRename}
            />
          )
        })}
      </div>
    </SortableContext>
  )
}
