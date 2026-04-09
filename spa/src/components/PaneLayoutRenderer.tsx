import { getPaneRenderer } from '../lib/module-registry'
import { getLayoutKey } from '../lib/pane-tree'
import { PaneSplitter } from './PaneSplitter'
import { useTabStore } from '../stores/useTabStore'
import type { PaneLayout } from '../types/tab'

interface Props {
  layout: PaneLayout
  tabId: string
  isActive: boolean
}

export function PaneLayoutRenderer({ layout, tabId, isActive }: Props) {
  if (layout.type === 'leaf') {
    const config = getPaneRenderer(layout.pane.content.kind)
    if (!config) {
      return (
        <div className="flex-1 flex items-center justify-center text-text-muted">
          No renderer for &quot;{layout.pane.content.kind}&quot;
        </div>
      )
    }
    const Component = config.component
    return <Component pane={layout.pane} isActive={isActive} />
  }

  if (layout.children.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center text-text-muted">
        Empty split layout
      </div>
    )
  }

  const handleResize = (index: number, deltaPx: number) => {
    const totalPercent = layout.sizes[index] + layout.sizes[index + 1]
    const percentDelta = deltaPx * 0.1
    const newLeft = Math.max(10, Math.min(totalPercent - 10, layout.sizes[index] + percentDelta))
    const newRight = totalPercent - newLeft
    const newSizes = [...layout.sizes]
    newSizes[index] = newLeft
    newSizes[index + 1] = newRight
    useTabStore.getState().resizePanes(tabId, layout.id, newSizes)
  }

  return (
    <div className={`flex-1 flex ${layout.direction === 'h' ? 'flex-row' : 'flex-col'} overflow-hidden`}>
      {layout.children.map((child, i) => (
        <div key={getLayoutKey(child)} className="contents">
          {i > 0 && (
            <PaneSplitter
              direction={layout.direction}
              onResize={(delta) => handleResize(i - 1, delta)}
            />
          )}
          <div style={{ flex: `${layout.sizes[i]} 0 0%` }} className="min-w-0 min-h-0 flex overflow-hidden">
            <PaneLayoutRenderer layout={child} tabId={tabId} isActive={isActive} />
          </div>
        </div>
      ))}
    </div>
  )
}
