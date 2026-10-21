// spa/src/lib/task-groups/session-roots-lifecycle.ts — the roots read, outside React (task groups TG-2r; moved out of hooks/useSessionRoots.ts). The one source is `UnattendedView.quotas` (`GET /api/team/unattended`,
// `team.relay_quota.v1`): the daemon lists every live session of the host with the root of its chain. The read goes through the shared reader (it joins a read already out and seeds `useRelayQuotaStore.reads`), and
// only while a caller says the task-group view is on (`setRootsInterest`):
//   - once for a wanted host that has no read yet (never read, or fenced by a reconnect);
//   - once more, debounced, when a wanted session is in neither the cache nor the last read — once per session per host epoch, so a daemon that never lists it is not asked again and again.
// Whatever a read lists goes into the device-local root cache, so an offline host still answers. The cache forgets a host that is removed, moved, or answers as another daemon, and sessions no tab of any world
// (on screen or parked) refers to. Nothing is guessed: a session whose root is not known is `null`.
import { useRelayQuotaStore } from '../team/relay-quota'
import { readFirstPage } from '../team/unattended-reader'
import { collectLeaves } from '../pane-tree'
import { readMasterWorld } from '../profile/master-world'
import { rootOf, rootsSupported, useRootCacheStore } from './root-cache'
import { daemonMismatchKey, useHostStore } from '../../stores/useHostStore'
import { hostEndpointKey } from '../../stores/useHostConfigStore'
import { useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import type { Tab } from '../../types/tab'

export const ROOT_REREAD_DEBOUNCE_MS = 800
/** How long the world stores must stay quiet before the cache is pruned against them. */
export const ROOT_PRUNE_SETTLE_MS = 1000

export interface WantedSession { hostId: string; agentSessionId: string }

/** Who asks: the sidebar's task-group view, and the personal panel of the active tab (task groups spec §4.5), and the push-title writer (`notify`: tabs that carry `tab.group`, notify title spec §3.2). Each source owns its own entry; the reads serve the union. */
export type RootsSource = 'sidebar' | 'personal' | 'notify'
const interests = new Map<RootsSource, readonly WantedSession[]>()
let evaluate: (() => void) | null = null

/** A source says what it needs: while `on`, the wanted sessions' roots are read. Takes effect at once when the lifecycle runs, else when it starts. */
export function setRootsInterest(on: boolean, sessions: readonly WantedSession[], source: RootsSource = 'sidebar'): void {
  if (on) interests.set(source, sessions)
  else interests.delete(source)
  evaluate?.()
}

function* agentSessionsOf(tabs: Record<string, Tab>): Generator<[string, string]> {
  for (const tab of Object.values(tabs)) {
    for (const leaf of collectLeaves(tab.layout)) {
      const c = leaf.content
      if (c.kind === 'tmux-session' && c.rebuild?.agent?.sessionId) yield [c.hostId, c.rebuild.agent.sessionId]
    }
  }
}

/** Every (host, agent session) a tab of any world refers to: the one on screen and every parked one. */
function referencedSessions(): Set<string> {
  const refs = new Set<string>()
  const add = (tabs: Record<string, Tab>) => { for (const [h, id] of agentSessionsOf(tabs)) refs.add(JSON.stringify([h, id])) }
  add(useTabStore.getState().tabs)
  const { parkedMaster, slaves } = useLocalProfilesStore.getState()
  if (parkedMaster !== null) add(parkedMaster.tabs)
  for (const slave of Object.values(slaves)) if (slave.world !== null) add(slave.world.tabs)
  return refs
}

function pruneUnreferenced(): void {
  const refs = referencedSessions()
  useRootCacheStore.getState().prune((h, id) => refs.has(JSON.stringify([h, id])))
}

/** Starts the lifecycle (cache feeding, host forgetting, cleanup, and the reads `setRootsInterest` asks for). Returns the stop. */
export function startSessionRootsLifecycle(): () => void {
  const asked = new Map<string, Set<string>>() // per host: the sessions already asked about in this epoch
  let timer: ReturnType<typeof setTimeout> | null = null
  let pruneTimer: ReturnType<typeof setTimeout> | null = null

  const schedulePrune = () => {
    if (pruneTimer !== null) clearTimeout(pruneTimer)
    pruneTimer = setTimeout(() => {
      pruneTimer = null
      // Another window's switch arrives store by store, in any order (profile/switch-active.ts): until the three stores agree on one world, the tab store may not hold what is about to be shown. Nothing is pruned then;
      // the next change of any of the three stores asks again (no timer of its own: a world that never settles must not keep waking this).
      if (!readMasterWorld().settled) return
      pruneUnreferenced()
    },ROOT_PRUNE_SETTLE_MS)
  }

  const feed = () => {
    for (const [hostId, read] of Object.entries(useRelayQuotaStore.getState().reads)) {
      if (read.ok && read.quotas !== undefined && !read.quotasFailed) useRootCacheStore.getState().ingest(hostId, read.quotas)
    }
  }

  const run = () => {
    if (timer !== null) { clearTimeout(timer); timer = null }
    const reads = useRelayQuotaStore.getState().reads
    for (const hostId of [...asked.keys()]) if (reads[hostId] === undefined) asked.delete(hostId) // a new epoch of the host: what was asked in the old one is asked again if the new read misses it
    const wanted = [...interests.values()].flat()
    if (interests.size === 0) return
    const unread = new Set<string>()
    for (const w of wanted) if (rootsSupported(w.hostId) && reads[w.hostId] === undefined) unread.add(w.hostId)
    for (const hostId of [...unread].sort()) readFirstPage(hostId).catch(() => {}) // a failure lands in the store as the host's read
    const fresh = wanted.filter((w) => {
      const read = reads[w.hostId]
      return read !== undefined && read.ok && read.quotas !== undefined && rootsSupported(w.hostId) && rootOf(w.hostId, w.agentSessionId) === null && !asked.get(w.hostId)?.has(w.agentSessionId)
    })
    if (fresh.length === 0) return
    timer = setTimeout(() => {
      timer = null
      const hosts = new Set<string>()
      for (const w of fresh) {
        hosts.add(w.hostId)
        const set = asked.get(w.hostId) ?? new Set<string>()
        set.add(w.agentSessionId)
        asked.set(w.hostId, set)
      }
      for (const hostId of hosts) readFirstPage(hostId).catch(() => {})
    }, ROOT_REREAD_DEBOUNCE_MS)
  }

  const unsubs = [
    useRelayQuotaStore.subscribe((s, prev) => { if (s.reads !== prev.reads) { feed(); run() } }),
    useHostStore.subscribe((next, prev) => {
      for (const hostId of Object.keys(prev.hosts)) {
        if (!next.hosts[hostId] || hostEndpointKey(prev.hosts[hostId]) !== hostEndpointKey(next.hosts[hostId])) useRootCacheStore.getState().dropHost(hostId)
      }
      for (const [hostId, rt] of Object.entries(next.runtime)) {
        const observed = rt?.daemonIdMismatch?.observed
        if (observed !== undefined && observed !== prev.runtime[hostId]?.daemonIdMismatch?.observed) useRootCacheStore.getState().dropHost(hostId)
      }
      if (daemonMismatchKey(next) !== daemonMismatchKey(prev)) run() // `rootsSupported` answers false for a mismatched host: a change re-derives what to read
    }),
    useUnattendedStore.subscribe((s, prev) => { if (s.byHost !== prev.byHost) run() }), // a capability learned after the view opened
    // A world switch updates the profiles store and then the tab store: pruning between the two would see the world about to be shown as neither parked nor on screen. So it waits until the switch settled.
    useLocalProfilesStore.subscribe((s, prev) => { if (s.parkedMaster !== prev.parkedMaster || s.slaves !== prev.slaves) schedulePrune() }),
    useWorkspaceStore.subscribe((s, prev) => { if (s.worldEpoch !== prev.worldEpoch) schedulePrune() }), // the third store of a switch: its arrival is what settles the world
    useTabStore.subscribe((s, prev) => { if (s.tabs !== prev.tabs) schedulePrune() }), // a plain tab close leaves the profiles store alone; any change of either side restarts the wait
  ]
  evaluate = run
  feed()
  schedulePrune()
  run()
  return () => {
    for (const off of unsubs) off()
    if (timer !== null) clearTimeout(timer)
    if (pruneTimer !== null) clearTimeout(pruneTimer)
    if (evaluate === run) evaluate = null
  }
}
