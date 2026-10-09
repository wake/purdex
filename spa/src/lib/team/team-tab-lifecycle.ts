// spa/src/lib/team/team-tab-lifecycle.ts — the one subscriber that keeps the tab list in step with the teams (spec R5, R6,
// P2; plan TI-1c).
//
//  - Order: in the workspace that holds a lead's tab, the group's tabs (the lead, then its members' tabs in team order) are
//    ONE contiguous run starting where the lead's tab is; every other tab keeps its relative order. A member that left the
//    team (R6) is no longer in the run, so it sits right behind it. Written only when it differs.
//  - Lead gone: when a lead's tab disappears from the tab store — by whatever path closed it — the group's other tabs in
//    that workspace close with it (no history, a locked tab is kept), and the workspace is remembered for the ghost row.
//    A lead tab that merely moved to another workspace is not gone.
//
// The tab store has forgotten a closed tab, so which team it led is remembered here from the previous pass.
import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { closeTab } from '../tab-lifecycle'
import { currentTeamState, type TeamState } from './team-state'

interface LedGroup {
  teamKey: string
  workspaceId: string
  /** The group's member tabs in that workspace when last seen. */
  memberTabIds: string[]
}

const MAX_PASSES = 6 // a pass that writes can enable another (a close, then a reorder); more than this is a loop

/** The groups the current state draws, by the lead's tab id. */
function ledGroups(state: TeamState): Map<string, LedGroup> {
  const out = new Map<string, LedGroup>()
  const { workspaces } = useWorkspaceStore.getState()
  for (const view of state.views) {
    const leadTab = view.lead.tabId
    if (leadTab === null || state.index.byTabId.get(leadTab)?.key !== view.key) continue
    const ws = workspaces.find((w) => w.tabs.includes(leadTab))
    if (!ws) continue
    const memberTabIds = view.members
      .map((m) => m.tabId)
      .filter((id): id is string => id !== null && ws.tabs.includes(id) && state.index.byTabId.get(id)?.key === view.key)
    out.set(leadTab, { teamKey: view.key, workspaceId: ws.id, memberTabIds })
  }
  return out
}

/** `tabs` with every group's tabs gathered into one run at its lead's place, in team order. */
function normalised(tabs: readonly string[], groups: ReadonlyArray<{ lead: string; run: readonly string[] }>): string[] {
  let list = [...tabs]
  for (const { lead, run } of groups) {
    const inRun = new Set(run)
    const next: string[] = []
    for (const id of list) {
      if (id === lead) next.push(...run)
      else if (!inRun.has(id)) next.push(id)
    }
    list = next
  }
  return list
}

/** One pass: returns whether it changed anything (a close or a reorder). */
function pass(prev: Map<string, LedGroup>): { changed: boolean; groups: Map<string, LedGroup> } {
  const state = currentTeamState()
  const groups = ledGroups(state)
  const tabs = useTabStore.getState().tabs

  let changed = false
  for (const [leadTab, led] of prev) {
    if (Object.hasOwn(tabs, leadTab)) continue
    for (const id of led.memberTabIds) {
      if (!Object.hasOwn(useTabStore.getState().tabs, id) || useTabStore.getState().tabs[id].locked) continue
      closeTab(id, { skipHistory: true })
      if (!Object.hasOwn(useTabStore.getState().tabs, id)) changed = true // a close the person declined stays declined
    }
    useTeamUiStore.getState().setGhostWorkspace(led.teamKey, led.workspaceId)
    changed = true
  }
  if (changed) return { changed, groups }

  const byWorkspace = new Map<string, Array<{ lead: string; run: string[] }>>()
  for (const [lead, led] of groups) {
    const list = byWorkspace.get(led.workspaceId) ?? []
    list.push({ lead, run: [lead, ...led.memberTabIds] })
    byWorkspace.set(led.workspaceId, list)
  }
  for (const [wsId, runs] of byWorkspace) {
    const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)
    if (!ws) continue
    const want = normalised(ws.tabs, runs)
    if (want.length === ws.tabs.length && want.every((id, i) => id === ws.tabs[i])) continue
    useWorkspaceStore.getState().reorderWorkspaceTabs(wsId, want)
    changed = true
  }
  return { changed, groups }
}

/** Starts the subscriber; returns its stop function. Runs once now, then on every change of what it reads. */
export function startTeamTabLifecycle(): () => void {
  let prev = new Map<string, LedGroup>()
  let running = false
  let dirty = false
  let scheduled = false
  let stopped = false

  const run = () => {
    if (running) {
      dirty = true // a write of ours (or a nested notification): the loop below looks again
      return
    }
    running = true
    try {
      for (let i = 0; i < MAX_PASSES; i++) {
        dirty = false
        const r = pass(prev)
        prev = r.groups
        if (!r.changed && !dirty) break
      }
    } finally {
      running = false
    }
  }

  // Cheap guard: a store notification that moved none of the inputs costs a reference comparison.
  let last: unknown[] = []
  const onChange = () => {
    const inputs = [
      useTabStore.getState().tabs, useWorkspaceStore.getState().workspaces, useTeamRosterStore.getState().byHost,
      useSessionStore.getState().sessions, useTeamUiStore.getState().memberOrder, useHostStore.getState().hostOrder,
    ]
    if (inputs.length === last.length && inputs.every((v, i) => Object.is(v, last[i]))) return
    last = inputs
    // Not inside the notification: a close notifies the tab store BEFORE it has chosen the next active tab, and the group's
    // cascade would remove the tab it is about to pick. A microtask runs once the action has finished, and folds a burst
    // of notifications into one pass.
    if (scheduled) return
    scheduled = true
    queueMicrotask(() => {
      scheduled = false
      if (!stopped) run()
    })
  }

  const offs = [
    useTabStore.subscribe(onChange), useWorkspaceStore.subscribe(onChange), useTeamRosterStore.subscribe(onChange),
    useSessionStore.subscribe(onChange), useTeamUiStore.subscribe(onChange), useHostStore.subscribe(onChange),
  ]
  onChange()
  return () => {
    stopped = true
    for (const off of offs) off()
  }
}
