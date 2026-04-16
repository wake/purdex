import { useLayoutStore } from '../../../stores/useLayoutStore'
import { ActivityBarNarrow } from './ActivityBarNarrow'
import { ActivityBarWide } from './ActivityBarWide'
import type { ActivityBarProps } from './activity-bar-props'

export function ActivityBar(props: ActivityBarProps) {
  const width = useLayoutStore((s) => s.activityBarWidth)
  if (width === 'wide') return <ActivityBarWide {...props} />
  return <ActivityBarNarrow {...props} />
}
