// spa/src/stores/useManualGroupsStore.ts — the manual task groups (task groups spec §4.1).
//
// `{ order, groups }`: `order` is the sidebar order of the group ids, `groups` the definitions. A member is a LOCAL host
// id plus a lineage root; a (host, root) sits in at most one group. Host ids are local in the store: the `settings`
// builder writes them as wire ids (de-duplicating and sorting the members then — NOT here: local ids differ per device,
// so a sort here would make two devices build different payloads and push each other's back) and `settingsFromWire`
// reads them back. This store removes only duplicates of its own form; the host rewrite collapses the ones its mapping
// creates.
//
// Persisted, registered with `syncManager`, projected in `settings` (`purdex-task-groups.{order,groups}`, ordinal 12).
// Every arrival (storage, another window, a `settings` apply) passes through `merge`, which sanitises.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import { groupNameError } from '../lib/task-groups/edits'

export const MAX_GROUPS = 32
export const MAX_MEMBERS = 64

export interface ManualGroupMember {
  /** LOCAL host id (or, until the host is known here, the wire id as received). */
  host: string
  /** Lineage root session id. */
  root: string
}

export interface ManualGroup {
  name: string
  /** `#rrggbb`; absent = the default colour. */
  color?: string
  members: ManualGroupMember[]
  createdAt: number
}

export interface ManualGroups {
  order: string[]
  groups: Record<string, ManualGroup>
}

interface ManualGroupsState extends ManualGroups {
  /** Adds the group and appends its id to `order`; an existing id is replaced in place. */
  addGroup: (id: string, group: ManualGroup) => void
  /** Merges fields into a group; an unknown id is ignored. */
  patchGroup: (id: string, patch: Partial<ManualGroup>) => void
  removeGroup: (id: string) => void
  setOrder: (order: string[]) => void
}

const GROUP_ID = /^[0-9a-f]{8}$/
const COLOR = /^#[0-9a-fA-F]{6}$/

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function sanitizeMembers(raw: unknown, taken: Set<string>): ManualGroupMember[] {
  if (!Array.isArray(raw)) return []
  const out: ManualGroupMember[] = []
  for (const m of raw) {
    if (out.length >= MAX_MEMBERS) break
    if (!isRecord(m) || typeof m.host !== 'string' || m.host === '' || typeof m.root !== 'string' || m.root === '') continue
    const key = JSON.stringify([m.host, m.root])
    if (taken.has(key)) continue
    taken.add(key)
    out.push({ host: m.host, root: m.root })
  }
  return out
}

function sanitizeGroup(raw: unknown, taken: Set<string>): ManualGroup | null {
  if (!isRecord(raw) || typeof raw.name !== 'string' || groupNameError(raw.name) !== null) return null
  const createdAt = typeof raw.createdAt === 'number' && Number.isFinite(raw.createdAt) && raw.createdAt >= 0 ? raw.createdAt : 0
  return {
    name: raw.name.trim(),
    ...(typeof raw.color === 'string' && COLOR.test(raw.color) ? { color: raw.color } : {}),
    members: sanitizeMembers(raw.members, taken),
    createdAt,
  }
}

/** The persisted value read as groups: bad groups dropped, members de-duplicated (first group in `order` wins), limits applied. */
export function sanitizeManualGroups(raw: unknown): ManualGroups {
  const stored = isRecord(raw) ? raw : {}
  const rawGroups = isRecord(stored.groups) ? stored.groups : {}
  const rawOrder = Array.isArray(stored.order) ? stored.order : []
  const candidates: string[] = []
  const seen = new Set<string>()
  const consider = (id: unknown) => {
    if (typeof id !== 'string' || seen.has(id) || !GROUP_ID.test(id) || !Object.hasOwn(rawGroups, id)) return
    seen.add(id)
    candidates.push(id)
  }
  for (const id of rawOrder) consider(id)
  for (const id of Object.keys(rawGroups)) consider(id)
  const order: string[] = []
  const groups: Record<string, ManualGroup> = {}
  const taken = new Set<string>()
  for (const id of candidates) {
    if (order.length >= MAX_GROUPS) break
    const group = sanitizeGroup(rawGroups[id], taken)
    if (group === null) continue
    order.push(id)
    groups[id] = group
  }
  return { order, groups }
}

export const useManualGroupsStore = create<ManualGroupsState>()(
  persist(
    (set) => ({
      order: [],
      groups: {},

      addGroup: (id, group) =>
        set((state) => ({
          groups: { ...state.groups, [id]: group },
          order: state.order.includes(id) ? state.order : [...state.order, id],
        })),

      patchGroup: (id, patch) =>
        set((state) => (state.groups[id] === undefined ? state : { groups: { ...state.groups, [id]: { ...state.groups[id], ...patch } } })),

      removeGroup: (id) =>
        set((state) => {
          if (state.groups[id] === undefined) return state
          const groups = { ...state.groups }
          delete groups[id]
          return { groups, order: state.order.filter((x) => x !== id) }
        }),

      setOrder: (order) => set({ order }),
    }),
    {
      name: STORAGE_KEYS.TASK_GROUPS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ order: state.order, groups: state.groups }),
      merge: (persisted, current) => ({ ...current, ...sanitizeManualGroups(persisted) }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.TASK_GROUPS, useManualGroupsStore)
