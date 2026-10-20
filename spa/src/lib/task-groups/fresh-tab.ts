// spa/src/lib/task-groups/fresh-tab.ts — the one automatic write of the task-group view (task groups spec §5): a tab OPENED while a group is selected joins that group. "Opened" is decided by where the tab object came from
// (`createTab`'s fresh set, types/tab.ts), not by when it is activated; the two tab-store actions that insert a tab, `addTab` and `detachPane`, take the tab out of that set in the same `set()` that inserts it and hand it
// here. The tab is placed when: the effective view is the task-group view; a group is selected — a live team or a manual group, never 「未分組」; the tab has no `group`; it is not a watcher-area tab; and neither a team seat nor
// an agent root places it as far as is known now (a root not read yet counts as no match — that rule still wins once it is). Placing sets `group` and nothing else: no rank, so the tab sorts last by its `createdAt`. Written in
// the same update as the insertion, the selection subscriber already sees the tab placed and the selection stays.
import type { Tab } from '../../types/tab'
import { useGroupViewStore } from '../../stores/useGroupViewStore'
import { isWatchTabId } from '../watch/ids'
import { effectiveView } from './effective-view'
import { currentPlacementCtx } from './current'
import { parseGroupKey, UNGROUPED } from './group-key'
import { groupOfTab } from './placement'

/** `tab` as the store should hold it: with the selected group as its `group` when the rule above says so, else the same object. Reads only. */
export function placeFreshTab(tab: Tab): Tab {
  if (tab.group !== undefined || isWatchTabId(tab.id)) return tab
  if (effectiveView() !== 'groups') return tab
  const selected = useGroupViewStore.getState().selectedGroup
  if (selected === null || selected === UNGROUPED) return tab
  const parsed = parseGroupKey(selected)
  if (parsed === null) return tab
  const ctx = currentPlacementCtx(tab) // with this tab in the team index: a tab that shows a seat is the team's (rule 1) before it is in the store
  // the selected group must still exist (rule 3 would ignore a key that names nothing)
  if (parsed.kind === 'team' && !ctx.index.byKey.has(parsed.teamKey)) return tab
  if (parsed.kind === 'manual' && ctx.manual.groups[parsed.id] === undefined) return tab
  if (groupOfTab(tab, ctx) !== UNGROUPED) return tab // a seat or a known root already places it
  return { ...tab, group: selected }
}
