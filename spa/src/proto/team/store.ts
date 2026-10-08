// spa/src/proto/team/store.ts — fake team data for the team-display prototype (no daemon).
//
// The shapes loosely mirror the PL-2b roster (lead/member seats, member order, tab binding) so the
// components can move to the real store later. Tabs, workspaces, hosts and lights live in the REAL stores.
import { create } from 'zustand'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { useAgentStore, type AgentStatus } from '../../stores/useAgentStore'
import { getPrimaryPane } from '../../lib/pane-tree'
import { compositeKey } from '../../lib/composite-key'
import { createTab } from '../../types/tab'
import type { ModelFamily } from '../../components/team/model-family'
import type { TeamGroupStyle, TeamSidebarStyle, TeamCollapseStyle, TeamHookStyle, TeamOpenMark, TeamHookTop, TeamCornerSize, TeamBadgeIcon, TeamEdgeWidth, TeamBookmarkCut, TeamBookmarkPos } from '../../components/team/team-display'

export interface ProtoSeat {
  sessionId: string
  title: string
  hostId: string
  code: string
  model: ModelFamily
  effort: string
  ctx: number
  teamKey: string | null
  role: 'lead' | 'member' | 'solo'
  alive: boolean
}

export interface ProtoTeam {
  key: string
  /** team_name: "" when the team has none (the label falls back to the lead's title). */
  name: string
  color: number
  leadId: string
  order: string[]
  collapsed: boolean
  /** Workspace of a lead whose tab is closed (the team runs on; the sidebar shows it faded). */
  ghostWs: string | null
}

interface ProtoState {
  seats: Record<string, ProtoSeat>
  teams: Record<string, ProtoTeam>
  panelMode: Record<string, 'full' | 'line'>
  beadHost: boolean
  groupStyle: TeamGroupStyle
  sidebarStyle: TeamSidebarStyle
  collapseStyle: TeamCollapseStyle
  hookStyle: TeamHookStyle
  hookTop: TeamHookTop
  cornerSize: TeamCornerSize
  badgeIcon: TeamBadgeIcon
  edgeWidth: TeamEdgeWidth
  bookmarkCut: TeamBookmarkCut
  bookmarkPos: TeamBookmarkPos
  openMark: TeamOpenMark
  /** Prototype switch: hide every team name, to see the fallback. */
  namesOff: boolean
  target: string | null
  log: string
  spawnN: number
}

const PANEL_KEY = 'purdex-proto-team-panel'

function loadPanelModes(): Record<string, 'full' | 'line'> {
  try { return JSON.parse(localStorage.getItem(PANEL_KEY) ?? '{}') } catch { return {} }
}

export const useProtoTeam = create<ProtoState>()(() => ({
  seats: {},
  teams: {},
  panelMode: loadPanelModes(),
  beadHost: true,
  groupStyle: 'corner-tr',
  sidebarStyle: 'hook',
  collapseStyle: 'users',
  hookStyle: 'thin',
  hookTop: 'below',
  cornerSize: 'md',
  badgeIcon: 'bookmark',
  edgeWidth: 2,
  bookmarkCut: 'third',
  bookmarkPos: 'above-right',
  openMark: 'tick',
  namesOff: false,
  target: null,
  log: '',
  spawnN: 0,
}))

const set = useProtoTeam.setState
const get = useProtoTeam.getState
export const say = (log: string) => set({ log })

/* ---------- lookups ---------- */

export function tabOfSeat(seat: Pick<ProtoSeat, 'hostId' | 'code'>): string | null {
  const { tabs } = useTabStore.getState()
  for (const t of Object.values(tabs)) {
    const c = getPrimaryPane(t.layout).content
    if (c.kind === 'tmux-session' && c.hostId === seat.hostId && c.sessionCode === seat.code) return t.id
  }
  return null
}

export function seatOfTab(tabId: string): ProtoSeat | null {
  const t = useTabStore.getState().tabs[tabId]
  if (!t) return null
  const c = getPrimaryPane(t.layout).content
  if (c.kind !== 'tmux-session') return null
  return Object.values(get().seats).find((s) => s.hostId === c.hostId && s.code === c.sessionCode) ?? null
}

export function liveMembers(team: ProtoTeam): ProtoSeat[] {
  const { seats } = get()
  return team.order.map((id) => seats[id]).filter((s): s is ProtoSeat => !!s && s.alive && s.teamKey === team.key)
}

const wsOfTab = (tabId: string) => useWorkspaceStore.getState().workspaces.find((w) => w.tabs.includes(tabId)) ?? null

/* ---------- tab actions (what the real handlers do, minus the daemon) ---------- */

