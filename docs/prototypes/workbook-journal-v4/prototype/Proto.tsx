import { useEffect, useMemo, useRef, useState } from 'react'
import { Notebook, UsersThree } from '@phosphor-icons/react'
import { TitleBar } from '../../src/components/TitleBar'
import { TabBar } from '../../src/components/TabBar'
import { TeamDisplayProvider } from '../../src/components/team/TeamDisplayProvider'
import { TeamPanel } from '../../src/components/team/TeamPanel'
import { useTeamDisplay } from '../../src/components/team/team-display'
import { BUTTON, IDLE, PRESSED } from '../../src/components/title-bar-styles'
import { JournalCardV4 } from '../../src/components/journal-v4-proto/JournalCardV4'
import { ConfirmDetail, RoadmapTab, TasksTab } from '../../src/components/journal-v4-proto/SideTabs'
import { KIND_COLOR, openItems, type Entry } from '../../src/components/journal-v4-proto/types'
import { useHostStore } from '../../src/stores/useHostStore'
import { useSessionStore } from '../../src/stores/useSessionStore'
import { useTabStore } from '../../src/stores/useTabStore'
import { useTeamRosterStore } from '../../src/stores/useTeamRosterStore'
import { useTeamUiStore } from '../../src/stores/useTeamUiStore'
import { useShownHostsStore } from '../../src/stores/useShownHostsStore'
import { useWorkspaceStore } from '../../src/features/workspace/store'
import type { TeamRoster, RosterSession } from '../../src/lib/team/roster'
import type { Tab } from '../../src/types/tab'
import { roadmap, simEntries, solo, team, teamEntries, teamTasks } from './fixture'

const HOST = 'h1'
const TEAM = 't1'
const W_TEAM = 340
const W_JOURNAL = 400
const W_TASKS = 290
const GAP = 12
const NARROW_W = 840 // the simulated narrow window
// The window width at which each part still fits (dropped in this order when it shrinks).
const FIT_TASKS = W_TEAM + W_JOURNAL + W_TASKS + 4 * GAP + 20
const FIT_ROADMAP = W_TEAM + W_JOURNAL + 3 * GAP + 120
const FIT_TEAM = W_TEAM + W_JOURNAL + 3 * GAP
const CARD = 'pointer-events-auto overflow-hidden rounded-b-lg border border-t-0 border-border-default bg-surface-elevated shadow-xl text-xs'

const sessionId = (ref: string) => ref.replace('_', 's-')

function seed() {
  const mk = (id: string, title: string, extra: Partial<RosterSession> = {}): RosterSession => ({
    session_id: id, ref: `_${id}`, address: `mlab/${id}`, title, live: true, tmux_session: title, ...extra,
  })
  const members = team.members.map((m, i) => ({
    ...mk(sessionId(m.ref), m.title, { model: 'claude-sonnet-5-5', effort: m.effort, context: { used_percentage: m.ctx, window: 200000, at: 1 } }),
    state: 'active', origin: 'spawned', joined_at: i + 1,
  }))
  const roster = {
    id: TEAM, host_id: 'daemon', created_at: 1, team_name: team.name, team_label: team.label,
    lead: mk('lead', team.lead.title, { model: 'claude-opus-5-5', effort: 'high', context: { used_percentage: 41, window: 200000, at: 1 } }),
    members,
  } as unknown as TeamRoster
  const names = [team.lead.title, ...team.members.map((m) => m.title)]
  const tab = (id: string, tmux: string): Tab => ({
    id, pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId: HOST, sessionCode: `code-${tmux}`, mode: 'terminal', cachedName: tmux, tmuxInstance: 'i' } } },
  })
  const tabs = names.map((n, i) => tab(i === 0 ? 'lead' : `m${i - 1}`, n))
  useHostStore.setState({ hostOrder: [HOST] })
  useShownHostsStore.setState({ ids: [HOST] })
  useTeamRosterStore.setState({ byHost: { [HOST]: [roster] } })
  useSessionStore.setState({ sessions: { [HOST]: names.map((name) => ({ code: `code-${name}`, name, mode: 'terminal', cwd: '~' })) as never } })
  useTabStore.setState({ tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), tabOrder: tabs.map((t) => t.id), activeTabId: 'lead' })
  useWorkspaceStore.setState({
    workspaces: [{ id: 'w1', name: 'w1', tabs: tabs.map((t) => t.id), activeTabId: null, moduleConfig: {} }],
    activeWorkspaceId: 'w1',
  })
  // The real panel area is not mounted (the prototype docks its own); keep the team in `full` so TitleBar shows no strip.
  useTeamUiStore.setState({ panelMode: { [`${HOST}\u0000${TEAM}`]: 'full' }, teamDrill: {}, panel: { width: W_TEAM } })
}
seed()

