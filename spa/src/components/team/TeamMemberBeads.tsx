// spa/src/components/team/TeamMemberBeads.tsx — the members under a lead row in the sidebar.
//
// One bead per member: the bot icon with its light (optionally the host badge), no name — the name is
// the tooltip. Beads are never faded: like the App's sidebar rows, the member being looked at is highlighted
// (the "active" look) and every other bead looks "inactive". Whether a member has a tab is a tiny tick under
// the bead (TeamOpenMark), or nothing. Click opens or switches; drag reorders within the team.
// The hook beside the beads runs down to the last wrapped row; clicking the hook or the blank area around the
// beads calls onBlankClick (the new fold style folds the team with it).
import { useCallback } from 'react'
import type { TeamHookStyle, TeamOpenMark, TeamSeatView } from './team-display'
import { TeamHook } from './TeamHook'
import { TeamSeatHostBadge, TeamSeatIcon } from './TeamSeatIcon'
import { useMemberDrag } from './useMemberDrag'

interface Props {
  teamKey: string
  color: string
  members: TeamSeatView[]
  activeTabId: string | null
  withHost: boolean
  hookStyle: TeamHookStyle | null
  openMark: TeamOpenMark
  onOpen: (sessionId: string) => void
  onReorder: (sessionIds: string[]) => void
  onBlankClick?: () => void
}

export function TeamMemberBeads({ teamKey, color, members, activeTabId, withHost, hookStyle, openMark, onOpen, onReorder, onBlankClick }: Props) {
  const order = members.map((m) => m.sessionId)
  const reorder = useCallback((ids: string[]) => onReorder(ids), [onReorder])
  const { propsFor, over, draggingId } = useMemberDrag(teamKey, order, reorder, 'x')
  if (members.length === 0) return null
  return (
    <div
      data-testid="team-beads"
      title={onBlankClick ? '點空白處收起' : undefined}
      onClick={(e) => { if (onBlankClick && !(e.target as HTMLElement).closest('[data-testid="team-bead"]')) onBlankClick() }}
      className={`relative flex flex-wrap items-center content-start gap-0.5 ml-[18px] mr-2 mb-0.5 pl-[18px] ${onBlankClick ? 'cursor-pointer' : ''}`}
    >
      {hookStyle && <TeamHook hookStyle={hookStyle} />}
      {members.map((m) => {
        const isActive = m.tabId !== null && m.tabId === activeTabId
        const ins = over?.id === m.sessionId ? (over.after ? 'after' : 'before') : null
        return (
          <button
            key={m.sessionId}
            type="button"
            data-testid="team-bead"
            data-session-id={m.sessionId}
            data-open={String(m.tabId !== null)}
            data-active={String(isActive)}
            title={m.title + (m.tabId ? '' : '（未開分頁）')}
            onClick={() => onOpen(m.sessionId)}
            {...propsFor(m.sessionId)}
            className={`group relative flex items-center gap-1 h-6 px-1.5 rounded-md cursor-pointer transition-colors ${
              isActive ? 'bg-surface-active text-white' : 'text-text-muted hover:bg-surface-hover hover:text-text-primary'
            } ${draggingId === m.sessionId ? 'opacity-30' : ''}`}
          >
            {ins && (
              <span
                className="absolute top-1 bottom-1 w-0.5 rounded"
                style={{ background: color, [ins === 'before' ? 'left' : 'right']: -2 }}
              />
            )}
            <TeamSeatIcon hostId={m.hostId} sessionCode={m.sessionCode} isActive={isActive} subagents={false} />
            {withHost && <TeamSeatHostBadge hostId={m.hostId} sessionCode={m.sessionCode} />}
            {openMark === 'tick' && m.tabId !== null && (
              <span data-testid="team-bead-open" className="absolute left-1/2 -translate-x-1/2 bottom-[1px] w-[3px] h-[3px] rounded-full pointer-events-none" style={{ background: color }} />
            )}
          </button>
        )
      })}
    </div>
  )
}
