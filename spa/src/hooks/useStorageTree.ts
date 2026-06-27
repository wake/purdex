/**
 * useStorageTree — builds the full In-App storage tree once and owns the
 * UI-only expand/collapse state (keyed by FULL path, not basename).
 *
 * The whole tree is enumerated eagerly via `listTreeUnder` (the in-app store is
 * small), so `toggle` never fetches — it only flips membership in `expanded`.
 * `refresh` re-reads the backend (after a CRUD mutation owned by other tasks).
 */
import { useCallback, useEffect, useState } from 'react'
import { getFsBackend } from '../lib/fs-backend'
import { STORAGE_ROOT } from '../lib/storage-paths'
import { listTreeUnder, type TreeNode } from '../lib/storage-tree'

export interface UseStorageTreeResult {
  tree: TreeNode[]
  loading: boolean
  error: string | null
  /** Re-read the backend and rebuild the tree. */
  refresh: () => void
  /** Full paths of currently expanded directories. */
  expanded: Set<string>
  /** Flip the expanded state of a directory, keyed by its full path. */
  toggle: (path: string) => void
}

export function useStorageTree(): UseStorageTreeResult {
  const [tree, setTree] = useState<TreeNode[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [nonce, setNonce] = useState(0)

  const refresh = useCallback(() => setNonce((n) => n + 1), [])

  const toggle = useCallback((path: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })
  }, [])

  useEffect(() => {
    let cancelled = false
    // Resolve + enumerate inside a promise so every setState is deferred off the
    // synchronous effect body (react-hooks/set-state-in-effect).
    Promise.resolve()
      .then(() => {
        const backend = getFsBackend({ type: 'inapp' })
        if (!backend) throw new Error('In-App storage backend unavailable')
        return listTreeUnder(backend, STORAGE_ROOT)
      })
      .then((nodes) => {
        if (cancelled) return
        setTree(nodes)
        setError(null)
        setLoading(false)
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setTree([])
        setError(err instanceof Error ? err.message : String(err))
        setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [nonce])

  return { tree, loading, error, refresh, expanded, toggle }
}
