import { getTabRenderer } from '../lib/tab-registry'
import { useTabAlivePool } from '../hooks/useTabAlivePool'
import type { Tab } from '../types/tab'

interface Props {
  activeTab: Tab | null
  allTabs: Tab[]
  wsBase: string
  daemonBase: string
}

export function TabContent({ activeTab, allTabs, wsBase, daemonBase }: Props) {
  const { aliveIds, poolVersion } = useTabAlivePool(
    activeTab?.id ?? null,
    allTabs.map((t) => ({ id: t.id, pinned: t.pinned })),
  )

  if (!activeTab && aliveIds.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">
        選擇或建立一個分頁開始使用
      </div>
    )
  }

  const tabMap = new Map(allTabs.map((t) => [t.id, t]))

  return (
    <div className="flex-1 relative">
      {aliveIds.map((id) => {
        const tab = tabMap.get(id)
        if (!tab) return null
        const config = getTabRenderer(tab.type)
        if (!config) return null
        const Renderer = config.component
        const isActive = id === activeTab?.id
        return (
          <div
            key={`${id}-${poolVersion}`}
            style={{ display: isActive ? 'contents' : 'none' }}
          >
            <Renderer tab={tab} isActive={isActive} wsBase={wsBase} daemonBase={daemonBase} />
          </div>
        )
      })}
    </div>
  )
}
