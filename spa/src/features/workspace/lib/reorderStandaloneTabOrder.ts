/**
 * Reorders the standalone tab subset within a global tabOrder array.
 * Non-standalone tabs keep their relative positions; the standalone subset
 * is replaced by `newOrder` and re-inserted at the index where the first
 * standalone originally appeared.
 *
 * If `newOrder` is empty, the original array is returned unchanged.
 */
export function reorderStandaloneTabOrder(current: string[], newOrder: string[]): string[] {
  if (newOrder.length === 0) return current.slice()
  const standaloneSet = new Set(newOrder)
  const kept: string[] = []
  let insertIndex = -1
  for (const id of current) {
    if (standaloneSet.has(id)) {
      if (insertIndex === -1) insertIndex = kept.length
    } else {
      kept.push(id)
    }
  }
  if (insertIndex === -1) insertIndex = kept.length
  kept.splice(insertIndex, 0, ...newOrder)
  return kept
}
