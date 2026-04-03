import type { PaneContent } from '../types/tab'
import type { HostRuntime } from '../stores/useHostStore'

export type TabState = 'active' | 'reconnecting' | 'terminated'

export function deriveTabState(content: PaneContent, runtime?: HostRuntime): TabState {
  if (content.kind !== 'tmux-session') return 'active'
  if (content.terminated) return 'terminated'
  if (runtime?.status === 'reconnecting') return 'reconnecting'
  return 'active'
}
