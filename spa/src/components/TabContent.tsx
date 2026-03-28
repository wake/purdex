import { PaneLayoutRenderer } from './PaneLayoutRenderer'
import { useTabAlivePool } from '../hooks/useTabAlivePool'
import type { Tab } from '../types/tab'

interface Props {
  activeTab: Tab | null
  allTabs: Tab[]
}

export function TabContent({ activeTab, allTabs }: Props) {
  const { aliveIds, poolVersion } = useTabAlivePool(
    activeTab?.id ?? null,
    allTabs.map((t) => ({ id: t.id, pinned: t.pinned })),
  )

  const tabMap = new Map(allTabs.map((t) => [t.id, t]))
  const hasAliveTab = aliveIds.some((id) => tabMap.has(id))

  if (!activeTab && !hasAliveTab) {
    return (
      <div className="flex-1 flex items-center justify-center text-text-secondary text-sm">
        選擇或建立一個分頁開始使用
      </div>
    )
  }

  return (
    <div className="flex-1 relative overflow-hidden">
      {aliveIds.map((id) => {
        const tab = tabMap.get(id)
        if (!tab) return null
        const isActive = id === activeTab?.id
        return (
          <div
            key={`${id}-${poolVersion}`}
            className="absolute"
            style={isActive ? { inset: 0 } : { top: 0, left: '-9999em', width: '100%', height: '100%' }}
          >
            <PaneLayoutRenderer layout={tab.layout} isActive={isActive} />
          </div>
        )
      })}
    </div>
  )
}
