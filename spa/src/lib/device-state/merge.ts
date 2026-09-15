// spa/src/lib/device-state/merge.ts — additive merge of a device state into the
// current tab world (spec §5.3). Pure: inputs are never mutated, and nothing in
// `next` shares a mutable object with `incoming`.

import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import { collectLeaves, scanPaneTree } from '../pane-tree'
import { tabKey } from './identity'

export interface TabWorld {
  tabs: Record<string, Tab>
  tabOrder: string[]
  activeTabId: string | null
  workspaces: Workspace[]
  activeWorkspaceId: string | null
}

export interface MergeReport {
  addedWorkspaces: number
  addedTabs: number
  skippedTabs: number
}

function cloneLayout(layout: PaneLayout, freshId: () => string): PaneLayout {
  if (layout.type === 'leaf') {
    return { type: 'leaf', pane: { id: freshId(), content: structuredClone(layout.pane.content) } }
  }
  return {
    type: 'split',
    id: freshId(),
    direction: layout.direction,
    sizes: [...layout.sizes],
    children: layout.children.map((child) => cloneLayout(child, freshId)),
  }
}

/** Deep copy of `tab` with a new tab id and a new id for every pane and split. Content is copied unchanged. */
export function cloneTabWithFreshIds(tab: Tab, freshId: () => string): Tab {
  return { ...tab, id: freshId(), layout: cloneLayout(tab.layout, freshId) }
}

function collectLayoutIds(layout: PaneLayout, into: Set<string>): void {
  if (layout.type === 'leaf') {
    into.add(layout.pane.id)
    return
  }
  into.add(layout.id)
  for (const child of layout.children) collectLayoutIds(child, into)
}

function isNewTabOnly(tab: Tab): boolean {
  return collectLeaves(tab.layout).every((pane) => pane.content.kind === 'new-tab')
}

function nameMap(workspaces: readonly Workspace[]): Map<string, string> {
  return new Map(workspaces.map((ws) => [ws.id, ws.name]))
}

export function mergeDeviceState(
  current: TabWorld,
  incoming: TabWorld,
  idGen: () => string,
): { next: TabWorld; report: MergeReport } {
  const currentNames = nameMap(current.workspaces)
  const incomingNames = nameMap(incoming.workspaces)

  const used = new Set<string>()
  for (const tab of Object.values(current.tabs)) {
    used.add(tab.id)
    collectLayoutIds(tab.layout, used)
  }
  for (const ws of current.workspaces) used.add(ws.id)
  const freshId = (): string => {
    let id = idGen()
    while (used.has(id)) id = idGen()
    used.add(id)
    return id
  }

  const existing = new Set<string>()
  for (const tab of Object.values(current.tabs)) {
    const key = tabKey(tab, currentNames)
    if (key !== null) existing.add(key)
  }

  const tabs: Record<string, Tab> = { ...current.tabs }
  const tabOrder = [...current.tabOrder]
  // Current workspaces are shallow-copied (with a fresh `tabs` array) so appends never touch the inputs.
  const workspaces: Workspace[] = current.workspaces.map((ws) => ({ ...ws, tabs: [...ws.tabs] }))
  const report: MergeReport = { addedWorkspaces: 0, addedTabs: 0, skippedTabs: 0 }
  const wsIdMap = new Map<string, string>()
  const cloned: Tab[] = []

  /** Skip or clone one incoming tab; returns the clone's id when added. */
  const takeTab = (incomingTabId: string): string | null => {
    const src = incoming.tabs[incomingTabId]
    if (!src || isNewTabOnly(src)) return null
    const key = tabKey(src, incomingNames)
    if (key !== null && existing.has(key)) {
      report.skippedTabs++
      return null
    }
    const clone = cloneTabWithFreshIds(src, freshId)
    tabs[clone.id] = clone
    tabOrder.push(clone.id)
    cloned.push(clone)
    if (key !== null) existing.add(key)
    report.addedTabs++
    return clone.id
  }

  // Pass 1a: workspaces and their tabs, in incoming order.
  const inIncomingWorkspace = new Set<string>()
  for (const inWs of incoming.workspaces) {
    for (const id of inWs.tabs) inIncomingWorkspace.add(id)
    const trimmed = inWs.name.trim()
    let target = workspaces.find((ws) => ws.name.trim() === trimmed)
    const isNew = target === undefined
    if (target === undefined) {
      target = {
        id: freshId(),
        name: inWs.name,
        ...(inWs.icon !== undefined ? { icon: inWs.icon } : {}),
        ...(inWs.iconWeight !== undefined ? { iconWeight: inWs.iconWeight } : {}),
        ...(inWs.moduleConfig !== undefined ? { moduleConfig: structuredClone(inWs.moduleConfig) } : {}),
        tabs: [],
        activeTabId: null,
      }
      workspaces.push(target)
      report.addedWorkspaces++
    }
    wsIdMap.set(inWs.id, target.id)

    const addedHere = new Map<string, string>() // incoming tab id → clone id
    for (const incomingTabId of inWs.tabs) {
      const cloneId = takeTab(incomingTabId)
      if (cloneId === null) continue
      target.tabs.push(cloneId)
      addedHere.set(incomingTabId, cloneId)
    }
    if (isNew) {
      const activeClone = inWs.activeTabId !== null ? addedHere.get(inWs.activeTabId) : undefined
      target.activeTabId = activeClone ?? addedHere.values().next().value ?? null
    }
  }

  // Pass 1b: standalone incoming tabs → tabOrder only.
  for (const incomingTabId of incoming.tabOrder) {
    if (inIncomingWorkspace.has(incomingTabId)) continue
    takeTab(incomingTabId)
  }

  // Pass 2: settings scopes in cloned tabs, now that every incoming workspace is mapped.
  // Clones are fresh objects owned by this function, so rewriting them in place is safe.
  for (const tab of cloned) {
    scanPaneTree(tab.layout, (pane) => {
      const content = pane.content
      if (content.kind !== 'settings' || content.scope === 'global') return
      const mapped = wsIdMap.get(content.scope.workspaceId)
      pane.content = { kind: 'settings', scope: mapped === undefined ? 'global' : { workspaceId: mapped } }
    })
  }

  return {
    next: {
      tabs,
      tabOrder,
      activeTabId: current.activeTabId,
      workspaces,
      activeWorkspaceId: current.activeWorkspaceId,
    },
    report,
  }
}
