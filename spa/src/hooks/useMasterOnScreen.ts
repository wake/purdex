// spa/src/hooks/useMasterOnScreen.ts — whose world the sidebar is showing, for the sidebar's conflict icons (sidebar
// conflict icons spec §5). A lock is the MASTER's; while a slave is on screen the sidebar's workspaces are the slave's,
// and a demoted master shares ids with it — so a row matched by id would lie. Read through `readMasterWorld()`, the
// one door to the master's world, over the same three stores CurrentBlock subscribes to: plain store subscriptions,
// no timer, and no recovery asked from here.
import { useSyncExternalStore } from 'react'
import { useWorkspaceStore } from '../features/workspace/store'
import { readMasterWorld } from '../lib/profile/master-world'
import { useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import { useTabStore } from '../stores/useTabStore'

/** `master` = the master world is on screen · `slave` = a slave is, the master parked · `unsettled` = nobody can say
 *  right now (a switch half-way, a window catching up): neither claim is made. */
export type MasterScreen = 'master' | 'slave' | 'unsettled'

function subscribeWorld(fn: () => void): () => void {
  const stops = [useTabStore.subscribe(fn), useWorkspaceStore.subscribe(fn), useLocalProfilesStore.subscribe(fn)]
  return () => {
    for (const stop of stops) stop()
  }
}

/** A string, so that `useSyncExternalStore` sees a change only when there is one. */
function screenNow(): MasterScreen {
  const read = readMasterWorld()
  if (!read.settled) return 'unsettled'
  return read.onScreen ? 'master' : 'slave'
}

/** The MASTER world's workspaces — parked while a slave is on screen — or null while unsettled. The array is the
 *  store's own, so its identity moves only when the list did. */
function masterWorkspacesNow(): readonly { id: string; name: string }[] | null {
  const read = readMasterWorld()
  return read.settled ? read.world.workspaces : null
}

export function useMasterScreen(): MasterScreen {
  return useSyncExternalStore(subscribeWorld, screenNow, screenNow)
}

/** The master world is settled AND on screen: only then does a workspace row stand for a master workspace. */
export function useMasterOnScreen(): boolean {
  return useMasterScreen() === 'master'
}

export function useMasterWorkspaces(): readonly { id: string; name: string }[] | null {
  return useSyncExternalStore(subscribeWorld, masterWorkspacesNow, masterWorkspacesNow)
}
