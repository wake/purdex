import { useCallback, useEffect, useState } from 'react'
import { getSnapshotStore } from '../../../../../lib/sync/snapshot-store-instance'
import type { SnapshotMetadata } from '../../../../../lib/sync/snapshot-types'

export interface UseLocalHistoryResult {
  items: SnapshotMetadata[]
  loading: boolean
  error: Error | null
  refresh: () => Promise<void>
}

export function useLocalHistory(): UseLocalHistoryResult {
  const [items, setItems] = useState<SnapshotMetadata[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<Error | null>(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const store = getSnapshotStore()
      await store.init()
      const list = await store.listLocal()
      setItems(list)
      setError(null)
    } catch (e) {
      setError(e as Error)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  return { items, loading, error, refresh }
}
