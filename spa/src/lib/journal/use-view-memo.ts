// spa/src/lib/journal/use-view-memo.ts — follow a unit's view memo from a component. The memo stays outside React
// (view-memory.ts); this only re-renders when any memo changes. `null` unit = nothing to remember (the unit is not loaded).
import { useSyncExternalStore } from 'react'
import { initialViewMemo, patchViewMemo, readViewMemo, subscribeViewMemos, viewMemoRev, type ViewMemo } from './view-memory'

export function useViewMemo(hostId: string | null, unitKey: string | null): ViewMemo {
  useSyncExternalStore(subscribeViewMemos, viewMemoRev)
  return hostId !== null && unitKey !== null ? readViewMemo(hostId, unitKey) : initialViewMemo()
}

export function patchUnitMemo(hostId: string | null, unitKey: string | null, patch: Partial<ViewMemo>): void {
  if (hostId !== null && unitKey !== null) patchViewMemo(hostId, unitKey, patch)
}
