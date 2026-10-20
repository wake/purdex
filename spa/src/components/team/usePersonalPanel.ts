// spa/src/components/team/usePersonalPanel.ts — the hooks behind the personal panel (task groups spec §7, TG-5b): the active tab's agent session, the group it is in, the readings and the name the cell draws.
// A session that is no team seat reads its context from its own statusLine snapshot (`ccStatus`, as the status bar does) and its name from the address the root cache kept (spec §4.5).
import { useEffect, useMemo } from 'react'
import { useTabStore } from '../../stores/useTabStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { collectLeaves } from '../../lib/pane-tree'
import { useAgentStore } from '../../stores/useAgentStore'
import { useTeamDisplay } from './team-display'
import { currentTeamState } from '../../lib/team/team-state'
import { currentPlacementCtx } from '../../lib/task-groups/current'
import { addressOf, rootOf, useRootCacheStore } from '../../lib/task-groups/root-cache'
import { setRootsInterest } from '../../lib/task-groups/session-roots-lifecycle'
import { personalGroupOf, personalTargetOf, type PersonalGroup, type PersonalTarget } from '../../lib/task-groups/personal'
import { parseCcExtras, parseCcUsage } from '../../lib/usage-display'
import { familyOf, type ModelFamily } from './model-family'
import { firstSentence } from './seat-workbook'
import { useLoadedSeatWorkbook } from './own-workbook'
import { leadLastLine, useLeadJournalRead } from './member-lines'
import { useJournalStore } from '../../stores/useJournalStore'
import { useIsJournalHost } from '../../lib/journal/use-journal-host'
import { seatOf, usePersonaRevision, type Seat } from '../../lib/names/seat'

/** The active tab's agent session when the tab is no team seat (the panel's rule 2), else null. */
export function usePersonalTarget(): PersonalTarget | null {
  const display = useTeamDisplay()
  const activeTabId = useTabStore((s) => s.activeTabId)
  const layout = useTabStore((s) => (s.activeTabId ? s.tabs[s.activeTabId]?.layout : undefined))
  const isTeam = display !== null && display.panelTeam(activeTabId) !== null
  return useMemo(() => (isTeam || activeTabId === null || layout === undefined ? null : personalTargetOf({ id: activeTabId, layout })), [isTeam, activeTabId, layout])
}

/** While a personal panel is on the active tab its session's root (and address) is read, whatever view the sidebar is in (spec §4.5). */
export function usePersonalRootsInterest(target: PersonalTarget | null): void {
  const hostId = target?.hostId
  const sessionId = target?.agentSessionId
  useEffect(() => {
    if (hostId === undefined || sessionId === undefined) { setRootsInterest(false, [], 'personal'); return }
    setRootsInterest(true, [{ hostId, agentSessionId: sessionId }], 'personal')
    return () => setRootsInterest(false, [], 'personal')
  }, [hostId, sessionId])
}

export interface PersonalGroupView {
  group: PersonalGroup
  /** The session's root has been read (R23): until then moving, taking out and starting a group stay off and the capsule says 「未分組」. */
  rootKnown: boolean
}

const NO_TABS: Record<string, never> = {}

