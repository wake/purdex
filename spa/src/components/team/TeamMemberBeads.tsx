// spa/src/components/team/TeamMemberBeads.tsx — the members under a lead row in the sidebar.
//
// One bead per member: the bot icon with its light (optionally the host badge), no name — the name is
// the tooltip. A member with no tab is a faded bead. Click opens or switches; drag reorders within the team.
import { useCallback } from 'react'
import type { TeamSeatView } from './team-display'
import { TeamSeatHostBadge, TeamSeatIcon } from './TeamSeatIcon'
import { useMemberDrag } from './useMemberDrag'

interface Props {
  teamKey: string
  color: string
  members: TeamSeatView[]
  activeTabId: string | null
  withHost: boolean
  onOpen: (sessionId: string) => void
  onReorder: (sessionIds: string[]) => void
}

export function TeamMemberBeads({ teamKey, color, members, activeTabId, withHost, onOpen, onReorder }: Props) {
  const order = members.map((m) => m.sessionId)
  const reorder = useCallback((ids: string[]) => onReorder(ids), [onReorder])
  const { propsFor, over, draggingId } = useMemberDrag(teamKey, order, reorder, 'x')
  if (members.length === 0) return null
  return (
    <div
      data-testid="team-beads"
      className="flex flex-wrap items-center gap-0.5 ml-[26px] mr-2 mb-0.5 pl-1.5 border-l-2"
      style={{ borderColor: color }}
    >
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
              isActive ? 'bg-surface-active text-white' : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
            } ${m.tabId ? '' : 'opacity-40 hover:opacity-70'} ${draggingId === m.sessionId ? 'opacity-30' : ''}`}
          >
            {ins && (
              <span
                className="absolute top-1 bottom-1 w-0.5 rounded"
                style={{ background: color, [ins === 'before' ? 'left' : 'right']: -2 }}
              />
            )}
            <TeamSeatIcon hostId={m.hostId} sessionCode={m.sessionCode} isActive={isActive} subagents={false} />
            {withHost && <TeamSeatHostBadge hostId={m.hostId} sessionCode={m.sessionCode} />}
          </button>
        )
      })}
    </div>
  )
}
