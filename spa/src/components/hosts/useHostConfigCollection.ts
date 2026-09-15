// spa/src/components/hosts/useHostConfigCollection.ts — the save path shared by
// the two daemon-backed collection editors (Projects and Commands).
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
import { HostConfigConflictError, type HostCommand, type HostProject } from '../../lib/host-config-api'
import { hostConfigQueueKey, queueHostConfigSave } from '../../lib/host-config-queue'
import { MAX_CONFIG_ITEMS } from '../../lib/host-config-validate'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'

/** Where a save failure is shown: inside the open dialog, or above the list. */
export type ErrorTarget = 'dialog' | 'list'
export interface SaveError { target: ErrorTarget; text: string }

export type CollectionKind = 'projects' | 'commands'
type CollectionItem = HostProject | HostCommand

/** An item the daemon stores under a stable id — what every action addresses. */
export interface WithId { id: string }

/** The limit is re-checked when the action runs, not when the button was drawn. */
class ItemLimitError extends Error {}

function readItems(hostId: string, kind: CollectionKind): CollectionItem[] {
  return useHostConfigStore.getState().byHost[hostId]?.[kind] ?? []
}

function writeItems(hostId: string, kind: CollectionKind, items: CollectionItem[]): Promise<void> {
  const store = useHostConfigStore.getState()
  return kind === 'projects'
    ? store.saveProjects(hostId, items as HostProject[])
    : store.saveCommands(hostId, items as HostCommand[])
}

export interface HostConfigCollectionOps<T extends WithId> {
  /** A save is in flight — for the dialog and the Add button, never for row actions. */
  pending: boolean
  saveError: SaveError | null
  /** Clear the error shown on one surface (opening or closing the dialog). */
  clearSaveError: (target?: ErrorTarget) => void
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
  const [pending, setPending] = useState(false)
  const [saveError, setSaveError] = useState<SaveError | null>(null)
  // Synchronous depth: React state lags a second action fired in the same tick.
  const depth = useRef(0)

  const describe = useCallback((err: unknown): string => {
    if (err instanceof ItemLimitError) return t('host_config.limit', { max: MAX_CONFIG_ITEMS })
    if (err instanceof HostConfigConflictError) return t('host_config.conflict')
    // A 400's message is the daemon's body text (host-config-api `failure`).
    return t('host_config.save_failed', { reason: err instanceof Error ? err.message : String(err) })
  }, [t])

  const enqueue = useCallback((
    mutate: (current: CollectionItem[]) => CollectionItem[],
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
        if (next !== current) await writeItems(hostId, kind, next)
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
    if (!exists && current.length >= MAX_CONFIG_ITEMS) throw new ItemLimitError()
    return exists
      ? current.map((i) => (i.id === item.id ? (item as unknown as CollectionItem) : i))
      : [...current, item as unknown as CollectionItem]
  }, target), [enqueue])

  return { pending, saveError, clearSaveError, move, remove, upsert }
}