export function selectTab(tabId: string) {
  const ws = wsOfTab(tabId)
  useTabStore.getState().setActiveTab(tabId)
  if (ws) {
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabId)
  }
  const seat = seatOfTab(tabId)
  if (seat) useAgentStore.setState((s) => ({ unread: { ...s.unread, [compositeKey(seat.hostId, seat.code)]: false } }))
  const team = seat?.teamKey ? get().teams[seat.teamKey] : null
  if (team && seat?.role === 'member' && team.collapsed) {
    patchTeam(team.key, { collapsed: false })
    say(`切到「${seat.title}」：它在收合的群組裡，群組自動展開（規則 10）。`)
  }
}

function patchTeam(key: string, patch: Partial<ProtoTeam>) {
  set((s) => ({ teams: { ...s.teams, [key]: { ...s.teams[key], ...patch } } }))
}
function patchSeat(id: string, patch: Partial<ProtoSeat>) {
  set((s) => ({ seats: { ...s.seats, [id]: { ...s.seats[id], ...patch } } }))
}

function addSeatTab(seat: ProtoSeat, wsId: string, afterTabId?: string | null): string {
  const tab = createTab({ kind: 'tmux-session', hostId: seat.hostId, sessionCode: seat.code, mode: 'terminal', cachedName: seat.title, tmuxInstance: '' })
  useTabStore.getState().addTab(tab)
  useWorkspaceStore.getState().insertTab(tab.id, wsId, afterTabId ?? null)
  return tab.id
}

/** Open a seat's tab, or switch to it (rules 3, 10, 11; X5 reopens a closed lead first). */
export function openSeat(teamKey: string | null, sessionId: string, via: 'click' | 'picker' = 'click') {
  const seat = get().seats[sessionId]
  if (!seat) return
  const team = teamKey ? get().teams[teamKey] : null
  const existing = tabOfSeat(seat)
  if (existing) {
    selectTab(existing)
    if (!team?.collapsed) say(`「${seat.title}」已經有分頁 → 直接切過去，不開第二個（規則 3）。`)
    return
  }
  const activeWs = useWorkspaceStore.getState().activeWorkspaceId
  if (!team) {
    const id = addSeatTab(seat, activeWs ?? useWorkspaceStore.getState().workspaces[0].id)
    selectTab(id)
    say(`開了「${seat.title}」（不屬於任何 team）。`)
    return
  }
  const lead = get().seats[team.leadId]
  let leadTab = tabOfSeat(lead)
  let reopened = false
  if (!leadTab) {
    leadTab = addSeatTab(lead, team.ghostWs ?? activeWs ?? useWorkspaceStore.getState().workspaces[0].id)
    patchTeam(team.key, { ghostWs: null })
    reopened = true
  }
  if (sessionId === lead.sessionId) {
    selectTab(leadTab)
    say(`重開 lead「${lead.title}」：群組回來了，member 都顯示未開（X5）。`)
    return
  }
  const ws = wsOfTab(leadTab)!
  // insert after the group's last tab, so the stored order stays tidy
  const groupTabs = ws.tabs.filter((id) => { const s = seatOfTab(id); return s?.teamKey === team.key })
  const id = addSeatTab(seat, ws.id, groupTabs[groupTabs.length - 1] ?? leadTab)
  const wasCollapsed = get().teams[team.key].collapsed
  if (wasCollapsed) patchTeam(team.key, { collapsed: false })
  selectTab(id)
  say(`開了「${seat.title}」：${via === 'picker' ? '從 session 清單開的 member 一樣開進 lead 群組（規則 11）' : '在上方 lead 群組開分頁並切過去（規則 3）'}${wasCollapsed ? '；群組原本收合，自動展開（規則 10）' : ''}${reopened ? '；lead 分頁原本關著，先一起重開（X5）' : ''}。`)
}

/** Close a tab. Closing the lead closes its group's tabs; every session keeps running (X5). */
export function closeTab(tabId: string) {
  const seat = seatOfTab(tabId)
  const team = seat?.teamKey ? get().teams[seat.teamKey] : null
  const ws = useWorkspaceStore.getState()
  if (team && seat?.role === 'lead') {
    const wsId = wsOfTab(tabId)?.id ?? null
    const groupTabs = [tabId, ...liveMembers(team).map((m) => tabOfSeat(m)).filter((x): x is string => !!x)]
    for (const id of groupTabs.slice(1)) ws.closeTabInWorkspace(id, { skipHistory: true })
    ws.closeTabInWorkspace(tabId, { skipHistory: true })
    patchTeam(team.key, { ghostWs: wsId, collapsed: false })
    say(`關掉 lead「${seat.title}」：群組 ${groupTabs.length} 個分頁一起關，session 都繼續跑；左邊留一列淡色 lead 和 member 顆粒，點它就重開，群組回來（X5）。`)
    return
  }
  ws.closeTabInWorkspace(tabId, { skipHistory: true })
  say(team ? `關掉「${seat!.title}」的分頁：它還是 member，顆粒變淡、面板那列標「未開」。` : '關掉分頁。')
}

