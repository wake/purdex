import { useEffect, useMemo, useRef, useState } from 'react'
import { Notebook, UsersThree } from '@phosphor-icons/react'
import { TitleBar } from '../../src/components/TitleBar'
import { TabBar } from '../../src/components/TabBar'
import { TeamDisplayProvider } from '../../src/components/team/TeamDisplayProvider'
import { TeamPanel } from '../../src/components/team/TeamPanel'
import { useTeamDisplay } from '../../src/components/team/team-display'
import { BUTTON, IDLE, PRESSED } from '../../src/components/title-bar-styles'
import { JournalPanel } from '../../src/components/journal/JournalPanel'
import type { JournalEvent } from '../../src/components/journal/journal-types'
import { useHostStore } from '../../src/stores/useHostStore'
import { useSessionStore } from '../../src/stores/useSessionStore'
import { useTabStore } from '../../src/stores/useTabStore'
import { useTeamRosterStore } from '../../src/stores/useTeamRosterStore'
import { useTeamUiStore } from '../../src/stores/useTeamUiStore'
import { useShownHostsStore } from '../../src/stores/useShownHostsStore'
import { useWorkspaceStore } from '../../src/features/workspace/store'
import type { TeamRoster, RosterSession } from '../../src/lib/team/roster'
import type { Tab } from '../../src/types/tab'
import fixture from './fixture.json'

const HOST = 'h1'
const TEAM = 't1'
const PANEL_W = 340
const CARD = 'pointer-events-auto overflow-y-auto rounded-b-lg border border-t-0 border-border-default bg-surface-elevated shadow-xl text-xs'

type Fx = typeof fixture
const sessionId = (ref: string) => ref.replace('_', 's-')

function seed() {
  const fx: Fx = fixture
  const mk = (id: string, title: string, tmux: string, extra: Partial<RosterSession> = {}): RosterSession => ({
    session_id: id, ref: `_${id}`, address: `mlab/${id}`, title, live: true, tmux_session: tmux, ...extra,
  })
  const members = fx.team.members.map((m, i) => ({
    ...mk(sessionId(m.ref), m.title, m.title, {
      model: m.model.toLowerCase() === 'sonnet' ? 'claude-sonnet-5-5' : 'claude-opus-5-5',
      effort: m.effort,
      context: { used_percentage: m.ctx, window: 200000, at: 1 },
    }),
    state: 'active', origin: 'spawned', joined_at: i + 1,
  }))
  const roster = {
    id: TEAM, host_id: 'daemon', created_at: 1, team_name: fx.team.name, team_label: fx.team.label,
    lead: mk('lead', fx.team.lead.title, fx.team.lead.title, { model: 'claude-opus-5-5', effort: 'high', context: { used_percentage: 41, window: 200000, at: 1 } }),
    members,
  } as unknown as TeamRoster
  const names = [fx.team.lead.title, ...fx.team.members.map((m) => m.title)]
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
  useTeamUiStore.setState({ panelMode: { [`${HOST}\u0000${TEAM}`]: 'full' }, teamDrill: {}, panel: { width: PANEL_W } })
}
seed()

// The fixture's requests are all answered by now, so 等我回覆 starts at 0; the simulated 請求 below is the one that waits.
const baseEvents: JournalEvent[] = (fixture.events as JournalEvent[]).map((e) => ({ ...e }))

const SIM: Omit<JournalEvent, 'date' | 'time'>[] = [
  { kind: 'exec', kindLabel: '執行', actor: 'purdex-a-line', title: '增量 R1 回來：無 critical，開始整理彙整表', body: '', refs: ['PR #2765'] },
  { kind: 'comm', kindLabel: '溝通', actor: 'purdex-lease-v2 → 我', title: '#2767 紅測試已寫好，等你放行', body: '', refs: ['#2767'] },
  { kind: 'req', kindLabel: '請求', actor: 'purdex-coordinator → 使用者', title: '接力 v2 的第一個 PR 已經審完，沒有發現問題，要合併嗎？', body: '合併後要等下一次部署才會生效。', refs: ['PR #2765'], pending: true },
]

type View = 'none' | 'journal' | 'both'
type PanelId = 'team' | 'journal'

