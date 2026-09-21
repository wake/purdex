// spa/src/stores/useLocalProfilesStore.ts — the slaves, the active pointer and
// the parked master world (Profile Sync spec §4.1; P3 plan, P3b Task 3).
//
// A profile's tab world — its workspaces and their tabs — is either ON SCREEN
// (it lives in `useWorkspaceStore` / `useTabStore`, and this store holds nothing
// for it) or PARKED (it lives here, as a `ParkedWorld`). This store is the
// parking lot and the pointer; it never touches the tab stores itself.
//
// INVARIANT: exactly one world is on screen.
//   activeProfileId === 'master' ⇔ parkedMaster === null, every slave parked
//   activeProfileId === <id>     ⇔ that slave's world === null, every other
//                                  slave parked, parkedMaster !== null
// and `slaveOrder` is a permutation of the keys of `slaves`. Every action keeps
// it (a request that would break it is refused, never half done); the persist
// `merge` re-establishes it for whatever storage hands back.
//
// WHY DEVICE-LOCAL, FOR EVER. Slaves never reach the daemon (decision 9), and
// which world is on screen is a fact about this machine. `PROJECTIONS` is the
// only sync allowlist and this storage key must never be listed there; the test
// pins it.
//
// WHY IT IS `syncManager`-REGISTERED. Every window of this client shows the same
// world, and the leader — possibly another window — must know whether the live
// stores hold the master's world or a slave's before it reports anything to the
// SOT (`lib/profile/master-world.ts`, Task 4). A cross-window rehydrate is a
// full-state replace, which is exactly right here: there is one parking lot.
//
// WHY ACTIONS ANSWER `{ok: false, reason}` AND NEVER THROW. They are the
// low-level primitives of `lib/profile/switch-active.ts` (Task 5), which runs
// them inside a synchronous block together with writes to two other stores and
// must be able to tell "refused, nothing changed" from "something broke" in
// order to roll back. A refusal leaves the state reference untouched (no
// subscriber call, no storage write).
//
// WHY `swapActive` AND `promoteSlave` ARE ONE `set` EACH. A state in which no
// world — or two — is on screen must never be observable: not by a subscriber,
// and not by another window, which sees exactly what was persisted. One `set` is
// one notification and one storage write.
//
// `worldEpoch` — the epoch barrier of Task 4. A switch stamps the SAME new epoch
// into this store and into the two tab stores; while the three disagree the
// master world is "unsettled" and nothing is reported. This store only guards
// that it moves forward: the caller supplies the value (it has to write the same
// one elsewhere), and one that does not exceed the current epoch is refused.
//
// `relabelCount` — +1 with every `promoteSlave`, never down; its value means
// nothing, only that it moved. A promote changes WHICH WORLD the label `'master'`
// (and a slave id) names, and anything that remembered a world by its label
// across time must be able to tell: lib/host-lifecycle.ts keeps it in the
// snapshot of a host delete, and an undo that finds it moved touches no world.
// Persisted and synced like the rest, so another window's promote counts too.
//
// APPEARANCE — a name, an icon (+ weight) and a colour per profile, the master included (`master`): what the
// Home button and the profile switcher show. With nothing set — `master: { name: null }` — the button is `Home`
// and the Purdex logo, i.e. the app as it was. The shapes are the ones the app already has, so the existing
// pickers fit: `icon` / `iconWeight` as on a `Workspace` (a Phosphor name from the catalog — an unknown one
// would be painted as TEXT by `WorkspaceIcon`, hence `isPhosphorIconName`), `color` as a host's strict
// `#rrggbb` (`isValidHostColor`; it reaches inline CSS). A profile's appearance is PER DEVICE for now, like
// everything in this store; carrying it across devices would take a section of its own and is not in P3.
//   It belongs to the WORLD, not to the label: `promoteSlave` hands the promoted slave's look to the master and
// the old master's to the demoted slave — what a user recognises is "that working environment", whatever it is
// called now. A copy (`addSlave`) starts plain: two worlds that look alike are one too many.
//
// `promoteSlave` IS A RELABELLING, NOT A SWITCH. It never takes a world off the
// screen or puts one on, so the tab stores' CONTENT is never involved — only, in
// two of the three cases, their world tag (see the action's comment).
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { generateId } from '../lib/id'
import { normalizeDeviceName } from '../lib/device-name'
import { fencedWorldStorage, registerFencedStore, STORAGE_KEYS, syncManager } from '../lib/storage'
import { isWorldEpoch } from '../lib/storage/world-fence'
import { isIconWeight, isPhosphorIconName, isValidHostColor } from '../lib/host-color'
import type { IconWeight, Tab, Workspace } from '../types/tab'

