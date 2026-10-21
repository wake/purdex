// spa/src/lib/notify/group-labels-writer.ts — writes each host its group names (notify title spec §3.2; plan N-3 #2/#3). One module-level subscription for the app's lifetime, started from main.tsx.
//   - Only a host that is connected and lists `notify.group_labels.v1` is written; an older daemon has no such route and is never asked.
//   - `PUT /api/notify/group-labels` carries this device's WHOLE set for the host (the daemon replaces the old one), so a rename, a removal or an emptied set clears what the daemon held.
//   - Written when the projection changed (debounced) and when the host connects; an unchanged projection is not written again. A failed write is logged and not retried until the projection changes or the host reconnects.
//   - More than NOTIFY_GROUP_LABELS_MAX labels are not sent (the daemon would refuse the whole set); the error is logged and nothing is cut.
//   - It is the `notify` asker of the roots lifecycle: the roots of the tabs that carry `tab.group` on such hosts are read, next to the sidebar's and the personal panel's interests.
import { getClientId } from '../client-identity'
import { hostFetch } from '../host-api'
import { rootOf, useRootCacheStore } from '../task-groups/root-cache'
import { setRootsInterest, type WantedSession } from '../task-groups/session-roots-lifecycle'
import { currentTeamState } from '../team/team-state'
import { useHostStore } from '../../stores/useHostStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { projectGroupLabels } from './group-labels'
import { NOTIFY_GROUP_LABELS_MAX, NOTIFY_GROUP_LABELS_PATH } from './types'

export const GROUP_LABELS_DEBOUNCE_MS = 500
const WRITE_TIMEOUT_MS = 10_000

export function startGroupLabelsWriter(): () => void {
  const lastSent = new Map<string, string>() // host id → the serialised labels last sent (or tried) on this connection
  let timer: ReturnType<typeof setTimeout> | null = null

  // One write at a time per host: the daemon keeps whichever whole set lands last, so a newer set must never overtake an older one still out. A change made meanwhile is written once the first answers.
  const inFlight = new Set<string>()

  const send = (hostId: string, labels: unknown[], key: string) => {
    lastSent.set(hostId, key)
    if (labels.length > NOTIFY_GROUP_LABELS_MAX) {
      console.error(`notify group labels: ${labels.length} labels for host ${hostId} exceed ${NOTIFY_GROUP_LABELS_MAX}; not sent`)
      return
    }
    inFlight.add(hostId)
    const done = () => { inFlight.delete(hostId); schedule() }
    hostFetch(hostId, NOTIFY_GROUP_LABELS_PATH, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ device: getClientId(), labels }),
      signal: AbortSignal.timeout(WRITE_TIMEOUT_MS), // a write that never answers must not hold the host's slot
    }).then(
      (res) => { if (!res.ok) console.error(`notify group labels: host ${hostId} answered ${res.status}`); done() },
      (err) => { console.error('notify group labels: write failed', err); done() },
    )
  }

  const evaluate = () => {
    timer = null
    const { hosts, runtime } = useHostStore.getState()
    const byHost = useUnattendedStore.getState().byHost
    const manual = useManualGroupsStore.getState()
    const tabs = useTabStore.getState().tabs
    const views = currentTeamState().views
    const wanted: WantedSession[] = []
    for (const hostId of Object.keys(hosts)) {
      const ready = runtime[hostId]?.status === 'connected' && byHost[hostId]?.notifyGroupLabelsSupport === 'yes'
      if (!ready) { lastSent.delete(hostId); continue }
      const p = projectGroupLabels({ hostId, manual: { order: manual.order, groups: manual.groups }, tabs, views, rootOf })
      wanted.push(...p.wanted)
      const key = JSON.stringify(p.labels)
      if (lastSent.get(hostId) !== key && !inFlight.has(hostId)) send(hostId, p.labels, key)
    }
    for (const hostId of [...lastSent.keys()]) if (!hosts[hostId]) lastSent.delete(hostId)
    setRootsInterest(wanted.length > 0, wanted, 'notify')
  }
  const schedule = () => {
    if (timer !== null) clearTimeout(timer)
    timer = setTimeout(evaluate, GROUP_LABELS_DEBOUNCE_MS)
  }

  const unsubs = [
    useManualGroupsStore.subscribe(schedule),
    useTabStore.subscribe((s, prev) => { if (s.tabs !== prev.tabs) schedule() }),
    useTeamRosterStore.subscribe((s, prev) => { if (s.byHost !== prev.byHost) schedule() }),
    useRootCacheStore.subscribe((s, prev) => { if (s.roots !== prev.roots) schedule() }),
    useUnattendedStore.subscribe((s, prev) => { if (s.byHost !== prev.byHost) schedule() }),
    useHostStore.subscribe((s, prev) => { {
      // `runtime` changes with every latency sample: only a host's status (or the host list) matters here.
      const changed = s.hosts !== prev.hosts || Object.keys(s.hosts).some((id) => s.runtime[id]?.status !== prev.runtime[id]?.status)
      if (changed) schedule()
    } }),
  ]
  schedule()
  return () => {
    for (const off of unsubs) off()
    if (timer !== null) clearTimeout(timer)
    setRootsInterest(false, [], 'notify')
  }
}
