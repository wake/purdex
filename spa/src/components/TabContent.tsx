import { PaneLayoutRenderer } from './PaneLayoutRenderer'
import { useTabAlivePool } from '../hooks/useTabAlivePool'
import { isLightTab } from '../lib/pane-weight'
import { useI18nStore } from '../stores/useI18nStore'
import type { Tab } from '../types/tab'

interface Props {
  activeTab: Tab | null
  allTabs: Tab[]
}

export function TabContent({ activeTab, allTabs }: Props) {
  const t = useI18nStore((s) => s.t)
  const { aliveIds, poolVersion } = useTabAlivePool(
    activeTab?.id ?? null,
    allTabs.map((t) => ({ id: t.id, pinned: t.pinned, light: isLightTab(t.layout) })),
  )

  const tabMap = new Map(allTabs.map((t) => [t.id, t]))
  const hasAliveTab = aliveIds.some((id) => tabMap.has(id))

  if (!activeTab && !hasAliveTab) {
    return (
      <div className="flex-1 flex items-center justify-center text-text-secondary text-sm">
        {t('tab.empty_state')}
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
            style={{ inset: 0, visibility: isActive ? 'visible' : 'hidden' }}
            inert={!isActive || undefined}
          >
            <PaneLayoutRenderer layout={tab.layout} tabId={id} isActive={isActive} />
          </div>
        )
      })}
    </div>
  )
}