/** The `activeProfileId` of the master; never a slave's id. */
export const MASTER_PROFILE_ID = 'master'

export interface ParkedWorld {
  workspaces: Workspace[]
  tabs: Record<string, Tab>
  activeWorkspaceId: string | null
  activeTabId: string | null
}

/** How a profile looks. Every field optional: absent = the default (the Purdex logo, no colour). */
export interface ProfileAppearance {
  /** A Phosphor icon name from the catalog, as `Workspace.icon`. */
  icon?: string
  /** Only ever with `icon`. */
  iconWeight?: IconWeight
  /** Strict lower-case `#rrggbb`, as a host's colour. */
  color?: string
}

export interface LocalProfile extends ProfileAppearance {
  id: string
  name: string
  createdAt: number
  /** null ⇔ this slave is the one on screen. */
  world: ParkedWorld | null
}

/** The master has no record of its own but this. `name: null` = never named: it is shown as `Home`. */
export interface MasterAppearance extends ProfileAppearance {
  name: string | null
}

/** Absent key = leave as is; `null` = clear. A slave's name cannot be cleared. */
export interface ProfileAppearancePatch {
  name?: string | null
  icon?: string | null
  iconWeight?: IconWeight | null
  color?: string | null
}

interface LocalProfilesData {
  slaves: Record<string, LocalProfile>
  slaveOrder: string[]
  activeProfileId: typeof MASTER_PROFILE_ID | string
  /** Non-null exactly while a slave is on screen. */
  parkedMaster: ParkedWorld | null
  worldEpoch: number
  /** +1 with every promote: the labels of the worlds have moved (see the header). */
  relabelCount: number
  /** The master's appearance (see APPEARANCE). */
  master: MasterAppearance
}

type Refused<R extends string> = { ok: false; reason: R }

export interface LocalProfilesState extends LocalProfilesData {
  /** A new PARKED slave holding `world`, last in the order. The world is kept by reference: hand over one nobody else will mutate. */
  addSlave: (name: string, world: ParkedWorld) => { ok: true; id: string } | Refused<'bad-name' | 'bad-world'>
  renameSlave: (id: string, name: string) => { ok: true } | Refused<'not-found' | 'bad-name'>
  /** Never the one on screen. The removed world is handed back (the caller may need its sessions, or an undo). */
  removeSlave: (id: string) => { ok: true; world: ParkedWorld } | Refused<'not-found' | 'on-screen'>
  /** `order` must be a permutation of the current ids. */
  reorderSlaves: (order: string[]) => { ok: true } | Refused<'bad-order'>
  /** THE EXCHANGE, in one `set`: `onScreen` (what the tab stores hold right now) is parked in the slot of the
   *  profile that was active, `targetId`'s world is taken out and returned, and the pointer and the epoch move.
   *  `worldEpoch` must be a safe integer above the current one. */
  swapActive: (
    targetId: string,
    onScreen: ParkedWorld,
    worldEpoch: number,
  ) => { ok: true; world: ParkedWorld; previousId: string } | Refused<'not-found' | 'already-on-screen' | 'bad-world' | 'bad-epoch'>
  /** A MOVE: the slave's world takes the master slot; the previous master world becomes a new slave named
   *  `demotedName`, in the promoted slave's place in the order; the promoted slave record is gone. One `set`.
   *  No world goes on or off the screen — what is on screen keeps being on screen under whatever label it now has:
   *    master on screen  → the screen is now the demoted slave   (answer: activeProfileId === demotedId)
   *    that slave        → the screen is now the master          (answer: activeProfileId === 'master')
   *    another slave     → untouched                             (answer: activeProfileId unchanged)
   *  The caller re-stamps the tab stores' world tag with the answer's `activeProfileId` and the same epoch. */
  promoteSlave: (
    slaveId: string,
    demotedName: string,
    worldEpoch: number,
  ) => { ok: true; demotedId: string; activeProfileId: string } | Refused<'not-found' | 'bad-name' | 'bad-epoch'>
  /** Name / icon / colour of the master (`'master'`) or a slave. One bad value refuses the whole patch. */
  setProfileAppearance: (
    id: string,
    patch: ProfileAppearancePatch,
  ) => { ok: true } | Refused<'not-found' | 'bad-name' | 'bad-icon' | 'bad-weight' | 'bad-color'>
  /** Overwrite a PARKED world — `'master'` for the parked master (the master keeps syncing while a slave is on
   *  screen, so an apply lands here). The world on screen is not this store's to write. The epoch does not move. */
  replaceParkedWorld: (targetId: string, world: ParkedWorld) => { ok: true } | Refused<'not-found' | 'on-screen' | 'bad-world'>
  /** Map every parked world (owner: a slave id or `'master'`) in one `set`; return the same reference to leave one
   *  alone. A malformed result is ignored for that world. Answers how many changed; zero → no `set` at all. */
  updateParkedWorlds: (fn: (world: ParkedWorld, ownerId: string) => ParkedWorld) => number
}

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const isStringOrNull = (v: unknown): v is string | null => v === null || typeof v === 'string'

