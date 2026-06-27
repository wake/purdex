/**
 * storage-dnd — the pure drag-and-drop resolver for the Storage pane, split out
 * of `storage-actions` (codex H3) so the data-layer CRUD module stays free of
 * dnd-kit concerns and this thin mapping is unit-testable on its own. The repo
 * precedent is `features/workspace/lib/computeDragEndAction.ts`.
 */

/**
 * Resolve a dnd-kit drop into the `{ from, targetDir }` pair for
 * `moveStorageEntry`. The drop target's directory is read from
 * `over.data.current.targetDir` — the **authoritative** value each droppable
 * publishes (codex B1):
 *
 *   - a **folder** row publishes its own path,
 *   - a **file** row publishes its PARENT directory (dropping onto a file means
 *     "into the folder that file lives in" — the common file-manager gesture;
 *     dropping onto a same-directory sibling then collapses to a no-op inside
 *     `moveStorageEntry` because the target dir equals the source's parent),
 *   - the root region publishes `STORAGE_ROOT`.
 *
 * Reading `targetDir` from the droppable's data (rather than inferring it from
 * `over.id`) removes the old ambiguity where a drop onto a file row — which had
 * no droppable — fell through to the root zone and silently moved the entry back
 * to the storage root. Returns `null` (caller no-ops) when there is no drop
 * target or the target published no `targetDir`.
 */
export function computeMoveFromDragEnd(
  active: { id: string | number } | null | undefined,
  over:
    | { id: string | number; data?: { current?: { targetDir?: unknown } } }
    | null
    | undefined,
): { from: string; targetDir: string } | null {
  if (!active || !over) return null
  const targetDir = over.data?.current?.targetDir
  if (typeof targetDir !== 'string') return null
  return { from: String(active.id), targetDir }
}
