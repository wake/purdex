// spa/src/lib/pool/use-cell-models.ts — runs the pool store's poll while a strip of cells (or a view of the pool) is mounted, and keeps the
// daemon identities fresh. The hook that turns fleet hosts into cell models is in fleet-bridge.ts.
import { useEffect, useState } from 'react'
import { usePoolStore } from './store'
import { NEX_HOST_TTL_MS } from '../../stores/useNexHostStore'
import { refreshPoolIdentities } from './daemons'

/** How often the freshness of the facts (30 s) is looked at again when nothing new arrives. */
const CLOCK_MS = 5000

// Cells can be mounted more than once (wide and narrow bars): the poll runs while any is.
let mounted = 0

/** Holds the pool's poll open (the same count `useRunPool` uses); the returned function lets go, once. */
export function retainPool(): () => void {
  if (mounted++ === 0) usePoolStore.getState().start()
  let released = false
  return () => {
    if (released) return
    released = true
    if (--mounted === 0) usePoolStore.getState().stop()
  }
}

export function useRunPool() {
  useEffect(() => {
    const release = retainPool()
    // A daemon that learned its wwd after the App first read /api/info is picked up when the pool's hosts change.
    refreshPoolIdentities()
    const unsubscribe = usePoolStore.subscribe((s, prev) => { if (s.hosts !== prev.hosts) refreshPoolIdentities() })
    // An event-driven pool stops polling, so nothing else would ask again once the 60 s cache TTL has passed.
    const timer = setInterval(refreshPoolIdentities, NEX_HOST_TTL_MS)
    return () => { unsubscribe(); clearInterval(timer); release() }
  }, [])
}

export function useClock(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), CLOCK_MS)
    return () => clearInterval(id)
  }, [])
  return now
}
