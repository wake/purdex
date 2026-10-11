// spa/src/hooks/useLaunchPlan.ts — triggers the shared launch-plan read (useLaunchPlanStore) for one session: when `enabled` turns on
// (mount), on `refresh()`, and whenever `changeKey` changes (the sessions list moved for this session). The state itself
// lives in the store, so the status bar and the pane read one answer and it survives a tab remount.
import { useCallback, useEffect, useState } from 'react'
import { selectLaunchPlanEntry, useLaunchPlanStore, type LaunchPlanEntry } from '../stores/useLaunchPlanStore'

export function useLaunchPlan(enabled: boolean, hostId: string, code: string, changeKey: string): { entry: LaunchPlanEntry | undefined; refresh: () => void } {
  const [tick, setTick] = useState(0)
  const refresh = useCallback(() => setTick((n) => n + 1), [])

  useEffect(() => {
    if (!enabled || !hostId || !code) return
    void useLaunchPlanStore.getState().load(hostId, code)
  }, [enabled, hostId, code, changeKey, tick])

  const entry = useLaunchPlanStore(selectLaunchPlanEntry(hostId, code))
  return { entry: enabled ? entry : undefined, refresh }
}
