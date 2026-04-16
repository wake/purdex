import { useLayoutStore } from '../../../stores/useLayoutStore'
import { ActivityBarNarrow } from './ActivityBarNarrow'
import { ActivityBarWide } from './ActivityBarWide'
import type { Workspace } from '../../../types/tab'

interface Props {
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  activeStandaloneTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onSelectHome: () => void
  standaloneTabIds: string[]
  onAddWorkspace: () => void
  onReorderWorkspaces?: (orderedIds: string[]) => void
  onContextMenuWorkspace?: (e: React.MouseEvent, wsId: string) => void
  onOpenHosts: () => void
  onOpenSettings: () => void
}

export function ActivityBar(props: Props) {
  const width = useLayoutStore((s) => s.activityBarWidth)
  if (width === 'wide') return <ActivityBarWide {...props} />
  return <ActivityBarNarrow {...props} />
}
