// spa/src/hooks/useLaunchPlan.ts — the daemon's launch plan for one session (no-agent launch panel). Read when `enabled` turns on
// (mount), on `refresh()`, and whenever `changeKey` changes (the sessions list moved for this session). The previous plan stays
// shown while a re-read is in flight; a failed read drops it (the caller then keeps today's view — never a stale panel).
import { useCallback, useEffect, useState } from 'react'
import { fetchLaunchPlan, type LaunchPlan } from '../lib/launch-plan'

export function useLaunchPlan(enabled: boolean, hostId: string, code: string, changeKey: string): { plan: LaunchPlan | null; refresh: () => void } {
  const [tick, setTick] = useState(0)
  const refresh = useCallback(() => setTick((n) => n + 1), [])
  const subject = `${hostId}\0${code}`
  const [held, setHeld] = useState<{ subject: string; plan: LaunchPlan | null } | null>(null)

  useEffect(() => {
    if (!enabled) return
    const ac = new AbortController()
    fetchLaunchPlan(hostId, code, ac.signal)
      .then((plan) => plan, () => null)
      .then((plan) => { if (!ac.signal.aborted) setHeld({ subject, plan }) })
    return () => ac.abort()
  }, [enabled, hostId, code, subject, changeKey, tick])

  return { plan: enabled && held?.subject === subject ? held.plan : null, refresh }
}
