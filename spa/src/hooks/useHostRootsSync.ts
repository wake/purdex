// spa/src/hooks/useHostRootsSync.ts — keeps a lead host's view of its grants on other hosts fresh while an editor is shown (XR-3):
// one read on mount, then every 10 s only while a change is pending (the daemon has no event for it). Unmounted = collapsed = stops.
import { useEffect } from 'react'
import { HOST_ROOTS_POLL_MS, hasPending, refreshHostRoots, useHostRootsStore, viewOf } from '../lib/team/host-roots'

export function useHostRootsSync(leadId: string): void {
  const pending = useHostRootsStore((s) => hasPending(viewOf(s, leadId).hosts))
  useEffect(() => { void refreshHostRoots(leadId) }, [leadId])
  useEffect(() => {
    if (!pending) return
    const id = setInterval(() => { void refreshHostRoots(leadId) }, HOST_ROOTS_POLL_MS)
    return () => clearInterval(id)
  }, [leadId, pending])
}
