// spa/src/hooks/useAttachStall.ts
// Attach-stall detector for a tmux-session pane (#1474, tmux idle-state spec S1).
//
// True once, for `ms` without a break, the host's attach gate has stayed shut
// (`attachReady !== true`) while its daemon is reachable
// (`daemonState === 'connected'`): the host answers, but its session list never
// arrives, so the terminal would otherwise sit on "connecting..." forever.
// Either condition breaking resets the timer. `status` is deliberately not read:
// it flaps `reconnecting`/`connected` during the daemon's subscribe-retry loop
// while `daemonState` stays `connected` (useMultiHostEventWs), and must not
// restart the count. The run is host-scoped: re-binding the pane to another
// host starts from zero. An empty `hostId` never stalls.
import { useEffect, useState } from 'react'
import { useHostStore } from '../stores/useHostStore'

export const ATTACH_STALL_MS = 10_000

export function useAttachStall(hostId: string, ms = ATTACH_STALL_MS): boolean {
  const waiting = useHostStore((s) => {
    if (!hostId) return false
    const rt = s.runtime[hostId]
    return rt?.attachReady !== true && rt?.daemonState === 'connected'
  })
  // The host the current stall belongs to, not a bare flag: a pane re-bound
  // from one waiting host to another must not inherit the first host's stall,
  // not even for the render before the effect below restarts the timer.
  const [stalledHost, setStalledHost] = useState<string | null>(null)

  useEffect(() => {
    if (!waiting) return
    const id = setTimeout(() => setStalledHost(hostId), ms)
    // Any break, host change or unmount ends the run: the next one starts from zero.
    return () => {
      clearTimeout(id)
      setStalledHost(null)
    }
  }, [waiting, hostId, ms])

  return waiting && stalledHost === hostId
}
