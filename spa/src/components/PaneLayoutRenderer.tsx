import { getPaneRenderer } from '../lib/pane-registry'
import { getLayoutKey } from '../lib/pane-tree'
import type { PaneLayout } from '../types/tab'

interface Props {
  layout: PaneLayout
  isActive: boolean
}

export function PaneLayoutRenderer({ layout, isActive }: Props) {
  if (layout.type === 'leaf') {
    const config = getPaneRenderer(layout.pane.content.kind)
    if (!config) {
      return (
        <div className="flex-1 flex items-center justify-center text-gray-500">
          No renderer for &quot;{layout.pane.content.kind}&quot;
        </div>
      )
    }
    const Component = config.component
    return <Component pane={layout.pane} isActive={isActive} />
  }

  // Split — future: render split container. For now, render first child.
  return (
    <PaneLayoutRenderer
      key={getLayoutKey(layout.children[0])}
      layout={layout.children[0]}
      isActive={isActive}
    />
  )
}
