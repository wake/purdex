// spa/src/components/team/TeamPanel.tsx — the team panel hanging from the top of the content area.
//
// Full: a sidebar-like list, lead on top and fixed, members draggable; each person takes two lines.
// Line: one cell per person (bot + light, context ring around the model shape) plus the team name; the cells
// wrap to a second row when the team is big. Same width either way, no top color bar.
// Row look follows the sidebar: the member being looked at has the highlight + bright text, no side line.
// Clicking a person opens their tab or switches to it.
import { useCallback } from 'react'
import { CaretDown, CaretUp } from '@phosphor-icons/react'
import type { TeamSeatView } from './team-display'
import { TeamSeatHostBadge, TeamSeatIcon } from './TeamSeatIcon'
import { ContextRing } from './ModelIcon'
import { MODEL_LABEL, type ModelFamily } from './model-family'
import { useMemberDrag } from './useMemberDrag'

export interface TeamPanelSeat extends TeamSeatView {
  model?: ModelFamily
  effort?: string
  /** Context used, 0–100; undefined when unknown. */
  ctx?: number
}

interface Props {
  teamKey: string
  color: string
  /** Team name, or the fallback label when the team has none. */
  name: string
  unnamed?: boolean
  lead: TeamPanelSeat
  members: TeamPanelSeat[]
  activeTabId: string | null
  mode: 'full' | 'line'
  onSetMode: (mode: 'full' | 'line') => void
  onOpen: (sessionId: string) => void
  onReorder: (sessionIds: string[]) => void
}

const TASK_PLACEHOLDER = '任務摘要（另案）'

export function TeamPanel(props: Props) {
  const { mode } = props
  return (
    <div
      data-testid="team-panel"
      data-mode={mode}
      className="absolute top-0 right-3 z-20 rounded-b-lg border border-t-0 border-border-default bg-surface-elevated shadow-xl text-xs text-text-primary overflow-hidden"
      style={{ width: 312 }}
    >
      {mode === 'full' ? <FullPanel {...props} /> : <LinePanel {...props} />}
    </div>
  )
}

function Header({ color, name, unnamed, count, children }: { color: string; name: string; unnamed?: boolean; count?: number; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2 px-2.5 py-2">
      <span
        data-testid="team-panel-name"
        className={`px-1.5 rounded text-[11px] font-semibold leading-[18px] truncate ${unnamed ? 'italic opacity-80' : ''}`}
        style={{ background: color, color: '#14141f' }}
        title={unnamed ? `${name}（team 沒有名字，暫用 lead 的標題）` : name}
      >
        {name}
      </span>
      {count !== undefined && <span className="text-text-muted whitespace-nowrap">· {count} members</span>}
      {children}
    </div>
  )
}

function FullPanel({ teamKey, color, name, unnamed, lead, members, activeTabId, onSetMode, onOpen, onReorder }: Props) {
  const order = members.map((m) => m.sessionId)
  const reorder = useCallback((ids: string[]) => onReorder(ids), [onReorder])
  const { propsFor, over, draggingId } = useMemberDrag(teamKey, order, reorder, 'y')
  return (
    <>
      <Header color={color} name={name} unnamed={unnamed} count={members.length}>
        <button
          type="button"
          data-testid="team-panel-to-line"
          onClick={() => onSetMode('line')}
          className="ml-auto flex items-center gap-1 px-1.5 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
          title="縮成一行"
        >
          <CaretUp size={11} />
        </button>
      </Header>
      <div className="border-t border-border-subtle py-1.5 flex flex-col gap-1 max-h-[60vh] overflow-y-auto">
        <PanelRow seat={lead} color={color} isActive={lead.tabId !== null && lead.tabId === activeTabId} onOpen={onOpen} />
        {members.map((m) => (
          <PanelRow
            key={m.sessionId}
            seat={m}
            color={color}
           
            isActive={m.tabId !== null && m.tabId === activeTabId}
            onOpen={onOpen}
            drag={propsFor(m.sessionId)}
            insert={over?.id === m.sessionId ? (over.after ? 'after' : 'before') : null}
            dragging={draggingId === m.sessionId}
          />
        ))}
      </div>
    </>
  )
}

interface RowProps {
  seat: TeamPanelSeat
  color: string
  isActive: boolean
  onOpen: (sessionId: string) => void
  drag?: ReturnType<ReturnType<typeof useMemberDrag>['propsFor']>
  insert?: 'before' | 'after' | null
  dragging?: boolean
}

