// spa/src/proto/team/ProtoApp.tsx — the team-display prototype: real sidebar, real TabBar, fake content.
import { useCallback, useMemo, useState } from 'react'
import { ActivityBar } from '../../components/ActivityBar'
import { TabBar } from '../../components/TabBar'
import { TitleBar } from '../../components/TitleBar'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { getVisibleTabIds } from '../../features/workspace'
import type { Tab } from '../../types/tab'
import { TeamDisplayContext, teamColor, type TeamDisplay, type TeamSeatView, type TeamTabMark, type TeamGhostLead } from '../../components/team/team-display'
import { TeamPanel, type TeamPanelSeat } from '../../components/team/TeamPanel'
import {
  useProtoTeam, tabOfSeat, seatOfTab, liveMembers, selectTab, openSeat, closeTab, toggleCollapse,
  reorderMembers, setPanelMode, say, teamLabel, type ProtoSeat, type ProtoTeam,
} from './store'
import { FakeTerminal } from './FakeTerminal'
import { ProtoControls } from './ProtoControls'
import { SessionPicker } from './SessionPicker'

const NOOP = () => {}

function seatView(s: ProtoSeat, role: 'lead' | 'member'): TeamPanelSeat {
  return { sessionId: s.sessionId, title: s.title, hostId: s.hostId, sessionCode: s.code, role, tabId: tabOfSeat(s), model: s.model, effort: s.effort, ctx: s.ctx }
}

/** One workspace's top-bar order: each team's block (lead, then members in team order) sits at its lead. */
function arrange(wsTabIds: string[], teams: Record<string, ProtoTeam>): { id: string; team: ProtoTeam | null; role: 'lead' | 'member' | null }[] {
  const out: { id: string; team: ProtoTeam | null; role: 'lead' | 'member' | null }[] = []
  for (const id of wsTabIds) {
    const s = seatOfTab(id)
    const team = s?.teamKey ? teams[s.teamKey] : null
    if (team && s!.role === 'member') {
      const leadTab = tabOfSeat(useProtoTeam.getState().seats[team.leadId])
      if (leadTab && wsTabIds.includes(leadTab)) continue
    }
    if (team && s!.role === 'lead') {
      out.push({ id, team, role: 'lead' })
      for (const m of liveMembers(team)) {
        const mt = tabOfSeat(m)
        if (mt && wsTabIds.includes(mt)) out.push({ id: mt, team, role: 'member' })
      }
      continue
    }
    out.push({ id, team: null, role: null })
  }
  return out
}

