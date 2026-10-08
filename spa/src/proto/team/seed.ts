// spa/src/proto/team/seed.ts — fill the REAL stores with a fixed scene (no daemon).
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { useHostStore } from '../../stores/useHostStore'
import { useAgentStore, type AgentStatus } from '../../stores/useAgentStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { compositeKey } from '../../lib/composite-key'
import { createTab, createWorkspace, type Tab } from '../../types/tab'
import { useProtoTeam, type ProtoSeat, DEFAULT_OPTIONS } from './store'

const MLAB = 'h-mlab'
const AIR = 'h-air26'

function tmuxTab(hostId: string, code: string, name: string): Tab {
  return createTab({ kind: 'tmux-session', hostId, sessionCode: code, mode: 'terminal', cachedName: name, tmuxInstance: '' })
}

export function seed() {
  useHostStore.setState({
    hosts: {
      [MLAB]: { id: MLAB, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0, icon: 'Cpu', color: '#3fb950' },
      [AIR]: { id: AIR, name: 'air26', ip: '100.64.0.4', port: 7860, order: 1, icon: 'Laptop', color: '#4a8fd1' },
    },
    hostOrder: [MLAB, AIR],
    runtime: { [MLAB]: { status: 'connected' }, [AIR]: { status: 'connected' } },
    activeHostId: MLAB,
  } as never)

  const seat = (sessionId: string, title: string, code: string, model: ProtoSeat['model'], effort: string, ctx: number, teamKey: string | null, role: ProtoSeat['role']): ProtoSeat =>
    ({ sessionId, title, hostId: MLAB, code, model, effort, ctx, teamKey, role, alive: true })
  const seats: Record<string, ProtoSeat> = {
    lead: seat('lead', 'purdex-1f', 'tm-1f', 'opus', 'high', 46, 't1', 'lead'),
    m1: seat('m1', 'purdex-team-view', 'tm-a1', 'sonnet', 'low', 31, 't1', 'member'),
    m2: seat('m2', 'purdex-interface-language-review', 'tm-b2', 'opus', 'medium', 72, 't1', 'member'),
    m3: seat('m3', 'purdex-lint', 'tm-c3', 'haiku', 'low', 12, 't1', 'member'),
    c4: seat('c4', 'nexen-c4', 'tm-c4', 'opus', 'medium', 18, null, 'solo'),
  }

  // Tabs: the stored order puts a member after an unrelated tab on purpose — the group still draws together.
  const notes = tmuxTab(AIR, 'notes', 'notes')
  const leadT = tmuxTab(MLAB, 'tm-1f', 'purdex-1f')
  const m1T = tmuxTab(MLAB, 'tm-a1', 'purdex-team-view')
  const log = tmuxTab(MLAB, 'pdx-log', 'pdx.log')
  const m2T = tmuxTab(MLAB, 'tm-b2', 'purdex-interface-language-review')
  const c4T = tmuxTab(MLAB, 'tm-c4', 'nexen-c4')
  const scratch = tmuxTab(AIR, 'scratch', 'scratch')
  const all = [notes, leadT, m1T, log, m2T, c4T, scratch]

  const ws1 = { ...createWorkspace('Purdex', 'Code'), tabs: [notes.id, leadT.id, m1T.id, log.id, m2T.id], activeTabId: leadT.id }
  const ws2 = { ...createWorkspace('實驗', 'Flask'), tabs: [c4T.id, scratch.id], activeTabId: c4T.id }

  useTabStore.setState({
    tabs: Object.fromEntries(all.map((t) => [t.id, t])),
    tabOrder: all.map((t) => t.id),
    activeTabId: leadT.id,
    visitHistory: [leadT.id],
  })
  useWorkspaceStore.setState({ workspaces: [ws1, ws2], activeWorkspaceId: ws1.id })

  const light = (code: string, status: AgentStatus, subs = 0, unread = false) => {
    const ck = compositeKey(MLAB, code)
    return { ck, status, subs, unread }
  }
  const lights = [light('tm-1f', 'running', 2), light('tm-a1', 'idle'), light('tm-b2', 'waiting', 0, true), light('tm-c3', 'running', 1), light('tm-c4', 'idle')]
  useAgentStore.setState({
    agentTypes: Object.fromEntries(lights.map((l) => [l.ck, 'cc'])),
    statuses: Object.fromEntries(lights.map((l) => [l.ck, l.status])),
    unread: Object.fromEntries(lights.map((l) => [l.ck, l.unread])),
    subagents: Object.fromEntries(lights.map((l) => [l.ck, Array.from({ length: l.subs }, (_, i) => ({ id: `${l.ck}-${i}`, type: 'Explore', started_at: 0, source_pid: 0, source_start_time: '' }))])),
  })

  useLayoutStore.setState({ activityBarWidth: 'wide', workspaceExpanded: { [ws1.id]: true, [ws2.id]: true } })
  if (!useLayoutStore.getState().tabPosition || useLayoutStore.getState().tabPosition === 'top') useLayoutStore.getState().setTabPosition('both')

  useProtoTeam.setState({
    ...DEFAULT_OPTIONS,
    seats,
    teams: { t1: { key: 't1', name: '介面線', color: 0, leadId: 'lead', order: ['m1', 'm2', 'm3'], collapsed: false, ghostWs: null } },
    target: 'm3',
    spawnN: 0,
    log: '初始：lead purdex-1f 帶 3 個 member；purdex-team-view 和 purdex-interface-language-review 已開分頁（後者存放順序在 pdx.log 後面，畫面上仍接在群組裡），purdex-lint 沒開。',
  })
}