export function toggleCollapse(teamKey: string) {
  const team = get().teams[teamKey]
  if (!team) return
  if (team.collapsed) { patchTeam(teamKey, { collapsed: false }); say('展開群組（規則 8）。'); return }
  const active = useTabStore.getState().activeTabId
  const as = active ? seatOfTab(active) : null
  patchTeam(teamKey, { collapsed: true })
  if (as?.teamKey === teamKey && as.role === 'member') {
    const leadTab = tabOfSeat(get().seats[team.leadId])
    if (leadTab) selectTab(leadTab)
    say(`停在 member「${as.title}」上收合 → 切回 lead（規則 9）。`)
  } else {
    say('收合群組：只看得到 lead，+N 是收起來的 member 分頁數；左右切換分頁會跳過它們（規則 8）。')
  }
}

export function reorderMembers(teamKey: string, ids: string[]) {
  const team = get().teams[teamKey]
  if (!team) return
  const others = team.order.filter((id) => !ids.includes(id))
  patchTeam(teamKey, { order: [...ids, ...others] })
  say('member 換了位置：左邊顆粒、上方群組、面板三處同步，lead 仍在最前面（規則 5）。')
}

export function setPanelMode(teamKey: string, mode: 'full' | 'line') {
  const panelMode = { ...get().panelMode, [teamKey]: mode }
  set({ panelMode })
  try { localStorage.setItem(PANEL_KEY, JSON.stringify(panelMode)) } catch { /* private mode */ }
  say(`面板改成${mode === 'full' ? '展開' : '一行'}；每個 team 各記各的，只記在這台機器（規則 17）。`)
}

/* ---------- events from the control panel ---------- */

const SPAWN_POOL: [string, ModelFamily, string][] = [
  ['purdex-docs-sync', 'sonnet', 'low'], ['purdex-e2e', 'sonnet', 'medium'], ['purdex-bump-check', 'haiku', 'low'],
  ['purdex-ios-spike', 'opus', 'high'], ['purdex-story', 'fable', 'medium'], ['purdex-a11y', 'sonnet', 'low'],
]

export function setLight(sessionId: string, status: AgentStatus | 'unread') {
  const seat = get().seats[sessionId]
  if (!seat) return
  const ck = compositeKey(seat.hostId, seat.code)
  if (status === 'unread') {
    useAgentStore.setState((s) => ({ unread: { ...s.unread, [ck]: true } }))
    say(`「${seat.title}」疊上未讀紅點。`)
    return
  }
  useAgentStore.setState((s) => ({ statuses: { ...s.statuses, [ck]: status } }))
  say(`「${seat.title}」的燈號 → ${{ running: '工作中', waiting: '等你回答', idle: '閒置', error: '失敗' }[status]}；顆粒、分頁、面板一起變。`)
}

export function setSubagents(sessionId: string, n: number) {
  const seat = get().seats[sessionId]
  if (!seat) return
  const ck = compositeKey(seat.hostId, seat.code)
  const refs = Array.from({ length: n }, (_, i) => ({ id: `${seat.code}-sub${i}`, type: 'Explore', started_at: 0, source_pid: 0, source_start_time: '' }))
  useAgentStore.setState((s) => ({ subagents: { ...s.subagents, [ck]: refs } }))
  say(`「${seat.title}」正在派 ${n} 個 subagent（藍點）。`)
}

export function spawn() {
  const active = useTabStore.getState().activeTabId
  const at = active ? seatOfTab(active) : null
  const teamKey = at?.teamKey ?? Object.keys(get().teams)[0]
  const team = get().teams[teamKey]
  const lead = get().seats[team.leadId]
  const n = get().spawnN
  const [name, model, effort] = SPAWN_POOL[n % SPAWN_POOL.length]
  const title = lead.title.startsWith('nexen') ? name.replace('purdex', 'nexen') : name
  const sessionId = `s-spawn-${n}`
  const seat: ProtoSeat = { sessionId, title, hostId: lead.hostId, code: `tm-sp${n}`, model, effort, ctx: 0, teamKey, role: 'member', alive: true }
  set((s) => ({ seats: { ...s.seats, [sessionId]: seat }, spawnN: n + 1, target: sessionId }))
  patchTeam(teamKey, { order: [...team.order, sessionId] })
  const ck = compositeKey(seat.hostId, seat.code)
  useAgentStore.setState((s) => ({ agentTypes: { ...s.agentTypes, [ck]: 'cc' }, statuses: { ...s.statuses, [ck]: 'running' } }))
  say(`${lead.title} spawn 了「${title}」：沒有開分頁，lead 底下多一顆淡色 bot、面板多一列「未開」、lead mode 行人數 +1。`)
}

