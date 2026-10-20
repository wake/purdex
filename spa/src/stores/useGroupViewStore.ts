// spa/src/stores/useGroupViewStore.ts — what this device remembers of the task-group view (task groups spec §6.2, §6.3): the selected group, each group's last active tab, which groups are expanded, and the last selection per view.
//
// Device-local: never in the SOT, never synced (so no `syncManager`), one record for the whole device — not per world. Group keys are the keys of lib/task-groups/group-key.ts, and a team key carries a LOCAL host id, so the store
// joins the host re-resolve / removal rewrite (`rewriteGroupViewHosts`, used by `planRewrite`). Tab ids are world-specific: an id that no longer exists is the reader's to fall back from. Every arrival (storage, rehydrate)
// passes through `merge`, which sanitises.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'
import { parseGroupKey, teamGroupKey } from '../lib/task-groups/group-key'

/** The last selection of each view (spec §6.3): the workspace id of the workspace view, the group key of the task-group view. */
export interface LastByView {
  workspace: string | null
  group: string | null
}

export interface GroupViewData {
  selectedGroup: string | null
  /** Group key → the tab that was active in it last. */
  lastActiveTab: Record<string, string>
  /** Group key → whether its tabs are listed; absent = the default (expanded). */
  expanded: Record<string, boolean>
  lastByView: LastByView
}

interface GroupViewState extends GroupViewData {
  /** The task-group view's selection follows the active tab: the group, that group's last tab, and the view's last group, in one write. */
  recordGroupActive: (groupKey: string, tabId: string) => void
  /** The workspace view's selection follows the active tab: the view's last workspace. */
  recordWorkspace: (workspaceId: string) => void
  /** A group selected with no tab (an empty group, a sidebar click that activates nothing yet). */
  selectGroup: (groupKey: string | null) => void
  setExpanded: (groupKey: string, expanded: boolean) => void
  /** Forgets everything about the groups `gone` says yes to (a deleted manual group, a team no roster lists). */
  forgetGroups: (gone: (groupKey: string) => boolean) => void
}

const EMPTY: GroupViewData = { selectedGroup: null, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } }

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const validKey = (k: unknown): k is string => typeof k === 'string' && parseGroupKey(k) !== null
const nonEmpty = (v: unknown): v is string => typeof v === 'string' && v !== ''

/** The persisted value read as the store's data: anything that is not what it should be is dropped. */
export function sanitizeGroupView(raw: unknown): GroupViewData {
  const p = isRecord(raw) ? raw : {}
  const lastActiveTab: Record<string, string> = {}
  if (isRecord(p.lastActiveTab)) for (const [k, v] of Object.entries(p.lastActiveTab)) if (validKey(k) && nonEmpty(v)) lastActiveTab[k] = v
  const expanded: Record<string, boolean> = {}
  if (isRecord(p.expanded)) for (const [k, v] of Object.entries(p.expanded)) if (validKey(k) && typeof v === 'boolean') expanded[k] = v
  const by = isRecord(p.lastByView) ? p.lastByView : {}
  return {
    selectedGroup: validKey(p.selectedGroup) ? p.selectedGroup : null,
    lastActiveTab,
    expanded,
    lastByView: { workspace: nonEmpty(by.workspace) ? by.workspace : null, group: validKey(by.group) ? by.group : null },
  }
}

/** A group key with the host of a team key passed through `map`; any other key as it is. */
function mapKey(key: string, map: (hostId: string) => string): string {
  const parsed = parseGroupKey(key)
  if (parsed?.kind !== 'team') return key
  const [host, teamId] = parsed.teamKey.split('\u0000')
  const next = map(host)
  return next === host ? key : teamGroupKey(`${next}\u0000${teamId}`)
}

function mapRecord<T>(rec: Record<string, T>, map: (hostId: string) => string): Record<string, T> {
  const out: Record<string, T> = {}
  for (const [k, v] of Object.entries(rec)) {
    const key = mapKey(k, map)
    if (!Object.hasOwn(out, key)) out[key] = v // two keys the mapping makes equal: the first one stays
  }
  return out
}

/**
 * The data with every team key's host passed through `map` (the host re-resolve pass, and the host deletion's rewrite).
 * The same state back (identity) when nothing moves.
 */
export function rewriteGroupViewHosts(state: GroupViewData, map: (hostId: string) => string): GroupViewData {
  const keys = [state.selectedGroup, state.lastByView.group, ...Object.keys(state.lastActiveTab), ...Object.keys(state.expanded)]
  if (!keys.some((k) => k !== null && mapKey(k, map) !== k)) return state
  return {
    selectedGroup: state.selectedGroup === null ? null : mapKey(state.selectedGroup, map),
    lastActiveTab: mapRecord(state.lastActiveTab, map),
    expanded: mapRecord(state.expanded, map),
    lastByView: { workspace: state.lastByView.workspace, group: state.lastByView.group === null ? null : mapKey(state.lastByView.group, map) },
  }
}

export const useGroupViewStore = create<GroupViewState>()(
  persist(
    (set) => ({
      ...EMPTY,

      recordGroupActive: (groupKey, tabId) =>
        set((s) => {
          if (s.selectedGroup === groupKey && s.lastActiveTab[groupKey] === tabId && s.lastByView.group === groupKey) return s
          return {
            selectedGroup: groupKey,
            lastActiveTab: s.lastActiveTab[groupKey] === tabId ? s.lastActiveTab : { ...s.lastActiveTab, [groupKey]: tabId },
            lastByView: s.lastByView.group === groupKey ? s.lastByView : { ...s.lastByView, group: groupKey },
          }
        }),

      recordWorkspace: (workspaceId) =>
        set((s) => (s.lastByView.workspace === workspaceId ? s : { lastByView: { ...s.lastByView, workspace: workspaceId } })),

      selectGroup: (groupKey) =>
        set((s) => {
          if (s.selectedGroup === groupKey && (groupKey === null || s.lastByView.group === groupKey)) return s
          return { selectedGroup: groupKey, lastByView: groupKey === null || s.lastByView.group === groupKey ? s.lastByView : { ...s.lastByView, group: groupKey } }
        }),

      setExpanded: (groupKey, expanded) =>
        set((s) => (s.expanded[groupKey] === expanded ? s : { expanded: { ...s.expanded, [groupKey]: expanded } })),

      forgetGroups: (gone) =>
        set((s) => {
          const keep = <T,>(rec: Record<string, T>) => Object.fromEntries(Object.entries(rec).filter(([k]) => !gone(k)))
          const lastActiveTab = keep(s.lastActiveTab)
          const expanded = keep(s.expanded)
          const selectedGroup = s.selectedGroup !== null && gone(s.selectedGroup) ? null : s.selectedGroup
          const group = s.lastByView.group !== null && gone(s.lastByView.group) ? null : s.lastByView.group
          const same = Object.keys(lastActiveTab).length === Object.keys(s.lastActiveTab).length && Object.keys(expanded).length === Object.keys(s.expanded).length && selectedGroup === s.selectedGroup && group === s.lastByView.group
          return same ? s : { selectedGroup, lastActiveTab, expanded, lastByView: { ...s.lastByView, group } }
        }),
    }),
    {
      name: STORAGE_KEYS.GROUP_VIEW,
      storage: purdexStorage,
      version: 1,
      partialize: (s) => ({ selectedGroup: s.selectedGroup, lastActiveTab: s.lastActiveTab, expanded: s.expanded, lastByView: s.lastByView }),
      merge: (persisted, current) => ({ ...current, ...sanitizeGroupView(persisted) }),
    },
  ),
)