function PanelRow({ seat, color, isActive, onOpen, drag, insert, dragging }: RowProps) {
  const unopened = seat.tabId === null
  const modelText = seat.model ? `${MODEL_LABEL[seat.model]}${seat.effort ? ' · ' + seat.effort : ''}` : '—'
  const ctxText = seat.ctx !== undefined ? `${seat.ctx}%` : '—'
  return (
    <div
      role="button"
      tabIndex={0}
      data-testid="team-panel-row"
      data-session-id={seat.sessionId}
      data-role={seat.role}
      data-active={String(isActive)}
      onClick={() => onOpen(seat.sessionId)}
      onKeyDown={(e) => { if (e.key === 'Enter') onOpen(seat.sessionId) }}
      {...drag}
      className={`group relative mx-1.5 px-2 py-2 rounded-md cursor-pointer transition-colors ${
        isActive ? 'bg-surface-active text-white' : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
      } ${dragging ? 'opacity-30' : ''}`}
    >
      {insert && <span className="absolute left-2 right-2 h-0.5 rounded" style={{ background: color, [insert === 'before' ? 'top' : 'bottom']: -1 }} />}
      {/* Line 1: subagent dots (drawn by the icon, to its left) → bot + light → host badge → title → lead / unopened → context ring */}
      <div className="flex items-center gap-1.5 pl-1.5">
        <TeamSeatIcon hostId={seat.hostId} sessionCode={seat.sessionCode} isActive={isActive} />
        <TeamSeatHostBadge hostId={seat.hostId} sessionCode={seat.sessionCode} />
        <span className="truncate min-w-0 flex-1" title={seat.title}>{seat.title}</span>
        {seat.role === 'lead' && (
          <span className="text-[9.5px] px-1 rounded border flex-shrink-0 text-text-primary" style={{ borderColor: color }}>lead</span>
        )}
        {unopened && <span className="text-[9.5px] text-text-secondary flex-shrink-0">未開</span>}
        <span className="flex-shrink-0 text-text-primary" title={`${modelText} · context ${ctxText}`}><ContextRing pct={seat.ctx} model={seat.model} size={20} /></span>
      </div>
      {/* Line 2: the task summary only */}
      <div className="flex items-center pl-[26px] mt-1.5 text-[11px] leading-[16px] text-text-secondary min-w-0">
        <span className="truncate">{TASK_PLACEHOLDER}</span>
      </div>
    </div>
  )
}

function LinePanel({ color, name, unnamed, lead, members, activeTabId, onSetMode, onOpen }: Props) {
  const seats = [lead, ...members]
  return (
    <div className="flex items-start gap-2 px-2.5 py-2">
      <span
        data-testid="team-panel-name"
        className={`mt-[5px] px-1.5 rounded text-[11px] font-semibold leading-[18px] truncate max-w-[84px] flex-shrink-0 ${unnamed ? 'italic opacity-80' : ''}`}
        style={{ background: color, color: '#14141f' }}
        title={unnamed ? `${name}（team 沒有名字，暫用 lead 的標題）` : name}
      >
        {name}
      </span>
      <div data-testid="team-panel-cells" className="flex flex-wrap items-center gap-0.5 flex-1 min-w-0">
        {seats.map((s, i) => {
          const isActive = s.tabId !== null && s.tabId === activeTabId
          return (
            <span key={s.sessionId} className="flex items-center">
              {i === 1 && <span className="w-px h-4 bg-border-default mx-1" />}
              <button
                type="button"
                data-testid="team-panel-cell"
                data-session-id={s.sessionId}
                data-active={String(isActive)}
                onClick={() => onOpen(s.sessionId)}
                title={`${s.title} · ${s.model ? MODEL_LABEL[s.model] : '?'} · context ${s.ctx ?? '—'}%${s.tabId ? '' : ' · 未開'}`}
                className={`flex items-center gap-1 h-7 px-1 rounded-md cursor-pointer ${isActive ? 'bg-surface-active text-white' : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'}`}
              >
                <TeamSeatIcon hostId={s.hostId} sessionCode={s.sessionCode} isActive={isActive} subagents={false} />
                <ContextRing pct={s.ctx} model={s.model} />
              </button>
            </span>
          )
        })}
      </div>
      <button
        type="button"
        data-testid="team-panel-to-full"
        onClick={() => onSetMode('full')}
        className="mt-[5px] px-1 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer flex-shrink-0"
        title="展開"
      >
        <CaretDown size={11} />
      </button>
    </div>
  )
}
