// spa/src/components/tab-icon-helpers.ts
import type { AgentStatus } from '../stores/useAgentStore'
import type { TabIndicatorStyle } from '../stores/useUISettingsStore'

/**
 * The corner pip is a fallback for indicator styles that have no dot to
 * co-locate unread with. 'badge' tints its overlay dot red; 'dot' / 'iconDot'
 * stack a small pip on the dot wrapper. 'icon' and "no event yet" states
 * have no dot, so they fall back to the corner pip.
 */
export function shouldShowGlobalUnreadPip(
  mode: TabIndicatorStyle,
  agentStatus: AgentStatus | undefined,
): boolean {
  if (!agentStatus) return true
  return mode === 'icon'
}