/** Structural, and deliberately shallow below a tab's `layout`: deep enough that the tab stores can take the
 *  world without crashing on its outline; pane contents are validated where they are rendered, as for live tabs. */
export function isParkedWorld(v: unknown): v is ParkedWorld {
  if (!isRecord(v)) return false
  if (!Array.isArray(v.workspaces) || !isRecord(v.tabs)) return false
  if (!isStringOrNull(v.activeWorkspaceId) || !isStringOrNull(v.activeTabId)) return false
  for (const ws of v.workspaces) {
    if (!isRecord(ws) || typeof ws.id !== 'string' || ws.id === '' || typeof ws.name !== 'string') return false
    if (!Array.isArray(ws.tabs) || !ws.tabs.every((t) => typeof t === 'string') || !isStringOrNull(ws.activeTabId)) return false
  }
  for (const [key, tab] of Object.entries(v.tabs)) {
    if (!isRecord(tab) || tab.id !== key || !isRecord(tab.layout)) return false
  }
  return true
}

/**
 * Characters nobody can see, which a name must not carry: it is rendered as it is — the Home row, the menu, a
 * `title`, an `aria-label` — and these re-order the text around them or make two names look the same.
 *   - `\p{Cc}`: the C0 / C1 controls, line breaks and tabs included;
 *   - the bidi controls: U+061C, U+200E–U+200F, U+202A–U+202E, U+2066–U+2069;
 *   - zero-width and invisible format characters: U+200B–U+200D, U+2060, U+FEFF, U+00AD.
 * U+200D (ZWJ) is on the list ON PURPOSE although it also joins emoji: a family emoji comes apart into its three people — uglier, not
 * wrong — whereas keeping it would leave a zero-width hole in the rule. Consistency over ligatures.
 * A list, not a category sweep, and NO Unicode normalisation: CJK, emoji with their variation selectors and skin
 * tones, and combining marks (NFC or NFD) come through exactly as typed.
 */
const INVISIBLE_IN_A_NAME = /[\p{Cc}\u061C\u200E\u200F\u202A-\u202E\u2066-\u2069\u200B-\u200D\u2060\uFEFF\u00AD]/gu

/** Remove the invisible characters; THEN trim; blank → null; cut at 64 code points (the device-name rule for
 *  the last three, on purpose: a slave saved before a pull is "named after the host" (spec §4.9), so a device
 *  name must fit). Every way a name gets in — `addSlave`, `renameSlave`, `setProfileAppearance`, `promoteSlave`,
 *  and `merge` on rehydrate, which is how a name stored by an older build gets cleaned. Duplicates are fine. */
