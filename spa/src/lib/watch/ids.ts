// The watcher area's fixed ids, in a module with no imports: the tab store and the workspace store both need them, and
// neither may import the other's feature to get them.

/**
 * The id of the watcher area's workspace (守門員專區). A CONSTANT, like `unsorted`: two devices (or two windows) that
 * create it each make the SAME workspace, so a sync apply or a rehydrate converges instead of duplicating. It cannot
 * collide with a generated id (those are exactly 6 chars of base36; this is 5) and it is a legal `tabs.<id>` key.
 */
export const WATCH_WORKSPACE_ID = 'watch'

/** Fixed tab ids: `watch-guardian`, `watch-events`, `watch-jobs`, `watch-host-<host id>`. Generated ids never contain `-`. */
export const WATCH_GUARDIAN_TAB_ID = 'watch-guardian'
export const WATCH_EVENTS_TAB_ID = 'watch-events'
export const WATCH_JOBS_TAB_ID = 'watch-jobs'
const WATCH_HOST_TAB_PREFIX = 'watch-host-'

/** `hostId` is the wwd `host_id` of a pool host (older tabs: a fleet host id). */
export const watchHostTabId = (hostId: string): string => `${WATCH_HOST_TAB_PREFIX}${hostId}`

/** How other stores recognise an area tab without reading the workspace store. */
export const isWatchTabId = (id: string): boolean => id.startsWith('watch-')

/** The host id of a `watch-host-<id>` tab id, else null. */
export function hostIdOfTabId(id: string): string | null {
  return id.startsWith(WATCH_HOST_TAB_PREFIX) ? id.slice(WATCH_HOST_TAB_PREFIX.length) : null
}

export const isWatchWorkspaceId = (id: string | null | undefined): boolean => id === WATCH_WORKSPACE_ID
