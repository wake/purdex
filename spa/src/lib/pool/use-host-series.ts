// spa/src/lib/pool/use-host-series.ts — the host page's hook onto `host-history.ts` (monitoring tabs plan MON-6c). A short range (30 minutes and under) reads wwd's 5 s records once
// when the page mounts and then takes each new facts record as it arrives; a long range reads the daemon's 1 minute series once and tops it up every minute from its last sample.
// The memory is shared per host: every page on the host is told when it changes, and a read is called off only when the last page holding its track leaves. The fleet is not read.
import { useEffect, useRef, useState } from 'react'
import { usePoolStore } from './store'
import { useNexHostStore } from '../../stores/useNexHostStore'
import {
  appendLive, dropLongTrack, holdTrack, loadLong, loadShort, readHostLoad, readSamples, readsFiveSecond, seriesAnnounced, subscribeHost,
  type HostLoad, type HostRange, type HostSample,
} from './host-history'
import type { PoolFacts } from './types'

const TOP_UP_MS = 60_000

export function useHostSeries(hostId: string, alias: string | null, range: HostRange, liveFacts: PoolFacts | null): { samples: HostSample[]; load: HostLoad; rev: number } {
  const source = usePoolStore((s) => s.sourceHostId)
  // The memory is the source; this count only makes a change to it (by this page or another on the same host) draw again.
  const [rev, setRev] = useState(0)
  useEffect(() => subscribeHost(hostId, () => setRev((n) => n + 1)), [hostId])
  const five = readsFiveSecond(range)
  // The host's constants for the long track (its total memory and cores). The total arriving after the series was read reads the range again, to fill what the first read left empty.
  const constants = useRef({ memTotalMb: null as number | null, ncpu: null as number | null })
  constants.current = { memTotalMb: liveFacts?.mem.total_mb ?? null, ncpu: liveFacts?.cpu.ncpu ?? null }
  const hasTotal = liveFacts !== null && liveFacts.mem.total_mb > 0

  useEffect(() => {
    if (!five || !source || !alias) return
    const release = holdTrack(hostId, 'short')
    void loadShort(source, alias, hostId, Date.now())
    return release
  }, [five, source, alias, hostId])

  const at = liveFacts?.at ?? null
  useEffect(() => {
    if (!liveFacts) return
    appendLive(hostId, liveFacts)
    // Only a new record time is news; the object itself changes with every pool answer.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hostId, at])

  // Subscribed: the daemon's capabilities may arrive after the page opened, and that must start the read.
  const announced = useNexHostStore((s) => seriesAnnounced(source, s.byHost))
  const longOn = !five && !!source && !!alias && announced
  useEffect(() => {
    // A daemon that does not announce the series (any more) leaves nothing to show for a long range.
    if (!five && !announced) dropLongTrack(hostId)
  }, [five, announced, hostId])
  useEffect(() => {
    if (!longOn || !source || !alias) return
    let cancelled = false
    const release = holdTrack(hostId, 'long')
    // One read at a time: a new one would call the one still out off (it shares the track's generation), so a slow daemon would never land an answer.
    let busy = false
    const run = (refresh: boolean) => {
      if (busy) return
      busy = true
      void loadLong({ source, alias, hostId, range, ...constants.current, now: Date.now(), refresh }).then(() => { busy = false })
    }
    run(false)
    const timer = setInterval(() => { if (!cancelled) run(true) }, TOP_UP_MS)
    return () => { cancelled = true; clearInterval(timer); release() }
  }, [longOn, source, alias, hostId, range, hasTotal])

  return { samples: readSamples(hostId, range, Date.now()), load: readHostLoad(hostId, five ? 'short' : 'long'), rev }
}
