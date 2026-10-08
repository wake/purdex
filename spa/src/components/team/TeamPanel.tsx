// spa/src/components/team/TeamPanel.tsx — the team panel hanging from the top of the content area.
//
// Full: a sidebar-like list, lead on top and fixed, members draggable; each person takes two lines.
// Line: one cell per person (bot + light, context ring around the model shape) plus the team name.
// Clicking a person opens their tab or switches to it.
import { useCallback } from 'react'
import { CaretDown, CaretUp } from '@phosphor-icons/react'
import type { TeamSeatView } from './team-display'
import { TeamSeatHostBadge, TeamSeatIcon } from './TeamSeatIcon'
import { ContextRing, ModelIcon } from './ModelIcon'
import { MODEL_LABEL, type ModelFamily } from './model-family'
import { useMemberDrag } from './useMemberDrag'

export interface TeamPanelSeat extends TeamSeatView {
  model?: ModelFamily
  effort?: string
  /** Context used, 0–100; undefined when unknown. */
  ctx?: number
}

/** Where the second line's pieces go (the user compares these). */
export type TeamPanelLayout = 'a' | 'b' | 'c'

interface Props {
  teamKey: string
  color: string
  name: string
  lead: TeamPanelSeat
  members: TeamPanelSeat[]
  activeTabId: string | null
  mode: 'full' | 'line'
  layout: TeamPanelLayout
  onSetMode: (mode: 'full' | 'line') => void
  onOpen: (sessionId: string) => void
  onReorder: (sessionIds: string[]) => void
}

const TASK_PLACEHOLDER = '任務摘要（另案）'

export function TeamPanel(props: Props) {
  const { color, mode } = props
  return (
    <div
      data-testid="team-panel"
      data-mode={mode}
      className="absolute top-0 right-3 z-20 rounded-b-lg border border-t-0 border-border-default bg-surface-elevated shadow-xl text-xs text-text-primary overflow-hidden"
      style={{ width: mode === 'full' ? 300 : undefined }}
    >
      <div className="h-[3px]" style={{ background: color }} />
      {mode === 'full' ? <FullPanel {...props} /> : <LinePanel {...props} />}
    </div>
  )
}

function Header({ color, name, count, children }: { color: string; name: string; count?: number; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2 px-2.5 py-1.5">
      <span className="w-2.5 h-2.5 rounded-[3px] flex-shrink-0" style={{ background: color }} />
      <span className="font-semibold truncate">{name}</span>
      {count !== undefined && <span className="text-text-muted whitespace-nowrap">· {count} members</span>}
      {children}
    </div>
  )
}

function FullPanel({ teamKey, color, name, lead, members, activeTabId, layout, onSetMode, onOpen, onReorder }: Props) {
  const order = members.map((m) => m.sessionId)
  const reorder = useCallback((ids: string[]) => onReorder(ids), [onReorder])
  const { propsFor, over, draggingId } = useMemberDrag(teamKey, order, reorder, 'y')
  return (
    <>
      <Header color={color} name={name} count={members.length}>
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
      <div className="border-t border-border-subtle py-1 max-h-[60vh] overflow-y-auto">
        <PanelRow seat={lead} color={color} layout={layout} isActive={lead.tabId !== null && lead.tabId === activeTabId} onOpen={onOpen} />
        {members.map((m) => (
          <PanelRow
            key={m.sessionId}
            seat={m}
            color={color}
            layout={layout}
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
  layout: TeamPanelLayout
  isActive: boolean
  onOpen: (sessionId: string) => void
  drag?: ReturnType<ReturnType<typeof useMemberDrag>['propsFor']>
  insert?: 'before' | 'after' | null
  dragging?: boolean
}

function PanelRow({ seat, color, layout, isActive, onOpen, drag, insert, dragging }: RowProps) {
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
      className={`group relative mx-1 px-2 py-1 rounded-md cursor-pointer transition-colors ${
        isActive ? 'bg-surface-active text-white' : 'hover:bg-surface-hover'
      } ${dragging ? 'opacity-30' : ''}`}
    >
      {isActive && <span className="absolute left-0 top-1 bottom-1 w-[3px] rounded" style={{ background: color }} />}
      {insert && <span className="absolute left-2 right-2 h-0.5 rounded" style={{ background: color, [insert === 'before' ? 'top' : 'bottom']: -1 }} />}
      {/* Line 1: subagent dots (drawn by the icon, to its left) → bot + light → host → title */}
      <div className={`flex items-center gap-1.5 pl-1.5 ${unopened ? 'opacity-55' : ''}`}>
        <TeamSeatIcon hostId={seat.hostId} sessionCode={seat.sessionCode} isActive={isActive} />
        <TeamSeatHostBadge hostId={seat.hostId} sessionCode={seat.sessionCode} withName />
        <span className="truncate min-w-0 flex-1" title={seat.title}>{seat.title}</span>
        {seat.role === 'lead' && (
          <span className="text-[9.5px] px-1 rounded border flex-shrink-0" style={{ borderColor: color }}>lead</span>
        )}
        {unopened && <span className="text-[9.5px] text-text-muted flex-shrink-0">未開</span>}
        {layout === 'b' && <span className="flex-shrink-0 text-text-secondary" title={modelText}><ModelIcon model={seat.model} /></span>}
        {layout === 'c' && <span title={`${modelText} · context ${ctxText}`}><ContextRing pct={seat.ctx} model={seat.model} size={20} /></span>}
      </div>
      {/* Line 2 */}
      <div className={`flex items-center gap-1.5 pl-[26px] mt-0.5 text-[11px] text-text-muted min-w-0 ${unopened ? 'opacity-70' : ''}`}>
        {layout === 'a' && (
          <>
            <span className="flex items-center gap-1 flex-shrink-0 text-text-secondary"><ModelIcon model={seat.model} />{modelText}</span>
            <span className="flex-shrink-0">·</span>
            <span className="flex-shrink-0 font-mono">{ctxText}</span>
            <span className="flex-shrink-0">·</span>
          </>
        )}
        {layout === 'b' && (
          <>
            <span className="flex-shrink-0 font-mono">ctx {ctxText}</span>
            <span className="flex-shrink-0">·</span>
          </>
        )}
        <span className="truncate italic opacity-70">{TASK_PLACEHOLDER}</span>
      </div>
    </div>
  )
}

function LinePanel({ color, name, lead, members, activeTabId, onSetMode, onOpen }: Props) {
  const seats = [lead, ...members]
  return (
    <Header color={color} name={name}>
      <div className="flex items-center gap-0.5">
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
                className={`flex items-center gap-1 h-7 px-1 rounded-md cursor-pointer ${isActive ? 'bg-surface-active text-white' : 'hover:bg-surface-hover'} ${s.tabId ? '' : 'opacity-45'}`}
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
        className="ml-1 px-1 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
        title="展開"
      >
        <CaretDown size={11} />
      </button>
    </Header>
  )
}
