// spa/src/components/journal/useSingleFlight.ts — `fn` as a click handler that is ignored while the call it started has not settled
// (a double click sends one request).
import { useCallback, useRef } from 'react'

export function useSingleFlight(fn: () => Promise<unknown>): () => void {
  const busy = useRef(false)
  return useCallback(() => {
    if (busy.current) return
    busy.current = true
    void fn().finally(() => { busy.current = false })
  }, [fn])
}
