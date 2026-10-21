// spa/src/lib/team/unattended-support.ts — does each host's daemon support 無人值守模式 (unattended spec D-U23-5:
// "a daemon without the capability … counts as unsupported"; plan PU-2a). One module-level subscription for the
// app's lifetime, started from main.tsx.
//
// Triggers, per host and only while connected: the transition to `connected`, or a change of its endpoint or token.
// Each trigger is one `/api/info`: `capabilities` an array holding `relay.unattended.v1` → 'yes', any other answer →
// 'no'. A failed request leaves every flag unknown (never the last connection's value) until the next trigger; nothing here
// polls. Only the newest request per host may apply (host-daemon-id.ts's generation guard), and a removed host's
// answer never does.
//
// A re-point (endpoint or token) or a removal forgets the host's entry: what the old daemon said — its support and
// its switch — is not the new one's. The new connection's snapshot and this probe fill it again.
import { fetchHostInfo } from '../host-api'
import { hostEndpoint, useHostStore, type HostConfig } from '../../stores/useHostStore'
import { PROBE_ONLY_FLAGS, useUnattendedStore } from '../../stores/useUnattendedStore'
import { quotaHostIds, useRelayQuotaStore } from './relay-quota'
import { useMaxMembersStore } from './max-members'
import { useRootsStore } from './roots'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { useJournalMembersStore } from '../../stores/useJournalMembersStore'
import { JOURNAL_CAPABILITY, JOURNAL_CONFIRM_CAPABILITY, JOURNAL_MEMBERS_CAPABILITY, JOURNAL_SESSIONS_CAPABILITY } from '../journal/types'
import { WORKBOOK_V1_CAPABILITY, WORKBOOK_V2_CAPABILITY } from '../workbook/types'
import { NOTIFY_GROUP_LABELS_CAPABILITY } from '../notify/types'
import { STAGES_CAPABILITY } from '../conversations/types'
import { TEAM_MAX_MEMBERS_V2_CAPABILITY, PERSON_CAPABILITY, RELAY_QUOTA_CAPABILITY, TEAM_EDIT_CAPABILITY, TEAM_MAX_MEMBERS_CAPABILITY, TEAM_PAUSE_CAPABILITY, UNANSWERED_CAPABILITY, UNATTENDED_CAPABILITY } from './types'

const identity = (h: HostConfig): string => `${hostEndpoint(h)}:${h.token ?? ''}`