export function normalizeLocalProfileName(name: unknown): string | null {
  return typeof name === 'string' ? normalizeDeviceName(name.replace(INVISIBLE_IN_A_NAME, '')) : null
}

/** `generateId` yields six base-36 characters — and 'master' is six base-36 characters. */
function freshSlaveId(taken: Record<string, unknown>): string {
  for (;;) {
    const id = generateId()
    if (id !== MASTER_PROFILE_ID && !Object.hasOwn(taken, id)) return id
  }
}

function isNextEpoch(v: unknown, current: number): v is number {
  return Number.isSafeInteger(v) && (v as number) > current
}

const RECOVERED_SLAVE_NAME = 'Recovered'
const RECOVERED_MASTER_NAME = 'Recovered master'
const RECOVERED_ID = 'recovered'

/** The appearance fields of whatever storage held: a field that is not what it must be is dropped, alone. */
function sanitiseAppearance(v: Record<string, unknown>): ProfileAppearance {
  const out: ProfileAppearance = {}
  if (isPhosphorIconName(v.icon)) {
    out.icon = v.icon
    if (isIconWeight(v.iconWeight)) out.iconWeight = v.iconWeight
  }
  if (isValidHostColor(v.color)) out.color = v.color.toLowerCase()
  return out
}

function sanitiseMaster(v: unknown): MasterAppearance {
  return isRecord(v) ? { name: normalizeLocalProfileName(v.name), ...sanitiseAppearance(v) } : { name: null }
}

/** `patch` applied to `current`, or why not. */
function patchAppearance(
  current: ProfileAppearance,
  patch: ProfileAppearancePatch,
): { ok: true; appearance: ProfileAppearance } | Refused<'bad-icon' | 'bad-weight' | 'bad-color'> {
  const next: ProfileAppearance = { icon: current.icon, iconWeight: current.iconWeight, color: current.color }
  if (patch.icon !== undefined) {
    if (patch.icon !== null && !isPhosphorIconName(patch.icon)) return { ok: false, reason: 'bad-icon' }
    next.icon = patch.icon ?? undefined
  }
  if (patch.iconWeight !== undefined) {
    if (patch.iconWeight !== null && !isIconWeight(patch.iconWeight)) return { ok: false, reason: 'bad-weight' }
    next.iconWeight = patch.iconWeight ?? undefined
  }
  if (patch.color !== undefined) {
    if (patch.color !== null && !isValidHostColor(patch.color)) return { ok: false, reason: 'bad-color' }
    next.color = patch.color?.toLowerCase()
  }
  if (next.icon === undefined) next.iconWeight = undefined // a weight alone means nothing
  // No `undefined`-valued keys: the record is compared and persisted as it is.
  const appearance: ProfileAppearance = {}
  if (next.icon !== undefined) appearance.icon = next.icon
  if (next.iconWeight !== undefined) appearance.iconWeight = next.iconWeight
  if (next.color !== undefined) appearance.color = next.color
  return { ok: true, appearance }
}

/** Just the appearance fields of a record, without `undefined`-valued keys. */
function appearanceOf(p: ProfileAppearance): ProfileAppearance {
  const r = patchAppearance(p, {})
  return r.ok ? r.appearance : {}
}

/** One persisted slave → a well-formed one, or null. Identity and world must be right (there is no guessing an
 *  id, and a malformed world is nothing the tab stores could take); a broken NAME or date is replaced instead,
 *  because dropping the record would throw the user's world away over a label. */
function sanitiseSlave(key: string, v: unknown): LocalProfile | null {
  if (!isRecord(v) || v.id !== key || key === '' || key === MASTER_PROFILE_ID) return null
  if (v.world !== null && !isParkedWorld(v.world)) return null
  return {
    id: key,
    name: normalizeLocalProfileName(v.name) ?? RECOVERED_SLAVE_NAME,
    createdAt: typeof v.createdAt === 'number' && Number.isFinite(v.createdAt) ? v.createdAt : 0,
    world: v.world,
    ...sanitiseAppearance(v),
  }
}

const sanitiseCount = (v: unknown): number => (Number.isSafeInteger(v) && (v as number) >= 0 ? (v as number) : 0)