/** kill / release: a member with a tab leaves the group as an ordinary tab (not closed); without one it disappears (rule 6). */
export function detach(sessionId: string, how: 'kill' | 'release') {
  const seat = get().seats[sessionId]
  const team = seat?.teamKey ? get().teams[seat.teamKey] : null
  if (!seat || !team || seat.role !== 'member') return
  patchSeat(sessionId, { teamKey: null, role: 'solo', alive: how === 'release' })
  patchTeam(team.key, { order: team.order.filter((x) => x !== sessionId) })
  const tab = tabOfSeat(seat)
  if (how === 'kill') {
    const ck = compositeKey(seat.hostId, seat.code)
    useAgentStore.setState((s) => {
      const { [ck]: _a, ...agentTypes } = s.agentTypes
      const { [ck]: _b, ...statuses } = s.statuses
      const { [ck]: _c, ...subagents } = s.subagents
      return { agentTypes, statuses, subagents }
    })
  }
  if (tab) {
    // land right after the former group in the workspace's stored order
    const ws = wsOfTab(tab)!
    const rest = ws.tabs.filter((id) => id !== tab)
    const lastGroup = [...rest].reverse().find((id) => seatOfTab(id)?.teamKey === team.key)
    const at = lastGroup ? rest.indexOf(lastGroup) + 1 : rest.length
    rest.splice(at, 0, tab)
    useWorkspaceStore.getState().reorderWorkspaceTabs(ws.id, rest)
  }
  const alive = Object.values(get().seats).filter((s) => s.role === 'member' && s.alive)
  set({ target: alive[alive.length - 1]?.sessionId ?? null })
  say(`「${seat.title}」被 ${how}：${tab ? '有分頁 → 脫離群組、變成群組右邊的獨立分頁，不自動關' + (how === 'kill' ? '（session 已結束）' : '（還活著，只是不屬於 team）') : '沒有分頁 → 直接從左邊消失'}；面板與 lead mode 行少一位（規則 6）。`)
}

export function becomeLead(sessionId: string) {
  const seat = get().seats[sessionId]
  if (!seat || seat.teamKey) return
  const used = Object.values(get().teams).map((t) => t.color)
  let color = 0
  while (used.includes(color)) color++
  const key = `team-${sessionId}`
  const memberId = `${sessionId}-m1`
  const member: ProtoSeat = { sessionId: memberId, title: 'nexen-docs', hostId: seat.hostId, code: `${seat.code}-m1`, model: 'sonnet', effort: 'low', ctx: 0, teamKey: key, role: 'member', alive: true }
  set((s) => ({
    seats: { ...s.seats, [sessionId]: { ...seat, teamKey: key, role: 'lead' }, [memberId]: member },
    teams: { ...s.teams, [key]: { key, name: '燈號', color, leadId: sessionId, order: [memberId], collapsed: false, ghostWs: null } },
    panelMode: { ...s.panelMode, [key]: 'full' },
  }))
  const ck = compositeKey(member.hostId, member.code)
  useAgentStore.setState((s) => ({ agentTypes: { ...s.agentTypes, [ck]: 'cc' }, statuses: { ...s.statuses, [ck]: 'running' } }))
  const tab = tabOfSeat(seat)
  if (tab) selectTab(tab)
  say(`${seat.title} 成為 lead（team 名「燈號」）並 spawn 了 nexen-docs：自動配到另一個 team 色（規則 13）；面板預設展開（規則 17）。`)
}

/** Next / previous tab in the active workspace's top order, skipping members of a collapsed group (rule 8). */
export function stepTab(dir: 1 | -1, visible: string[]) {
  if (visible.length === 0) return
  const active = useTabStore.getState().activeTabId
  const i = Math.max(0, visible.indexOf(active ?? ''))
  selectTab(visible[(i + dir + visible.length) % visible.length])
}

/** The label a team shows: its name, or the lead's title when it has none (or names are switched off). */
export function teamLabel(team: ProtoTeam): { label: string; unnamed: boolean } {
  const name = useProtoTeam.getState().namesOff ? '' : team.name.trim()
  if (name) return { label: name, unnamed: false }
  return { label: useProtoTeam.getState().seats[team.leadId]?.title ?? 'team', unnamed: true }
}