function Dock({ events }: { events: JournalEvent[] }) {
  const display = useTeamDisplay()
  const team = display ? display.panelTeam('lead') : null
  const [open, setOpen] = useState<PanelId[]>([]) // oldest-opened first
  const [width, setWidth] = useState(() => window.innerWidth)
  useEffect(() => {
    const on = () => setWidth(window.innerWidth)
    window.addEventListener('resize', on)
    return () => window.removeEventListener('resize', on)
  }, [])
  // Both cards need two widths plus the gaps; when they do not fit, the journal takes the space and the team card hides.
  const narrow = width < 2 * PANEL_W + 3 * 12
  const showTeam = open.includes('team') && !(narrow && open.includes('journal'))
  const showJournal = open.includes('journal')
  const toggle = (id: PanelId) => setOpen((o) => (o.includes(id) ? o.filter((x) => x !== id) : [...o, id]))
  ;(window as unknown as { __setView?: (v: View) => void }).__setView = (v) =>
    setOpen(v === 'none' ? [] : v === 'journal' ? ['journal'] : ['team', 'journal'])

  const [live] = useState(true)
  return (
    <>
      <div className="relative">
        <TitleBar title="Purdex · purdex-coordinator" />
        {/* The two entries, separate buttons, placed where the title bar's notebook button sits (that one is hidden below). */}
        <div className="absolute right-[104px] top-0 h-9 flex items-center gap-0.5" style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}>
          <button type="button" data-testid="entry-team" aria-pressed={open.includes('team')} onClick={() => toggle('team')} className={`${BUTTON} ${open.includes('team') ? PRESSED : IDLE} flex items-center gap-1 text-[11px]`} title="團隊">
            <UsersThree size={14} />團隊
          </button>
          <button type="button" data-testid="entry-journal" aria-pressed={open.includes('journal')} onClick={() => toggle('journal')} className={`${BUTTON} ${open.includes('journal') ? PRESSED : IDLE} flex items-center gap-1 text-[11px]`} title="日誌">
            <Notebook size={14} />日誌
          </button>
        </div>
      </div>
      <TabBarShell />
      <div className="flex-1 min-h-0 relative">
        <div data-testid="terminal-area" className="absolute inset-0 bg-terminal-bg text-terminal-fg font-mono text-[12px] leading-5 p-4 overflow-hidden">
          <div className="text-text-muted">$ pdx team status</div>
          <div>team「{fixture.team.name}」 · {fixture.team.members.length + 1} 人 · 上限 {fixture.team.cap}</div>
          <div>lead  {fixture.team.lead.title}   {fixture.team.lead.address}</div>
          {fixture.team.members.map((m) => <div key={m.ref}>member  {m.title}  {m.ref}  {m.model}/{m.effort}  ctx {m.ctx}%</div>)}
          <div className="mt-2 text-text-muted">$ _</div>
        </div>
        {/* Floating cards under the title bar, like TeamPanelArea: the team card is always left, the journal card right of it. */}
        <div className="absolute top-0 right-3 z-20 flex items-start gap-3 max-h-[calc(100%-12px)] h-full pointer-events-none">
          {showTeam && (
            <div data-testid="card-team" className={CARD} style={{ width: PANEL_W, maxHeight: '100%', animation: 'dock-in 180ms ease-out' }}>
              {team ? <TeamPanel team={{ ...team, mode: 'full' }} activeTabId="lead" width={PANEL_W} onSetMode={() => {}} onOpen={() => {}} onReorder={() => {}} /> : <div className="p-3 text-text-muted">no team</div>}
            </div>
          )}
          {showJournal && (
            <div data-testid="card-journal" className={CARD} style={{ width: PANEL_W, height: 'min(640px, 100%)', animation: 'dock-in 180ms ease-out' }}>
              <JournalPanel session={fixture.session} roleLine={`lead · ${fixture.team.name} · ${fixture.team.members.length} 位 member`} live={live} events={events} onClose={() => toggle('journal')} />
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
  const [events, setEvents] = useState<JournalEvent[]>(baseEvents)
  const n = useRef(0)
  const simulate = () => {
    const s = SIM[n.current++ % SIM.length]
    const d = new Date()
    setEvents((e) => [...e, { ...s, date: '2026-10-13', time: `17:${String(32 + n.current).padStart(2, '0')}` }])
    void d
  }
  const view = (v: View) => (window as unknown as { __setView?: (v: View) => void }).__setView?.(v)
  const btn = 'px-2 py-0.5 rounded border border-border-default text-[11px] text-text-secondary hover:text-text-primary hover:bg-surface-hover'
  const memo = useMemo(() => <TeamDisplayProvider><Dock events={events} /></TeamDisplayProvider>, [events])
  return (
    <div className="h-screen flex flex-col bg-surface-primary text-text-primary font-sans relative">
      <style>{`@keyframes dock-in{from{transform:translateY(-12px);opacity:0}to{transform:none;opacity:1}}[data-testid="team-notebook-wrap"]{display:none}`}</style>
      {memo}
      <div data-testid="proto-toolbar" className="absolute left-2 bottom-2 z-50 flex items-center gap-1 px-2 py-1 rounded-md border border-dashed border-status-warning bg-surface-secondary/95 shadow-lg">
        <span className="text-[10px] text-status-warning-text mr-1">原型工具列</span>
        <button className={btn} onClick={() => view('none')}>都關著</button>
        <button className={btn} onClick={() => view('journal')}>只開日誌</button>
        <button className={btn} onClick={() => view('both')}>兩個都開</button>
        <button className={btn} data-testid="simulate" onClick={simulate}>模擬新事件</button>
      </div>
    </div>
  )
}