export function startUnattendedSupport(): () => void {
  let counter = 0
  // Generation of the newest request per host; absent = none may apply.
  const current = new Map<string, number>()

  const probe = (hostId: string) => {
    const generation = ++counter
    current.set(hostId, generation)
    // First, before the flags go unknown: the fence moves the quota epoch, which a panel answers by reading the host at once,
    // and that read opens its GET bracket (so the new rows reach the store) only while the last answer still says quotas.
    useRelayQuotaStore.getState().fence(hostId) // and the quota reads: a read out over the last connection writes nothing
    useUnattendedStore.getState().invalidateSupport(hostId) // what the last connection said is not this one's answer (every flag: #2309)
    useWorkbookStore.getState().fence(hostId) // same for the workbook: a new connection generation, support unknown until this answer
    useJournalStore.getState().fence(hostId) // and the journal: unknown until this answer, but its sticky last answer stays
    useJournalMembersStore.getState().fence(hostId) // and the member lines' gate
    fetchHostInfo(hostId).then(
      (info) => {
        if (current.get(hostId) !== generation) return
        const caps: unknown = info?.capabilities
        const listed = Array.isArray(caps) ? caps : []
        useUnattendedStore.getState().setSupport(hostId, listed.includes(UNATTENDED_CAPABILITY) ? 'yes' : 'no')
        useUnattendedStore.getState().setQuotaSupport(hostId, listed.includes(RELAY_QUOTA_CAPABILITY) ? 'yes' : 'no')
        useUnattendedStore.getState().setMaxMembersSupport(hostId, listed.includes(TEAM_MAX_MEMBERS_CAPABILITY) ? 'yes' : 'no')
        useUnattendedStore.getState().setEditSupport(hostId, listed.includes(TEAM_EDIT_CAPABILITY) ? 'yes' : 'no')
        useUnattendedStore.getState().setPauseSupport(hostId, listed.includes(TEAM_PAUSE_CAPABILITY) ? 'yes' : 'no')
        useUnattendedStore.getState().setUnansweredSupport(hostId, listed.includes(UNANSWERED_CAPABILITY) ? 'yes' : 'no')
        useUnattendedStore.getState().setPersonSupport(hostId, listed.includes(PERSON_CAPABILITY) ? 'yes' : 'no')
        useUnattendedStore.getState().setStagesSupport(hostId, listed.includes(STAGES_CAPABILITY) ? 'yes' : 'no')
        useUnattendedStore.getState().setMaxMembersV2Support(hostId, listed.includes(TEAM_MAX_MEMBERS_V2_CAPABILITY) ? 'yes' : 'no')
        useUnattendedStore.getState().setNotifyGroupLabelsSupport(hostId, listed.includes(NOTIFY_GROUP_LABELS_CAPABILITY) ? 'yes' : 'no')
        // The workbook flags share this guard; each answer is a new connection generation for the workbook store
        // (a seat is loaded once per generation, workbook-loader.ts).
        useWorkbookStore.getState().setSupport(hostId, { v1: listed.includes(WORKBOOK_V1_CAPABILITY), v2: listed.includes(WORKBOOK_V2_CAPABILITY) })
        useJournalStore.getState().setSupport(hostId, listed.includes(JOURNAL_CAPABILITY), listed.includes(JOURNAL_CONFIRM_CAPABILITY), listed.includes(JOURNAL_SESSIONS_CAPABILITY))
        useJournalMembersStore.getState().setSupport(hostId, listed.includes(JOURNAL_MEMBERS_CAPABILITY))
      },
      () => { // not retried until the next trigger; the probe's flags stay unknown rather than keeping an old 'yes'
        // `support` was made unknown when this probe started; a `team.unattended` frame that proved it since is live
        // evidence from this connection and stays, so only the flags nothing but the probe can answer are dropped again.
        if (current.get(hostId) === generation) useUnattendedStore.getState().invalidateSupport(hostId, PROBE_ONLY_FLAGS)
      },
    )
  }

  const initial = useHostStore.getState()
  for (const [hostId, rt] of Object.entries(initial.runtime)) {
    if (rt?.status === 'connected' && initial.hosts[hostId]) probe(hostId)
  }

  return useHostStore.subscribe((next, prev) => {
    const store = useUnattendedStore.getState()
    for (const hostId of [...current.keys()]) {
      if (!next.hosts[hostId]) current.delete(hostId)
    }
    for (const hostId of Object.keys(store.byHost)) {
      if (!next.hosts[hostId]) store.forgetHost(hostId)
    }
    for (const hostId of Object.keys(useJournalStore.getState().sticky)) {
      if (!next.hosts[hostId]) useJournalStore.getState().forgetHost(hostId) // a removed host's journal answer goes with it
    }
    for (const hostId of Object.keys(useJournalMembersStore.getState().support)) {
      if (!next.hosts[hostId]) useJournalMembersStore.getState().forgetHost(hostId)
    }
    for (const hostId of quotaHostIds(useRelayQuotaStore.getState())) {
      if (!next.hosts[hostId]) useRelayQuotaStore.getState().forgetHost(hostId) // a removed host's numbers go with it
    }
    for (const hostId of Object.keys(prev.hosts)) {
      if (!next.hosts[hostId]) { useMaxMembersStore.getState().forgetHost(hostId); useRootsStore.getState().forgetHost(hostId) } // and a cap / roots request still out to it
    }
    for (const [hostId, host] of Object.entries(next.hosts)) {
      const before = prev.hosts[hostId]
      const repointed = before !== undefined && identity(before) !== identity(host)
      if (repointed) {
        current.delete(hostId) // an answer still on its way is the old daemon's
        store.forgetHost(hostId)
        useRelayQuotaStore.getState().forgetHost(hostId) // and so are the quota numbers it confirmed
        useMaxMembersStore.getState().forgetHost(hostId) // and a cap request still out to it
        useRootsStore.getState().forgetHost(hostId)
        useJournalStore.getState().forgetHost(hostId) // the old daemon's journal.v1 answer, sticky included, is not the new one's
        useJournalMembersStore.getState().forgetHost(hostId) // and its member lines
      }
      if (next.runtime[hostId]?.status !== 'connected') {
        // The connection the capabilities were learned on is gone: they are unknown until the next probe answers, and an
        // answer still on its way from that connection must not set them.
        if (prev.runtime[hostId]?.status === 'connected') {
          current.delete(hostId)
          store.invalidateSupport(hostId)
          useWorkbookStore.getState().fence(hostId) // the connection is gone: requests still out must not land
          useJournalStore.getState().fence(hostId) // unknown now; the sticky answer stays so the journal UI does not flash back
          useJournalMembersStore.getState().fence(hostId)
          useRelayQuotaStore.getState().fence(hostId) // a quota read out over this connection must not land either
        }
        continue
      }
      if (repointed || !before || prev.runtime[hostId]?.status !== 'connected') probe(hostId)
    }
  })
}
