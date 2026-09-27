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
// restart the count. An empty `hostId` never stalls.
import { useEffect, useState } from 'react'
import { useHostStore } from '../stores/useHostStore'

export const ATTACH_STALL_MS = 10_000

export function useAttachStall(hostId: string, ms = ATTACH_STALL_MS): boolean {
  const waiting = useHostStore((s) => {
    if (!hostId) return false
    const rt = s.runtime[hostId]
    return rt?.attachReady !== true && rt?.daemonState === 'connected'
  })
  const [stalled, setStalled] = useState(false)

  useEffect(() => {
    if (!waiting) return
    const id = setTimeout(() => setStalled(true), ms)
    // Any break (or unmount) ends the run: the next one starts from zero.
    return () => {
      clearTimeout(id)
      setStalled(false)
    }
  }, [waiting, ms])

  return waiting && stalled
}
