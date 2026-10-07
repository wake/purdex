// spa/src/components/hosts/useHostConfigCollection.ts — everything the
// daemon-backed list editors (Projects, Commands, Quick replies) do the same way:
// the gate, the item limit, the edit dialog's open/close/commit, the
// delete confirmation, and the save path below. The sections keep what is
// genuinely theirs — their fields and validation, their dialog, their rows.
//
// Two rules live here, and they are the reason this is not written twice:
//
//  1. **An action is a transformation, not a list.** A row action used to build
//     its replacement list from the array the render captured and the row's
//     position in it. Both go stale the moment anything else writes — another
//     click, a conflict reload, another client's copy — and the PUT would then
//     carry a list assembled from a world that no longer exists. Every action
//     here is a function of whatever the store holds when it RUNS, and it
//     names its row by id, never by index.
//
//  2. **One PUT at a time per host and collection** (`queueHostConfigSave`).
//     The `saving` flag that used to guard this is React state: a second click
//     in the same tick sees the pre-render value and races the first, and the
//     loser's 409 reload discards the user's intent.
//
// The UI keeps its buttons live while a save is in flight; queueing is what
// makes that safe, and it is what keeps the second intent.
import { useCallback, useRef, useState } from 'react'
import { HostConfigConflictError, type HostCommand, type HostProject, type QuickReply } from '../../lib/host-config-api'
import { hostConfigQueueKey, queueHostConfigSave } from '../../lib/host-config-queue'
import { MAX_CONFIG_ITEMS } from '../../lib/host-config-validate'
import { effectiveQuickReplies, MAX_QUICK_REPLIES } from '../../lib/quick-replies'
import { useHostConfigStore, type HostConfigEntry, type HostConfigProblem } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostConfigGate, type GateNotice } from './HostConfigNotice'

/** Where a save failure is shown: inside the open dialog, or above the list. */
export type ErrorTarget = 'dialog' | 'list'
export interface SaveError { target: ErrorTarget; text: string }

type CollectionItem = HostProject | HostCommand | QuickReply

/**
 * How each collection is read from a host's entry, written back, and capped.
 * Spelled out per kind: a kind is the DAEMON's collection name, which need not
 * equal the entry field that holds it, so `entry[kind]` is never a lookup.
 */
interface CollectionBinding {
  read: (entry: HostConfigEntry) => readonly CollectionItem[]
  problem: (entry: HostConfigEntry) => HostConfigProblem | undefined
  save: (hostId: string, items: CollectionItem[]) => Promise<void>
  max: number
}

const COLLECTIONS = {
  projects: {
    read: (entry) => entry.projects,
    problem: (entry) => entry.problems.projects,
    save: (hostId, items) => useHostConfigStore.getState().saveProjects(hostId, items as HostProject[]),
    max: MAX_CONFIG_ITEMS,
  },
  commands: {
    read: (entry) => entry.commands,
    problem: (entry) => entry.problems.commands,
    save: (hostId, items) => useHostConfigStore.getState().saveCommands(hostId, items as HostCommand[]),
    max: MAX_CONFIG_ITEMS,
  },
  // A list never written reads as the defaults, so the first action writes
  // them — edited — for real.
  'quick-replies': {
    read: (entry) => effectiveQuickReplies(entry),
    problem: (entry) => entry.problems.quickReplies,
    save: (hostId, items) => useHostConfigStore.getState().saveQuickReplies(hostId, items as QuickReply[]),
    max: MAX_QUICK_REPLIES,
  },
} satisfies Record<string, CollectionBinding>

export type CollectionKind = keyof typeof COLLECTIONS

/** An item the daemon stores under a stable id — what every action addresses. */
export interface WithId { id: string }

/** The limit is re-checked when the action runs, not when the button was drawn. */
class ItemLimitError extends Error {
  readonly max: number
  constructor(max: number) {
    super(`at most ${max} items`)
    this.max = max
  }
}

const NO_ITEMS: readonly CollectionItem[] = Object.freeze([])

function readItems(hostId: string, kind: CollectionKind): readonly CollectionItem[] {
  const entry = useHostConfigStore.getState().byHost[hostId]
  return entry ? COLLECTIONS[kind].read(entry) : NO_ITEMS
}

export interface HostConfigCollectionOps<T extends WithId> {
  /** This host's copy of the collection — the list to render. */
  items: T[]
  /** Editing is allowed: the host is online and its config loaded. */
  editable: boolean
  /** Why editing is not allowed, for `HostConfigNotice`. */
  notice: GateNotice | null
  /** The host's stored copy was malformed (#1489), for `HostConfigProblemNotice`. */
  problem: HostConfigProblem | undefined
  /** The collection is full: no row may be added. */
  atLimit: boolean
  /** A save is in flight — for the dialog and the Add button, never for row actions. */
  pending: boolean
  saveError: SaveError | null

