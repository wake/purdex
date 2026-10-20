// spa/src/lib/pool/use-host-series.ts — the host page's hook onto `host-history.ts` (monitoring tabs plan MON-6c). A short range (30 minutes and under) reads wwd's 5 s records once
// when the page mounts and then takes each new facts record as it arrives; a long range reads the daemon's 1 minute series once and tops it up every minute from its last sample.
// Leaving calls off what is still out. The fleet is not read.
import { useEffect, useRef, useState } from 'react'
import { usePoolStore } from './store'
import { appendLive, invalidateHostHistory, loadLong, loadShort, readHostLoad, readSamples, readsFiveSecond, seriesAnnounced, type HostLoad, type HostRange, type HostSample } from './host-history'
import type { PoolFacts } from './types'

const TOP_UP_MS = 60_000

export function useHostSeries(hostId: string, alias: string | null, range: HostRange, liveFacts: PoolFacts | null): { samples: HostSample[]; load: HostLoad; rev: number } {
  const source = usePoolStore((s) => s.sourceHostId)
  // The memory is the source; this count only makes a finished read, or a new live record, draw again.
  const [rev, setRev] = useState(0)
  const bump = () => setRev((n) => n + 1)
  const five = readsFiveSecond(range)
  // The host's constants for the long track (its total memory and cores), read when the effect runs; they do not restart it.
  const constants = useRef({ memTotalMb: null as number | null, ncpu: null as number | null })
  constants.current = { memTotalMb: liveFacts?.mem.total_mb ?? null, ncpu: liveFacts?.cpu.ncpu ?? null }

  useEffect(() => {
    if (!five || !source || !alias) return
    let cancelled = false
    void loadShort(source, alias, hostId, Date.now()).then(() => { if (!cancelled) bump() })
    return () => { cancelled = true; invalidateHostHistory(hostId, 'short') }
  }, [five, source, alias, hostId])

  const at = liveFacts?.at ?? null
  useEffect(() => {
    if (!liveFacts) return
    appendLive(hostId, liveFacts)
    bump()
    // Only a new record time is news; the object itself changes with every pool answer.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hostId, at])

  const longOn = !five && !!source && !!alias && seriesAnnounced(source)
  useEffect(() => {
    if (!longOn || !source || !alias) return
    let cancelled = false
    const run = (refresh: boolean) => {
      void loadLong({ source, alias, hostId, range, ...constants.current, now: Date.now(), refresh }).then(() => { if (!cancelled) bump() })
    }
    run(false)
    const timer = setInterval(() => run(true), TOP_UP_MS)
    return () => { cancelled = true; clearInterval(timer); invalidateHostHistory(hostId, 'long') }
  }, [longOn, source, alias, hostId, range])

  return { samples: readSamples(hostId, range, Date.now()), load: readHostLoad(hostId, five ? 'short' : 'long'), rev }
}
