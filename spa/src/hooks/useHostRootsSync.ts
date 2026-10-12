// spa/src/hooks/useHostRootsSync.ts — keeps a lead host's view of its grants on other hosts fresh while an editor is shown (XR-3):
// one read when the first editor of the lead host is shown, then every 10 s only while a change is pending (the daemon has no event
// for it), from ONE timer per lead host however many team editors are open (lib/team/host-roots.ts). Unmounted = collapsed = released.
import { useEffect } from 'react'
import { retainHostRootsPoll } from '../lib/team/host-roots'

export function useHostRootsSync(leadId: string): void {
  useEffect(() => retainHostRootsPoll(leadId), [leadId])
}
