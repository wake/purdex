// spa/src/lib/pool/use-proc-history.ts — the 行程 tab's hook onto `proc-history.ts` (monitoring tabs plan MON-8b). Nothing is read until `wanted` (somebody moved the timeline): then the last hour of 5 s records is read once
// (not again within 5 minutes of a good read) and each new facts record joins it. The memory is shared per host; a read is called off when the last page holding it leaves.
import { useEffect, useState } from 'react'
import { usePoolStore } from './store'
import { appendLiveProcs, holdProcs, loadProcHistory, procsFresh, readProcLoad, readProcSnaps, subscribeProcs, type ProcLoad, type ProcSnap } from './proc-history'
import type { PoolFacts } from './types'

export function useProcHistory(hostId: string, alias: string | null, liveFacts: PoolFacts | null, wanted: boolean): { snaps: ProcSnap[]; load: ProcLoad } {
  const source = usePoolStore((s) => s.sourceHostId)
  // The memory is the source; this count only makes a change to it draw again.
  const [, setRev] = useState(0)
  useEffect(() => subscribeProcs(hostId, () => setRev((n) => n + 1)), [hostId])

  useEffect(() => {
    if (!wanted || !source || !alias) return
    const release = holdProcs(hostId)
    if (!procsFresh(hostId, Date.now())) void loadProcHistory(source, alias, hostId, Date.now())
    return release
  }, [wanted, source, alias, hostId])

  const at = liveFacts?.at ?? null
  useEffect(() => {
    if (!wanted || !liveFacts) return
    appendLiveProcs(hostId, liveFacts)
    // Only a new record time is news; the object itself changes with every pool answer.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wanted, hostId, at])

  return { snaps: wanted ? readProcSnaps(hostId) : [], load: readProcLoad(hostId) }
}