interface DockProps {
  hasTeam: boolean
  narrow: boolean
  roadmapOpen: boolean
  tasksOpen: boolean
  setRoadmapOpen: (v: boolean) => void
  setTasksOpen: (v: boolean) => void
  entries: Entry[]
  setEntries: React.Dispatch<React.SetStateAction<Entry[]>>
}

function Dock(p: DockProps) {
  const display = useTeamDisplay()
  const teamData = display ? display.panelTeam('lead') : null
  const [teamOpen, setTeamOpen] = useState(true)
  const [journalOpen, setJournalOpen] = useState(true)
  const [selected, setSelected] = useState<string | null>(null)
  const [hoverTask, setHoverTask] = useState<string | null>(null)
  const area = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(1400)
  useEffect(() => {
    const el = area.current
    if (!el) return
    const ro = new ResizeObserver(() => setWidth(el.clientWidth))
    ro.observe(el)
    setWidth(el.clientWidth)
    return () => ro.disconnect()
  }, [])

  const { hasTeam, entries } = p
  const waiting = entries.reduce((n, e) => n + (e.kind === 'confirm' ? openItems(e) : 0), 0)
  const showTasks = journalOpen && p.tasksOpen && width >= FIT_TASKS
  const showRoadmap = journalOpen && p.roadmapOpen && width >= FIT_ROADMAP
  const showTeam = hasTeam && teamOpen && width >= FIT_TEAM
  const leftCol = showTeam || showRoadmap
  const detail = entries.find((e) => e.id === selected) ?? null
  const tasks = hasTeam ? teamTasks : solo.tasks

  const togglePin = (id: string) => p.setEntries((es) => es.map((e) => (e.id === id ? { ...e, pinned: !e.pinned } : e)))

  ;(window as unknown as Record<string, unknown>).__dock = { setTeamOpen, setJournalOpen, setSelected, setHoverTask }

  const inAnim = { animation: 'dock-in 180ms ease-out' }
  return (
    <>
      <div className="relative">
        <TitleBar title={`Purdex · ${hasTeam ? team.lead.title : solo.name}`} />
        <div className="absolute right-[104px] top-0 h-9 flex items-center gap-0.5" style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}>
          {hasTeam && (
            <button type="button" data-testid="entry-team" aria-pressed={teamOpen} onClick={() => setTeamOpen(!teamOpen)} className={`${BUTTON} ${teamOpen ? PRESSED : IDLE} flex items-center gap-1 text-[11px]`} title="團隊">
              <UsersThree size={14} />團隊
            </button>
          )}
          <button type="button" data-testid="entry-journal" aria-pressed={journalOpen} onClick={() => setJournalOpen(!journalOpen)} className={`${BUTTON} ${journalOpen ? PRESSED : IDLE} relative flex items-center gap-1 text-[11px]`} title="日誌">
            <Notebook size={14} />日誌
            {waiting > 0 && (
              <span data-testid="journal-badge" className="ml-0.5 min-w-[15px] h-[15px] px-1 rounded-full text-[10px] leading-[15px] text-center font-semibold" style={{ background: KIND_COLOR.confirm, color: '#3a2f00' }}>{waiting}</span>
            )}
          </button>
        </div>
      </div>
      <TabBarShell />
      <div className="flex-1 min-h-0 relative">
        <div className="absolute inset-0 bg-terminal-bg text-terminal-fg font-mono text-[12px] leading-5 p-4 overflow-hidden">
          <div className="text-text-muted">$ pdx team status</div>
          {hasTeam ? (
            <>
              <div>team「{team.name}」 · {team.members.length + 1} 人 · 上限 {team.cap}</div>
              <div>lead  {team.lead.title}   {team.lead.address}</div>
              {team.members.map((m) => <div key={m.ref}>member  {m.title}  {m.ref}  {m.model}/{m.effort}  ctx {m.ctx}%</div>)}
            </>
          ) : <div>(not in a team) session {solo.name}</div>}
          <div className="mt-2 text-text-muted">$ _</div>
        </div>
        <div ref={area} className="absolute inset-0 pointer-events-none" />
        <div className="absolute top-0 right-3 z-20 flex items-start gap-3 h-full max-h-[calc(100%-12px)] pointer-events-none">
          {leftCol && (
            <div data-testid="left-col" className="flex flex-col gap-3 h-full min-h-0" style={{ width: W_TEAM }}>
              {showTeam && (
                <div data-testid="card-team" className={`${CARD} overflow-y-auto flex-none`} style={{ maxHeight: showRoadmap ? '52%' : '100%', ...inAnim }}>
                  {teamData ? <TeamPanel team={{ ...teamData, mode: 'full' }} activeTabId="lead" width={W_TEAM} onSetMode={() => {}} onOpen={() => {}} onReorder={() => {}} /> : <div className="p-3 text-text-muted">no team</div>}
                </div>
              )}
              {showRoadmap && (
                <div data-testid="card-roadmap" className={`${CARD} flex-1 min-h-0 ${showTeam ? 'rounded-t-lg border-t' : ''}`} style={inAnim}>
                  <RoadmapTab features={roadmap} />
                </div>
              )}
            </div>
          )}
          {journalOpen && (
            <div className="relative h-full" style={{ width: W_JOURNAL, ...inAnim }}>
              <div data-testid="card-journal" className={`${CARD} h-[min(660px,100%)]`}>
                <JournalCardV4
                  name={hasTeam ? `${team.name} · 團隊日誌` : solo.name}
                  roleLine={hasTeam ? `leader ${team.lead.title} 的日誌 · ${team.members.length} 位 member` : '單一 session · 沒有 team'}
                  live entries={entries}
                  hoverTask={hoverTask} onHoverTask={setHoverTask}
                  onTogglePin={togglePin}
                  selectedConfirm={selected} onOpenConfirm={(id) => setSelected(id === selected ? null : id)}
                  roadmapOpen={p.roadmapOpen} tasksOpen={p.tasksOpen}
                  onToggleRoadmap={() => p.setRoadmapOpen(!p.roadmapOpen)} onToggleTasks={() => p.setTasksOpen(!p.tasksOpen)}
                  onClose={() => setJournalOpen(false)}
                />
              </div>
              {detail && (
                <div data-testid="card-detail" className={`${CARD} absolute top-0 right-full mr-3 z-30 h-[min(660px,100%)]`} style={{ width: W_TEAM, animation: 'detail-in 160ms ease-out' }}>
                  <ConfirmDetail e={detail} onClose={() => setSelected(null)} />
                </div>
              )}
            </div>
          )}
          {showTasks && (
            <div data-testid="card-tasks" className={`${CARD} h-[min(660px,100%)]`} style={{ width: W_TASKS, ...inAnim }}>
              <TasksTab tasks={tasks} solo={!hasTeam} hoverTask={hoverTask} onHoverTask={setHoverTask} />
            </div>
          )}
        </div>
      </div>
    </>
  )
}

