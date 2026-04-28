import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore } from '../useHostStore'
import { usePathCacheStore } from './usePathCacheStore'

interface WorkspaceLikeState {
  workspaces: { id: string }[]
  _lastRemovedKeepSettings?: string
}

interface HostLikeState {
  hostOrder: string[]
}

/**
 * Subscribe path-cache to workspace + host removal events. Returns a dispose
 * function — caller MUST invoke it on HMR dispose / test cleanup.
 *
 * Tear-off / merge semantics: when removeWorkspace(id, {keepSettings:true})
 * runs, the workspace store sets _lastRemovedKeepSettings = id. We treat any
 * removal whose id matches that hint as a tear-off and skip cleanup entirely;
 * in-memory + persisted cache both survive. Real deletes (keepSettings absent
 * or false) clear the scope across every host (the cache is keyed by
 * hostId:wsId, so a single workspace can have entries on multiple hosts).
 *
 * Hydration race: defers wiring the workspace subscriber until persist
 * finishes hydration so the empty in-memory baseline isn't compared against
 * the just-restored persisted workspaces and incorrectly treated as a wipe.
 * Host store is non-persisted so it can attach immediately.
 */
export function attachPathCacheAutoCleanup(): () => void {
  let unsubWs: (() => void) | undefined
  let unsubHost: (() => void) | undefined
  let disposed = false

  const start = () => {
    if (disposed) return

    unsubWs = useWorkspaceStore.subscribe((state, prevState) => {
      const prev = prevState as unknown as WorkspaceLikeState
      const curr = state as unknown as WorkspaceLikeState
      const prevIds = new Set(prev.workspaces.map((w) => w.id))
      const currIds = new Set(curr.workspaces.map((w) => w.id))
      const removed: string[] = []
      for (const id of prevIds) if (!currIds.has(id)) removed.push(id)
      if (removed.length === 0) return

      const keepId = curr._lastRemovedKeepSettings
      const dirs = usePathCacheStore.getState().dirsByScope
      for (const wsId of removed) {
        if (wsId === keepId) continue
        for (const key of Object.keys(dirs)) {
          const sepIdx = key.indexOf(':')
          if (sepIdx <= 0) continue
          const hostId = key.slice(0, sepIdx)
          const scopeWsId = key.slice(sepIdx + 1)
          if (scopeWsId === wsId) {
            usePathCacheStore.getState().clearScope(hostId, scopeWsId)
          }
        }
      }
    })

    unsubHost = useHostStore.subscribe((state, prevState) => {
      const prev = prevState as unknown as HostLikeState
      const curr = state as unknown as HostLikeState
      const prevIds = new Set(prev.hostOrder)
      const currIds = new Set(curr.hostOrder)
      for (const id of prevIds) {
        if (!currIds.has(id)) usePathCacheStore.getState().clearHost(id)
      }
    })
  }

  if (useWorkspaceStore.persist.hasHydrated()) {
    start()
  } else {
    const finishUnsub = useWorkspaceStore.persist.onFinishHydration(() => {
      finishUnsub()
      start()
    })
  }

  return () => {
    disposed = true
    unsubWs?.()
    unsubHost?.()
    unsubWs = unsubHost = undefined
  }
}
