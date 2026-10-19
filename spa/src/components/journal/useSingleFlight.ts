// spa/src/components/journal/useSingleFlight.ts — `fn` as a click handler that is ignored while the call it started has not settled
// (a double click sends one request). One flight per `key` (the target: host + session): the same hook instance is reused when the
// stack switches to another target, and a retry still out for one must not swallow the other's, nor may its late end release the other's.
import { useCallback, useRef } from 'react'

export function useSingleFlight(key: string, fn: () => Promise<unknown>): () => void {
  const inflight = useRef(new Set<string>())
  return useCallback(() => {
    if (inflight.current.has(key)) return
    inflight.current.add(key)
    void fn().finally(() => { inflight.current.delete(key) })
  }, [key, fn])
}