/**
 * `relabelCount` as `localStorage` holds it RIGHT NOW, by the rule `merge` applies
 * to it; null: absent, unreadable. Another window's promote reaches this window's
 * store an event and a rehydrate later; the storage it persisted to does not wait
 * (as `masterAttachedInStorage` in useProfileStore). For lib/host-lifecycle.ts.
 */
export function relabelCountInStorage(): number | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEYS.LOCAL_PROFILES)
    if (raw === null) return null
    const state = (JSON.parse(raw) as { state?: unknown }).state
    return isRecord(state) ? sanitiseCount(state.relabelCount) : null
  } catch {
    return null
  }
}

/** Whatever storage held → a record that satisfies the invariant, WITHOUT dropping a world that can be kept:
 *  - A slave without a world that is not the one on screen holds nothing; it goes.
 *  - A consistent "slave S on screen" survives other damage (the tab stores hold S's world; moving the pointer
 *    would mislabel it).
 *  - Anything else falls back to "master on screen". A master world found parked at that point contradicts the
 *    pointer, but it is somebody's tabs: it is RESCUED as a parked slave. Its id is deterministic, not random —
 *    `merge` runs in every window over the same storage and they must all arrive at the same state.
 *  - A slave on screen with NO parked master falls back too: inventing an empty master world would hand the
 *    collector an empty world to push. The tab stores then still carry that slave's tag, so Task 4 reads
 *    "unsettled" and reports nothing — silent, never wrong. */
function sanitiseData(persisted: unknown): LocalProfilesData {
  const p = isRecord(persisted) ? persisted : {}

  const slaves: Record<string, LocalProfile> = {}
  if (isRecord(p.slaves)) {
    for (const [key, v] of Object.entries(p.slaves)) {
      const slave = sanitiseSlave(key, v)
      if (slave !== null) slaves[key] = slave
    }
  }

  let parkedMaster = isParkedWorld(p.parkedMaster) ? p.parkedMaster : null
  const pointed = typeof p.activeProfileId === 'string' && Object.hasOwn(slaves, p.activeProfileId) ? slaves[p.activeProfileId] : null
  const activeProfileId = pointed !== null && pointed.world === null && parkedMaster !== null ? pointed.id : MASTER_PROFILE_ID

  for (const slave of Object.values(slaves)) {
    if (slave.world === null && slave.id !== activeProfileId) delete slaves[slave.id]
  }

  const slaveOrder: string[] = []
  if (Array.isArray(p.slaveOrder)) {
    for (const id of p.slaveOrder) {
      if (typeof id === 'string' && Object.hasOwn(slaves, id) && !slaveOrder.includes(id)) slaveOrder.push(id)
    }
  }
  const unlisted = Object.values(slaves).filter((s) => !slaveOrder.includes(s.id))
  unlisted.sort((a, b) => a.createdAt - b.createdAt || (a.id < b.id ? -1 : 1))
  for (const s of unlisted) slaveOrder.push(s.id)

  if (activeProfileId === MASTER_PROFILE_ID && parkedMaster !== null) {
    let id = RECOVERED_ID
    for (let n = 2; Object.hasOwn(slaves, id); n++) id = `${RECOVERED_ID}-${n}`
    slaves[id] = { id, name: RECOVERED_MASTER_NAME, createdAt: 0, world: parkedMaster }
    slaveOrder.push(id)
    parkedMaster = null
  }

  return {
    slaves,
    slaveOrder,
    activeProfileId,
    parkedMaster,
    worldEpoch: isWorldEpoch(p.worldEpoch) ? p.worldEpoch : 0, // beyond the ceiling is junk too (lib/storage/world-fence.ts)
    relabelCount: sanitiseCount(p.relabelCount),
    master: sanitiseMaster(p.master),
  }
}