export function ProtoApp() {
  const tabs = useTabStore((s) => s.tabs)
  const tabOrder = useTabStore((s) => s.tabOrder)
  const activeTabId = useTabStore((s) => s.activeTabId)
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)
  const tabPosition = useLayoutStore((s) => s.tabPosition)
  const proto = useProtoTeam()
  const [picker, setPicker] = useState(false)

  const activeWs = workspaces.find((w) => w.id === activeWorkspaceId) ?? null
  const visibleIds = getVisibleTabIds({ tabs, tabOrder, activeTabId, workspaces, activeWorkspaceId })

  // The top bar's order for the active workspace, with collapsed members left out.
  const arranged = useMemo(() => arrange(visibleIds, proto.teams), [visibleIds, proto.teams, proto.seats, tabs]) // eslint-disable-line react-hooks/exhaustive-deps
  const shown = arranged.filter((x) => !(x.role === 'member' && x.team?.collapsed))
  const displayTabs: Tab[] = shown.map((x) => tabs[x.id]).filter(Boolean)

  const display: TeamDisplay = useMemo(() => {
    const marks = new Map<string, TeamTabMark>()
    for (const team of Object.values(proto.teams)) {
      const block = shown.filter((x) => x.team === team)
      const hidden = arranged.filter((x) => x.team === team && x.role === 'member').length - block.filter((x) => x.role === 'member').length
      const { label, unnamed } = teamLabel(team)
      block.forEach((x, i) => marks.set(x.id, {
        teamKey: team.key, color: teamColor(team.color), label, unnamed, role: x.role!, style: proto.groupStyle,
        first: i === 0, last: i === block.length - 1, collapsed: team.collapsed, hiddenCount: hidden,
      }))
    }
    const hidden = new Set<string>()
    const beads = new Map<string, { teamKey: string; color: string; label: string; unnamed: boolean; collapsed: boolean; members: TeamSeatView[] }>()
    for (const team of Object.values(proto.teams)) {
      const leadTab = tabOfSeat(proto.seats[team.leadId])
      if (!leadTab) continue
      const leadWs = workspaces.find((w) => w.tabs.includes(leadTab))
      const members = liveMembers(team).map((m) => seatView(m, 'member'))
      for (const m of members) if (m.tabId && leadWs?.tabs.includes(m.tabId)) hidden.add(m.tabId)
      beads.set(leadTab, { teamKey: team.key, color: teamColor(team.color), ...teamLabel(team), collapsed: team.collapsed, members })
    }
    return {
      activeTabId,
      beadHost: proto.beadHost,
      groupStyle: proto.groupStyle,
      sidebarStyle: proto.sidebarStyle,
      collapseStyle: proto.collapseStyle,
      hookStyle: proto.hookStyle,
      hookTop: proto.hookTop,
      cornerSize: proto.cornerSize,
      badgeIcon: proto.badgeIcon,
      edgeWidth: proto.edgeWidth,
      bookmarkCut: proto.bookmarkCut,
      bookmarkPos: proto.bookmarkPos,
      shadowStrength: proto.shadowStrength,
      shadowScope: proto.shadowScope,
      openMark: proto.openMark,
      tabMark: (id) => marks.get(id) ?? null,
      sidebarHidden: (id) => hidden.has(id),
      sidebarBeads: (id) => beads.get(id) ?? null,
      ghostLeads: (wsId): TeamGhostLead[] => Object.values(proto.teams)
        .filter((t) => t.ghostWs !== null && t.ghostWs === wsId && !tabOfSeat(proto.seats[t.leadId]))
        .map((t) => ({ teamKey: t.key, color: teamColor(t.color), ...teamLabel(t), lead: seatView(proto.seats[t.leadId], 'lead'), members: liveMembers(t).map((m) => seatView(m, 'member')) })),
      onToggleCollapse: toggleCollapse,
      onOpenSeat: (teamKey, sid) => openSeat(teamKey, sid),
      onReorderMembers: reorderMembers,
    }
  }, [proto, arranged, shown, workspaces, activeTabId, tabs]) // eslint-disable-line react-hooks/exhaustive-deps

  // TabBar reorder: a group must stay contiguous with its lead first; anything else bounces (rules 4, 5).
  const handleReorderTabs = useCallback((newOrder: string[]) => {
    if (!activeWs) return
    for (const team of Object.values(proto.teams)) {
      const idx = newOrder.map((id, i) => (display.tabMark(id)?.teamKey === team.key ? i : -1)).filter((i) => i >= 0)
      if (idx.length === 0) continue
      const contiguous = idx[idx.length - 1] - idx[0] === idx.length - 1
      const leadFirst = display.tabMark(newOrder[idx[0]])?.role === 'lead'
      if (!contiguous || !leadFirst) {
        say(!leadFirst ? '彈回原位：lead 永遠在群組最前面（規則 5）。' : '彈回原位：群組裡的分頁不能拖出去，外面的分頁也不能拖進來（規則 4）。')
        useTabStore.setState({}) // nudge a re-render so dnd-kit drops the transform
        return
      }
      const memberIds = idx.slice(1).map((i) => seatOfTab(newOrder[i])!.sessionId)
      const before = liveMembers(team).filter((m) => memberIds.includes(m.sessionId)).map((m) => m.sessionId)
      if (memberIds.join() !== before.join()) reorderMembers(team.key, memberIds)
    }
    // stored order: the new order, with collapsed members kept right after their lead
    const final: string[] = []
    for (const id of newOrder) {
      final.push(id)
      const mark = display.tabMark(id)
      if (mark?.role === 'lead' && mark.collapsed) {
        for (const x of arranged) if (x.team?.key === mark.teamKey && x.role === 'member') final.push(x.id)
      }
    }
    for (const id of activeWs.tabs) if (!final.includes(id)) final.push(id)
    useWorkspaceStore.getState().reorderWorkspaceTabs(activeWs.id, final)
  }, [activeWs, proto.teams, display, arranged])

  const activeTab = activeTabId ? tabs[activeTabId] : null
  const activeSeat = activeTabId ? seatOfTab(activeTabId) : null
  const activeTeam = activeSeat?.teamKey ? proto.teams[activeSeat.teamKey] : null
  const stepIds = tabPosition === 'left' ? arranged.map((x) => x.id) : shown.map((x) => x.id)

  return (
    <TeamDisplayContext.Provider value={display}>
      <div className="h-screen flex flex-col bg-surface-primary text-text-primary">
        <TitleBar title={activeWs?.name ?? 'Purdex'} />
        <div className="flex-1 flex min-h-0">
          <ActivityBar
            workspaces={workspaces}
            activeWorkspaceId={activeWorkspaceId}
            onSelectWorkspace={(id) => {
              useWorkspaceStore.getState().setActiveWorkspace(id)
              const ws = workspaces.find((w) => w.id === id)
              const next = ws?.activeTabId ?? ws?.tabs[0]
              if (next) selectTab(next)
            }}
            onSelectHome={NOOP}
            onAddWorkspace={NOOP}
            onOpenHosts={NOOP}
            onOpenSettings={NOOP}
            tabsById={tabs}
            activeTabId={activeTabId}
            onSelectTab={selectTab}
            onCloseTab={closeTab}
            onMiddleClickTab={closeTab}
            onContextMenuTab={(e) => e.preventDefault()}
            onReorderWorkspaceTabs={(wsId, ids) => useWorkspaceStore.getState().reorderWorkspaceTabs(wsId, ids)}
          />
          <div className="flex-1 flex flex-col min-w-0">
            {tabPosition !== 'left' && (
              <div className="relative">
                <TabBar
                  tabs={displayTabs}
                  activeTabId={activeTabId}
                  onSelectTab={selectTab}
                  onCloseTab={closeTab}
                  onAddTab={() => setPicker((p) => !p)}
                  onReorderTabs={handleReorderTabs}
                  onMiddleClick={closeTab}
                  onContextMenu={(e) => e.preventDefault()}
                />
              </div>
            )}
            <div className="flex-1 relative overflow-hidden flex">
              {picker && <SessionPicker onClose={() => setPicker(false)} />}
              {activeTeam && (
                <TeamPanel
                  teamKey={activeTeam.key}
                  color={teamColor(activeTeam.color)}
                  name={teamLabel(activeTeam).label}
                  unnamed={teamLabel(activeTeam).unnamed}
                  lead={seatView(proto.seats[activeTeam.leadId], 'lead')}
                  members={liveMembers(activeTeam).map((m) => seatView(m, 'member'))}
                  activeTabId={activeTabId}
                  mode={proto.panelMode[activeTeam.key] ?? 'full'}
                  onSetMode={(m) => setPanelMode(activeTeam.key, m)}
                  onOpen={(sid) => openSeat(activeTeam.key, sid)}
                  onReorder={(ids) => reorderMembers(activeTeam.key, ids)}
                />
              )}
              <FakeTerminal tab={activeTab} seat={activeSeat} team={activeTeam} />
            </div>
          </div>
        </div>
        <ProtoControls stepIds={stepIds} onOpenPicker={() => setPicker(true)} />
      </div>
    </TeamDisplayContext.Provider>
  )
}
