// spa/src/components/team/usePersonalPanel.ts — the hooks behind the personal panel (task groups spec §7, TG-5b): the active tab's agent session, the group it is in, the readings and the name the cell draws.
// A session that is no team seat reads its context from its own statusLine snapshot (`ccStatus`, as the status bar does) and its name from the address the root cache kept (spec §4.5).
import { useEffect, useMemo } from 'react'
import { useTabStore } from '../../stores/useTabStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { collectLeaves } from '../../lib/pane-tree'
import { useAgentStore } from '../../stores/useAgentStore'
import { useTeamDisplay } from './team-display'
import { useTeamViews } from '../../hooks/useTeamViews'
import { currentPlacementCtx } from '../../lib/task-groups/current'
import { addressOf, rootOf, useRootCacheStore } from '../../lib/task-groups/root-cache'
import { setRootsInterest } from '../../lib/task-groups/session-roots-lifecycle'
import { personalGroupOf, personalTargetOf, type PersonalGroup, type PersonalTarget } from '../../lib/task-groups/personal'
import { parseCcExtras, parseCcUsage } from '../../lib/usage-display'
import { familyOf, type ModelFamily } from './model-family'
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

export function usePersonalGroup(target: PersonalTarget | null): PersonalGroupView {
  const tab = useTabStore((s) => (target ? s.tabs[target.tabId] : undefined))
  const tabs = useTabStore((s) => s.tabs)
  const order = useManualGroupsStore((s) => s.order)
  const groups = useManualGroupsStore((s) => s.groups)
  const roots = useRootCacheStore((s) => s.roots)
  const views = useTeamViews()
  return useMemo(() => {
    if (!target || !tab) return { group: { kind: 'ungrouped' }, rootKnown: false }
    const ctx = { ...currentPlacementCtx(), manual: { order, groups } }
    return { group: personalGroupOf(tab, ctx, views, tabs), rootKnown: rootOf(target.hostId, target.agentSessionId) !== null }
    // `roots` is read through `rootOf` (the cache's plain reader); it is a dependency so the capsule follows a read.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target, tab, tabs, order, groups, roots, views])
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
  /** What the session is called where no persona is: the tmux session's name (the sidebar's row title), else the name the pane cached. */
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
  const title = useSessionStore((s) => {
    if (!target) return ''
    return s.sessions[target.hostId]?.find((r) => r.code === target.sessionCode)?.name ?? ''
  })
  const cachedName = useTabStore((s) => {
    const layout = target ? s.tabs[target.tabId]?.layout : undefined
    const pane = layout ? collectLeaves(layout).find((p) => p.content.kind === 'tmux-session' && p.content.sessionCode === target?.sessionCode) : undefined
    return pane?.content.kind === 'tmux-session' ? pane.content.cachedName : ''
  })
  return { title: title !== '' ? title : cachedName, address, persona: address === '' ? null : seatOf(address) }
}
