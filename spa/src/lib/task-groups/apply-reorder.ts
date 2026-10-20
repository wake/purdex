// spa/src/lib/task-groups/apply-reorder.ts — makes the writes a `ReorderDecision` (reorder.ts) asks for (task groups spec §6.1; TG-3e).
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import type { ReorderDecision } from './reorder'

export function applyReorder(decision: ReorderDecision): void {
  if (decision.kind === 'rank') useTabStore.getState().setTabGroupRanks(decision.writes)
  else if (decision.kind === 'members') useTeamUiStore.getState().setMemberOrder(decision.teamKey, decision.order)
}