function TabBarShell() {
  const tabsById = useTabStore((s) => s.tabs)
  const order = useTabStore((s) => s.tabOrder)
  const tabs = useMemo(() => order.map((id) => tabsById[id]), [order, tabsById])
  const active = useTabStore((s) => s.activeTabId)
  return (
    <div className="bg-surface-secondary border-b border-border-subtle">
      <TabBar
        tabs={tabs} activeTabId={active}
        onSelectTab={(id) => useTabStore.setState({ activeTabId: id })}
        onCloseTab={() => {}} onAddTab={() => {}} onReorderTabs={() => {}} onMiddleClick={() => {}} onContextMenu={() => {}}
      />
    </div>
  )
}

export function Proto() {
  const [hasTeam, setHasTeam] = useState(true)
  const [roadmapOpen, setRoadmapOpen] = useState(true)
  const [tasksOpen, setTasksOpen] = useState(true)
  const [narrow, setNarrow] = useState(false)
  const [teamEntriesS, setTeamEntries] = useState<Entry[]>(teamEntries)
  const [soloEntriesS, setSoloEntries] = useState<Entry[]>(solo.entries)
  const n = useRef(0)
  const entries = hasTeam ? teamEntriesS : soloEntriesS
  const setEntries = hasTeam ? setTeamEntries : setSoloEntries

  const simulate = () => {
    const s = simEntries[n.current % simEntries.length]
    n.current += 1
    const t = `14:${String(10 + n.current * 3).padStart(2, '0')}`
    setEntries((es) => [...es, { ...s, actor: hasTeam ? s.actor : s.actor.split('purdex-88-b8').join(solo.name), id: `sim${n.current}`, date: '2026-10-14', time: t, items: s.items?.map((i) => ({ ...i })) }])
  }
  // Haiku ticks the next open item; when the entry's last item is ticked it unpins.
  const tick = () => {
    setEntries((es) => {
      const i = es.findIndex((e) => e.kind === 'confirm' && openItems(e) > 0)
      if (i < 0) return es
      const e = es[i]
      const k = e.items!.findIndex((it) => !it.done)
      const items = e.items!.map((it, j) => (j === k ? { ...it, done: true } : it))
      const left = items.filter((it) => !it.done).length
      const next = es.slice()
      next[i] = { ...e, items, pinned: left === 0 ? false : e.pinned }
      return next
    })
  }

  const btn = (on?: boolean) => `px-2 py-0.5 rounded border text-[11px] ${on ? 'border-accent bg-accent-muted text-text-primary' : 'border-border-default text-text-secondary hover:text-text-primary hover:bg-surface-hover'}`
  return (
    <div className="h-screen bg-black/40 flex">
      <div className="h-screen flex flex-col bg-surface-primary text-text-primary font-sans relative overflow-hidden" style={{ width: narrow ? NARROW_W : '100%' }} data-testid="window">
        <style>{`@keyframes dock-in{from{transform:translateY(-12px);opacity:0}to{transform:none;opacity:1}}@keyframes detail-in{from{transform:translateX(16px);opacity:0}to{transform:none;opacity:1}}[data-testid="team-notebook-wrap"]{display:none}`}</style>
        <TeamDisplayProvider>
          <Dock
            hasTeam={hasTeam} narrow={narrow}
            roadmapOpen={roadmapOpen} tasksOpen={tasksOpen} setRoadmapOpen={setRoadmapOpen} setTasksOpen={setTasksOpen}
            entries={entries} setEntries={setEntries}
          />
        </TeamDisplayProvider>
        <div data-testid="proto-toolbar" className="absolute left-2 bottom-2 z-50 flex flex-wrap items-center gap-1 px-2 py-1 rounded-md border border-dashed border-status-warning bg-surface-secondary/95 shadow-lg">
          <span className="text-[10px] text-status-warning-text mr-1">原型工具列</span>
          <button className={btn(hasTeam)} onClick={() => setHasTeam(true)}>有 team</button>
          <button className={btn(!hasTeam)} onClick={() => setHasTeam(false)}>單一 session</button>
          <button className={btn(roadmapOpen)} onClick={() => setRoadmapOpen(!roadmapOpen)}>路線圖</button>
          <button className={btn(tasksOpen)} onClick={() => setTasksOpen(!tasksOpen)}>任務</button>
          <button className={btn(narrow)} onClick={() => setNarrow(!narrow)}>窄視窗</button>
          <button className={btn()} onClick={simulate}>模擬新事件</button>
          <button className={btn()} onClick={tick}>模擬 Haiku 打勾</button>
        </div>
      </div>
    </div>
  )
}