export const useLocalProfilesStore = create<LocalProfilesState>()(
  persist(
    (set, get) => ({
      slaves: {},
      slaveOrder: [],
      activeProfileId: MASTER_PROFILE_ID,
      parkedMaster: null,
      worldEpoch: 0,
      relabelCount: 0,
      master: { name: null },

      addSlave: (name, world) => {
        const normalized = normalizeLocalProfileName(name)
        if (normalized === null) return { ok: false, reason: 'bad-name' }
        if (!isParkedWorld(world)) return { ok: false, reason: 'bad-world' }
        const s = get()
        const id = freshSlaveId(s.slaves)
        set({
          slaves: { ...s.slaves, [id]: { id, name: normalized, createdAt: Date.now(), world } },
          slaveOrder: [...s.slaveOrder, id],
        })
        return { ok: true, id }
      },

      renameSlave: (id, name) => {
        const s = get()
        if (!Object.hasOwn(s.slaves, id)) return { ok: false, reason: 'not-found' }
        const normalized = normalizeLocalProfileName(name)
        if (normalized === null) return { ok: false, reason: 'bad-name' }
        set({ slaves: { ...s.slaves, [id]: { ...s.slaves[id], name: normalized } } })
        return { ok: true }
      },

      setProfileAppearance: (id, patch) => {
        const s = get()
        const isMaster = id === MASTER_PROFILE_ID
        if (!isMaster && !Object.hasOwn(s.slaves, id)) return { ok: false, reason: 'not-found' }
        const current = isMaster ? s.master : s.slaves[id]
        let name = current.name
        if (patch.name !== undefined) {
          if (patch.name !== null && typeof patch.name !== 'string') return { ok: false, reason: 'bad-name' }
          name = normalizeLocalProfileName(patch.name)
          if (name === null && !isMaster) return { ok: false, reason: 'bad-name' } // only the master can be unnamed
        }
        const patched = patchAppearance(current, patch)
        if (!patched.ok) return patched
        if (isMaster) set({ master: { name, ...patched.appearance } })
        else {
          const { id: slaveId, createdAt, world } = s.slaves[id]
          set({ slaves: { ...s.slaves, [id]: { id: slaveId, name: name as string, createdAt, world, ...patched.appearance } } })
        }
        return { ok: true }
      },

      removeSlave: (id) => {
        const s = get()
        if (!Object.hasOwn(s.slaves, id)) return { ok: false, reason: 'not-found' }
        const world = s.slaves[id].world
        if (world === null || s.activeProfileId === id) return { ok: false, reason: 'on-screen' }
        const slaves = { ...s.slaves }
        delete slaves[id]
        set({ slaves, slaveOrder: s.slaveOrder.filter((x) => x !== id) })
        return { ok: true, world }
      },

      reorderSlaves: (order) => {
        const s = get()
        const isPermutation =
          Array.isArray(order) &&
          order.length === s.slaveOrder.length &&
          new Set(order).size === order.length &&
          order.every((id) => typeof id === 'string' && Object.hasOwn(s.slaves, id))
        if (!isPermutation) return { ok: false, reason: 'bad-order' }
        set({ slaveOrder: [...order] })
        return { ok: true }
      },

      swapActive: (targetId, onScreen, worldEpoch) => {
        const s = get()
        const toMaster = targetId === MASTER_PROFILE_ID
        if (!toMaster && !Object.hasOwn(s.slaves, targetId)) return { ok: false, reason: 'not-found' }
        const taken = toMaster ? s.parkedMaster : s.slaves[targetId].world
        if (taken === null || s.activeProfileId === targetId) return { ok: false, reason: 'already-on-screen' }
        if (!isParkedWorld(onScreen)) return { ok: false, reason: 'bad-world' }
        if (!isNextEpoch(worldEpoch, s.worldEpoch)) return { ok: false, reason: 'bad-epoch' }

        const previousId = s.activeProfileId
        const slaves = { ...s.slaves }
        let parkedMaster = s.parkedMaster
        if (previousId === MASTER_PROFILE_ID) parkedMaster = onScreen
        else slaves[previousId] = { ...slaves[previousId], world: onScreen }
        if (toMaster) parkedMaster = null
        else slaves[targetId] = { ...slaves[targetId], world: null }

        set({ slaves, parkedMaster, activeProfileId: targetId, worldEpoch })
        return { ok: true, world: taken, previousId }
      },

      promoteSlave: (slaveId, demotedName, worldEpoch) => {
        const s = get()
        if (!Object.hasOwn(s.slaves, slaveId)) return { ok: false, reason: 'not-found' }
        const name = normalizeLocalProfileName(demotedName)
        if (name === null) return { ok: false, reason: 'bad-name' }
        if (!isNextEpoch(worldEpoch, s.worldEpoch)) return { ok: false, reason: 'bad-epoch' }

        // The taken-set still holds `slaveId`: a demoted slave must not reuse the id of the one it replaces, or a
        // window that rehydrates mid-way could not tell the two apart.
        const demotedId = freshSlaveId(s.slaves)
        const promoted = s.slaves[slaveId]
        const slaves = { ...s.slaves }
        delete slaves[slaveId]
        // Each side simply takes the other's slot, `null` ("on screen") included — which is what makes the three
        // cases one: the master slot gets the slave's world, the new slave gets what the master slot held.
        // The look goes with the world (see APPEARANCE): the old master's — and its name, if it had one; `demotedName`
        // is for a master nobody named — to the new slave, the promoted slave's to the master.
        slaves[demotedId] = { id: demotedId, name: s.master.name ?? name, createdAt: Date.now(), world: s.parkedMaster, ...appearanceOf(s.master) }
        const activeProfileId =
          s.activeProfileId === MASTER_PROFILE_ID ? demotedId : s.activeProfileId === slaveId ? MASTER_PROFILE_ID : s.activeProfileId

        set({
          slaves,
          slaveOrder: s.slaveOrder.map((id) => (id === slaveId ? demotedId : id)),
          parkedMaster: promoted.world,
          master: { name: promoted.name, ...appearanceOf(promoted) },
          activeProfileId,
          worldEpoch,
          relabelCount: s.relabelCount + 1,
        })
        return { ok: true, demotedId, activeProfileId }
      },

      replaceParkedWorld: (targetId, world) => {
        const s = get()
        const toMaster = targetId === MASTER_PROFILE_ID
        if (!toMaster && !Object.hasOwn(s.slaves, targetId)) return { ok: false, reason: 'not-found' }
        const current = toMaster ? s.parkedMaster : s.slaves[targetId].world
        if (current === null || s.activeProfileId === targetId) return { ok: false, reason: 'on-screen' }
        if (!isParkedWorld(world)) return { ok: false, reason: 'bad-world' }
        set(toMaster ? { parkedMaster: world } : { slaves: { ...s.slaves, [targetId]: { ...s.slaves[targetId], world } } })
        return { ok: true }
      },

      updateParkedWorlds: (fn) => {
        const s = get()
        let changed = 0
        const mapped = (world: ParkedWorld, ownerId: string): ParkedWorld => {
          const next = fn(world, ownerId)
          if (next === world || !isParkedWorld(next)) return world
          changed++
          return next
        }
        const parkedMaster = s.parkedMaster === null ? null : mapped(s.parkedMaster, MASTER_PROFILE_ID)
        const slaves: Record<string, LocalProfile> = {}
        for (const [id, slave] of Object.entries(s.slaves)) {
          const world = slave.world === null ? null : mapped(slave.world, id)
          slaves[id] = world === slave.world ? slave : { ...slave, world }
        }
        if (changed > 0) set({ slaves, parkedMaster })
        return changed
      },
    }),
    {
      name: STORAGE_KEYS.LOCAL_PROFILES,
      // A world store: a write from a window that still holds an older world is dropped (lib/storage/world-fence.ts).
      storage: fencedWorldStorage,
      version: 1,
      partialize: (state) => ({
        slaves: state.slaves,
        slaveOrder: state.slaveOrder,
        activeProfileId: state.activeProfileId,
        parkedMaster: state.parkedMaster,
        worldEpoch: state.worldEpoch,
        relabelCount: state.relabelCount,
        master: state.master,
      }),
      // Only the seven sanitised fields ever come out of storage: persisted junk
      // can neither add a key nor replace an action.
      merge: (persisted, current) => ({ ...current, ...sanitiseData(persisted) }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.LOCAL_PROFILES, useLocalProfilesStore)
registerFencedStore(STORAGE_KEYS.LOCAL_PROFILES, useLocalProfilesStore)
