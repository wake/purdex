// spa/src/hooks/useLaunchPlan.ts — triggers the shared launch-plan read (useLaunchPlanStore) for one session generation: when
// `enabled` turns on (mount), on `refresh()`, and whenever `changeKey` changes (the sessions list moved for this session). The
// state itself lives in the store, so the status bar and the pane read one answer and it survives a tab remount.
//
// Which entry both readers look at is derived HERE, once (`useLaunchPlanInstance` / `useLaunchPlanEntry`), so they cannot drift.
import { useCallback, useLayoutEffect, useRef, useState } from 'react'
import { selectLaunchPlanEntry, useLaunchPlanStore, type LaunchPlanEntry } from '../stores/useLaunchPlanStore'
import { useSessionStore } from '../stores/useSessionStore'

/** The tmux generation the key carries: the sessions list's current `tmux_instance` for this session, else the pane
 *  content's, else ''. */
export function useLaunchPlanInstance(hostId: string, code: string, contentInstance: string | undefined): string {
  const listed = useSessionStore((s) => (Object.hasOwn(s.sessions, hostId) ? s.sessions[hostId] : undefined)?.find((c) => c.code === code)?.tmux_instance)
  return listed || contentInstance || ''
}

/** The shared entry for a session, as the status bar and the pane both read it. */
export function useLaunchPlanEntry(hostId: string, code: string, contentInstance: string | undefined): LaunchPlanEntry | undefined {
  const instance = useLaunchPlanInstance(hostId, code, contentInstance)
  return useLaunchPlanStore(selectLaunchPlanEntry(hostId, code, instance))
}

export function useLaunchPlan(enabled: boolean, hostId: string, code: string, changeKey: string, tmuxInstance: string): { entry: LaunchPlanEntry | undefined; refresh: () => void } {
  const [tick, setTick] = useState(0)
  const refresh = useCallback(() => setTick((n) => n + 1), [])
  const ranBefore = useRef(false)
  const wasEnabled = useRef(false)
  const everEnabled = useRef(false)

  // A layout effect: a dropped plan must not paint for a frame when the gate flips.
  useLayoutEffect(() => {
    // The gate flipped (an agent ran and ended in front of the user): what was kept is from before it, so drop it. Not on
    // mount, and not on the first enable of a mount (a tab remount must not flash).
    const flipped = enabled && !wasEnabled.current && everEnabled.current
    const forced = ranBefore.current
    ranBefore.current = true
    wasEnabled.current = enabled
    if (!enabled || !hostId || !code) return
    everEnabled.current = true
    void useLaunchPlanStore.getState().load(hostId, code, tmuxInstance, { force: forced, keep: !flipped })
  }, [enabled, hostId, code, changeKey, tmuxInstance, tick])

  const entry = useLaunchPlanStore(selectLaunchPlanEntry(hostId, code, tmuxInstance))
  return { entry: enabled ? entry : undefined, refresh }
}