  /** The item the edit dialog is open on, and whether it is not in the list yet. */
  editing: T | null
  isNew: boolean
  openEditor: (item: T) => void
  closeEditor: () => void
  /** Save the dialog's item; the dialog closes only once the host took it. */
  submit: (item: T) => void

  /** The row whose delete is awaiting confirmation. */
  deleting: string | null
  askDelete: (id: string) => void
  cancelDelete: () => void
  confirmDelete: (id: string) => void

  /** Swap the row with `id` with its neighbour. Resolves true when the save landed. */
  move: (id: string, delta: -1 | 1) => Promise<boolean>
  remove: (id: string) => Promise<boolean>
  /** Replace the item with the same id, or append it. Refused past the item limit. */
  upsert: (item: T, target?: ErrorTarget) => Promise<boolean>
}

export function useHostConfigCollection<T extends WithId>(
  hostId: string,
  kind: CollectionKind,
): HostConfigCollectionOps<T> {
  const t = useI18nStore((s) => s.t)
  const { entry, editable, notice } = useHostConfigGate(hostId)
  // The caller names the item type that goes with `kind`; the store holds the
  // union, and this is the one place the two are tied together.
  const binding: CollectionBinding = COLLECTIONS[kind]
  const items = binding.read(entry) as unknown as T[]
  const [pending, setPending] = useState(false)
  const [saveError, setSaveError] = useState<SaveError | null>(null)
  const [editing, setEditing] = useState<T | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)
  // Synchronous depth: React state lags a second action fired in the same tick.
  const depth = useRef(0)

  const describe = useCallback((err: unknown): string => {
    if (err instanceof ItemLimitError) return t('host_config.limit', { max: err.max })
    if (err instanceof HostConfigConflictError) return t('host_config.conflict')
    // A 400's message is the daemon's body text (host-config-api `failure`).
    return t('host_config.save_failed', { reason: err instanceof Error ? err.message : String(err) })
  }, [t])

  const enqueue = useCallback((
    mutate: (current: readonly CollectionItem[]) => readonly CollectionItem[],
    target: ErrorTarget,
  ): Promise<boolean> => {
    depth.current += 1
    setPending(true)
    const run = async (): Promise<boolean> => {
      setSaveError(null)
      try {
        const current = readItems(hostId, kind)
        const next = mutate(current)
        // An action with nothing to do — a row already gone, an edge move —
        // never spends a revision on the daemon.
        if (next !== current) await COLLECTIONS[kind].save(hostId, next.slice())
        return true
      } catch (err) {
        setSaveError({ target, text: describe(err) })
        return false
      } finally {
        depth.current -= 1
        if (depth.current === 0) setPending(false)
      }
    }
    return queueHostConfigSave(hostConfigQueueKey(hostId, kind), run)
  }, [describe, hostId, kind])

  const clearSaveError = useCallback((target?: ErrorTarget) => {
    setSaveError((e) => (!e || !target || e.target === target ? null : e))
  }, [])

  const move = useCallback((id: string, delta: -1 | 1) => enqueue((current) => {
    const index = current.findIndex((i) => i.id === id)
    const to = index + delta
    if (index < 0 || to < 0 || to >= current.length) return current
    const next = current.slice()
    ;[next[index], next[to]] = [next[to], next[index]]
    return next
  }, 'list'), [enqueue])

  const remove = useCallback((id: string) => enqueue((current) => {
    const next = current.filter((i) => i.id !== id)
    return next.length === current.length ? current : next
  }, 'list'), [enqueue])

  const upsert = useCallback((item: T, target: ErrorTarget = 'dialog') => enqueue((current) => {
    const exists = current.some((i) => i.id === item.id)
    const { max } = COLLECTIONS[kind]
    if (!exists && current.length >= max) throw new ItemLimitError(max)
    return exists
      ? current.map((i) => (i.id === item.id ? (item as unknown as CollectionItem) : i))
      : [...current, item as unknown as CollectionItem]
  }, target), [enqueue, kind])

  const openEditor = useCallback((item: T) => {
    clearSaveError()
    setEditing(item)
  }, [clearSaveError])

  const closeEditor = useCallback(() => {
    setEditing(null)
    // The list keeps its own failure; only the dialog's goes with the dialog.
    clearSaveError('dialog')
  }, [clearSaveError])

  const submit = useCallback((item: T) => {
    void upsert(item).then((saved) => { if (saved) setEditing(null) })
  }, [upsert])

  const confirmDelete = useCallback((id: string) => {
    setDeleting(null)
    void remove(id)
  }, [remove])

  return {
    items,
    editable,
    notice,
    problem: binding.problem(entry),
    atLimit: items.length >= binding.max,
    pending,
    saveError,
    editing,
    isNew: !!editing && !items.some((i) => i.id === editing.id),
    openEditor,
    closeEditor,
    submit,
    deleting,
    askDelete: setDeleting,
    cancelDelete: useCallback(() => setDeleting(null), []),
    confirmDelete,
    move,
    remove,
    upsert,
  }
}