/** `withCount`: the group's session count is wanted (the full form's header), which needs every tab; the strip and the one line pass false and are not woken by other tabs changing (`count` is then 0). */
export function usePersonalGroup(target: PersonalTarget | null, withCount = false): PersonalGroupView {
  const tab = useTabStore((s) => (target ? s.tabs[target.tabId] : undefined))
  const tabs = useTabStore((s) => (withCount ? s.tabs : NO_TABS))
  const order = useManualGroupsStore((s) => s.order)
  const groups = useManualGroupsStore((s) => s.groups)
  const roots = useRootCacheStore((s) => s.roots)
  return useMemo(() => {
    if (!target || !tab) return { group: { kind: 'ungrouped' }, rootKnown: false }
    const state = currentTeamState() // read now, not subscribed: the team views follow every tab, and this hook must not (a team group is the rare case, a plain tab filed under a team)
    const ctx = { ...currentPlacementCtx(), manual: { order, groups } }
    return { group: personalGroupOf(tab, ctx, state.views, tabs), rootKnown: rootOf(target.hostId, target.agentSessionId) !== null }
    // `roots` is read through `rootOf` (the cache's plain reader); it is a dependency so the capsule follows a read.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target, tab, tabs, order, groups, roots])
}

export interface PersonalReading {
  /** Context used, 0–100; undefined when the snapshot has none. */
  ctx: number | undefined
  model: ModelFamily | undefined
  modelRaw: string | undefined
}

export function usePersonalReading(target: PersonalTarget | null): PersonalReading {
  const entry = useAgentStore((s) => (target ? s.ccStatus[`${target.hostId}:${target.sessionCode}`] : undefined))
  return useMemo(() => {
    const raw = entry?.raw
    const used = parseCcUsage(raw)?.context
    const modelRaw = parseCcExtras(raw).model ?? undefined
    const model = familyOf(modelRaw)
    return { ctx: used === null || used === undefined ? undefined : Math.round(used), model: model ?? undefined, modelRaw: model ? undefined : modelRaw }
  }, [entry])
}

export interface PersonalWho {
  /** What the session is called besides its persona's name: its pdx title, else the tmux session's name, else the name the pane cached. */
  title: string
  /** The address the daemon listed for the session, or '' when no read ever did. */
  address: string
  persona: Seat | null
}

/** Who the session is: the persona by the address the root cache kept (so an ended session keeps its name). */
export function usePersonalWho(target: PersonalTarget | null): PersonalWho {
  usePersonaRevision() // `seatOf` reads the persona store without subscribing: redraw when it loads or changes
  const cached = useRootCacheStore((s) => (target ? s.addresses[target.hostId]?.[target.agentSessionId] : undefined))
  const address = target ? (cached ?? addressOf(target.hostId, target.agentSessionId) ?? '') : ''
  // The pdx title the quota row carried (the sidebar team row's), else the tmux session's name, else the name the pane cached.
  const pdxTitle = useRootCacheStore((s) => (target ? s.titles[target.hostId]?.[target.agentSessionId] : undefined)) ?? ''
  const sessionName = useSessionStore((s) => {
    if (!target) return ''
    return s.sessions[target.hostId]?.find((r) => r.code === target.sessionCode)?.name ?? ''
  })
  const cachedName = useTabStore((s) => {
    const layout = target ? s.tabs[target.tabId]?.layout : undefined
    const pane = layout ? collectLeaves(layout).find((p) => p.content.kind === 'tmux-session' && p.content.sessionCode === target?.sessionCode) : undefined
    return pane?.content.kind === 'tmux-session' ? pane.content.cachedName : ''
  })
  return { title: pdxTitle !== '' ? pdxTitle : sessionName !== '' ? sessionName : cachedName, address, persona: address === '' ? null : seatOf(address) }
}

/** The session's last status line (R13): on a journal.v1 host the first line of its own newest journal entry, on an older host the first sentence of 「目前狀況」; '' when there is none. */
export function usePersonalStatusLine(target: PersonalTarget): string {
  const journalHost = useIsJournalHost(target.hostId)
  useLeadJournalRead(target.hostId, target.agentSessionId, journalHost)
  const journalLine = useJournalStore((s) => (journalHost ? leadLastLine(s, target.hostId, target.agentSessionId) : ''))
  // Asks the host for THIS session's workbook (once per connection), whatever its agent type or liveness.
  const status = useLoadedSeatWorkbook({ hostId: target.hostId, sessionId: target.agentSessionId }).conv?.status.trim() ?? ''
  return journalHost ? journalLine : firstSentence(status)
}

/** The name the person is shown by: the persona's name with the title after it; with no persona the title alone (R15). */
export function usePersonalNames(target: PersonalTarget): { name: string; title: string; persona: PersonalWho['persona'] } {
  const who = usePersonalWho(target)
  const name = who.persona?.name ?? who.title
  const title = who.persona !== null && who.title !== '' && who.title !== who.persona.name ? who.title : ''
  return { name, title, persona: who.persona }
}

