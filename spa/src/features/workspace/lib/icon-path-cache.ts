export type PathData = string | Array<string | { d: string; o: number }>
type WeightData = Record<string, PathData>

const cache = new Map<string, WeightData>()
const inflight = new Map<string, Promise<void>>()

/** Prefetch a weight's path data. Deduplicates concurrent calls for the same weight. */
export async function prefetchWeight(weight: string): Promise<void> {
  if (cache.has(weight)) return
  if (inflight.has(weight)) return inflight.get(weight)

  const promise = (async () => {
    const res = await fetch(`/icons/${weight}.json`)
    if (!res.ok) throw new Error(`Failed to fetch icon weight "${weight}": ${res.status}`)
    const data: WeightData = await res.json()
    cache.set(weight, data)
  })()

  inflight.set(weight, promise)
  try {
    await promise
  } finally {
    inflight.delete(weight)
  }
}

/** Sync path lookup — returns null if weight not yet cached */
export function getIconPath(name: string, weight: string): PathData | null {
  return cache.get(weight)?.[name] ?? null
}

/** Check if weight is loaded */
export function isWeightLoaded(weight: string): boolean {
  return cache.has(weight)
}
