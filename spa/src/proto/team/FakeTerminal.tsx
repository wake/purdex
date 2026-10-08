// spa/src/proto/team/FakeTerminal.tsx — a stand-in for the terminal: a Claude Code screen, with the
// mod's "lead mode · N members" line at the very bottom of the lead's terminal (rule 14).
import type { Tab } from '../../types/tab'
import { teamColor } from '../../components/team/team-display'
import { MODEL_LABEL } from '../../components/team/model-family'
import { liveMembers, type ProtoSeat, type ProtoTeam } from './store'
import { useAgentStore } from '../../stores/useAgentStore'
import { compositeKey } from '../../lib/composite-key'

export function FakeTerminal({ tab, seat, team }: { tab: Tab | null; seat: ProtoSeat | null; team: ProtoTeam | null }) {
  const subs = useAgentStore((s) => (seat ? s.subagents[compositeKey(seat.hostId, seat.code)]?.length ?? 0 : 0))
  const base = 'flex-1 min-w-0 flex flex-col font-mono text-[12px] leading-[1.6] px-4 pt-3 pb-2 bg-terminal-bg text-terminal-fg'
  if (!tab) return <div className={base}><span className="opacity-60">沒有分頁</span></div>
  if (!seat) {
    return (
      <div className={base}>
        <div className="opacity-60">~/Workspace/wake/purdex $</div>
        <div>tail -f ~/.config/pdx/logs/pdx.log</div>
        <div className="opacity-60">2026-10-09 00:41:07 team.roster seq=88 members=3</div>
      </div>
    )
  }
  if (!seat.alive) {
    return <div className={`${base} items-center justify-center font-sans opacity-70`}>「{seat.title}」的 session 已結束。分頁不會自動關，看完可以自己關。</div>
  }
  const isLead = team !== null && seat.role === 'lead'
  const n = isLead ? liveMembers(team!).length : 0
  return (
    <div className={base} data-testid="fake-terminal">
      <div className="flex-1 min-h-0 overflow-hidden">
        <div className="opacity-60">╭─ Claude Code ─ {MODEL_LABEL[seat.model]} 5.5 · {seat.effort} ─ ~/Workspace/wake/purdex</div>
        <div><span style={{ color: '#a78bfa' }}>⏺</span> {isLead ? '派 member 平行處理介面 PR，我負責整合與 review。' : '照 plan 的 task 3 寫測試，先跑受影響的 vitest。'}</div>
        <div className="opacity-60">  ⎿ {isLead ? 'pdx spawn --model sonnet --title purdex-lint …' : 'cd spa && npx vitest run src/components/team/TeamPanel.test.tsx'}</div>
        <div><span style={{ color: '#4ade80' }}>⏺</span> {isLead ? 'member 都起來了，等它們回報。' : '6 passed'}</div>
      </div>
      <div className="border border-current/40 rounded-md px-2.5 py-1 my-1.5 opacity-90">&gt; <span className="opacity-50">▌</span></div>
      <div className="whitespace-pre opacity-60">  ⏵⏵ auto mode on (shift+tab to cycle)</div>
      {Array.from({ length: Math.min(subs, 3) }, (_, i) => (
        <div key={i} className="whitespace-pre opacity-60">  ◯ {['Explore · 讀 TabBar.tsx', 'general-purpose · 盤點 roster 欄位', 'Explore · 找 FloatingPanel 用法'][i]}   running</div>
      ))}
      {isLead && (
        <div data-testid="lead-mode-line" className="whitespace-pre" style={{ color: teamColor(team!.color) }}>
          {`  lead mode · ${n} member${n === 1 ? '' : 's'}`}
        </div>
      )}
    </div>
  )
}
