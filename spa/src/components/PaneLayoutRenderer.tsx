import { getPaneRenderer } from '../lib/module-registry'
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
        <div className="flex-1 flex items-center justify-center text-text-muted">
          No renderer for &quot;{layout.pane.content.kind}&quot;
        </div>
      )
    }
    const Component = config.component
    return <Component pane={layout.pane} isActive={isActive} />
  }

  // Split — future: render split container. For now, render first child.
  if (layout.children.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center text-text-muted">
        Empty split layout
      </div>
    )
  }

  return (
    <PaneLayoutRenderer
      key={getLayoutKey(layout.children[0])}
      layout={layout.children[0]}
      isActive={isActive}
    />
  )
}
