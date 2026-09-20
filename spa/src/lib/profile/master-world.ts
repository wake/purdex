// spa/src/lib/profile/master-world.ts — which part of this device is THE MASTER
// PROFILE'S, for the two impure halves (collector.ts, apply-to-stores.ts).
//
// The pure core never assumes that what the stores hold is what the profile
// owns: `buildSettingsSection` / `applySettings` take the master's workspace ids
// as a parameter. This file is the ONE place that answers where they come from,
// so that the day the screen can show a world that is not the master's (a local
// profile that must never reach the SOT, the master's world parked elsewhere)
// is a change to this function and to nothing else.
import { useWorkspaceStore } from '../../features/workspace/store'
import { isSyncableWorkspaceId } from './sections'

/**
 * The ids of the workspaces the master profile's `workspaces` section holds on
 * this device. Today: the live workspace store — minus the ids that cannot form
 * a `tabs.<id>` key, which `buildWorkspacesSection` leaves out. Such a workspace
 * is device-local as a whole, its scoped settings included: sent, they would be
 * an orphan to every other client, whose corrective push would then come back
 * and delete them HERE.
 */
export function masterWorkspaceIds(): ReadonlySet<string> {
  return new Set(useWorkspaceStore.getState().workspaces.map((ws) => ws.id).filter(isSyncableWorkspaceId))
}
