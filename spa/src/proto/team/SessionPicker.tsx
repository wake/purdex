// spa/src/proto/team/SessionPicker.tsx — a stand-in for New tab → session list (rule 11).
import { teamColor } from '../../components/team/team-display'
import { TeamSeatIcon } from '../../components/team/TeamSeatIcon'
import { useProtoTeam, tabOfSeat, openSeat } from './store'

export function SessionPicker({ onClose }: { onClose: () => void }) {
  const { seats, teams } = useProtoTeam()
  const rows = Object.values(seats).filter((s) => s.alive && !tabOfSeat(s))
  return (
    <div data-testid="session-picker" className="absolute left-3 top-2 z-30 w-72 rounded-lg border border-border-default bg-surface-elevated shadow-xl p-1 text-xs">
      <div className="px-2 py-1 text-text-muted">新分頁 · session 清單（模擬）</div>
      {rows.length === 0 && <div className="px-2 py-1 text-text-muted">所有 session 都已經開了</div>}
      {rows.map((s) => {
        const team = s.teamKey ? teams[s.teamKey] : null
        return (
          <button
            key={s.sessionId}
            type="button"
            data-testid="session-picker-row"
            data-session-id={s.sessionId}
            onClick={() => { openSeat(s.teamKey, s.sessionId, 'picker'); onClose() }}
            className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md hover:bg-surface-hover cursor-pointer text-left"
          >
            <TeamSeatIcon hostId={s.hostId} sessionCode={s.code} subagents={false} />
            <span className="truncate flex-1">{s.title}</span>
            {team && (
              <span className="text-[10px] px-1 rounded border flex-shrink-0" style={{ borderColor: teamColor(team.color) }}>
                {seats[team.leadId].title} 的 {s.role === 'lead' ? 'team' : 'member'}
              </span>
            )}
          </button>
        )
      })}
      <button type="button" onClick={onClose} className="w-full text-center text-text-muted hover:text-text-primary py-1 cursor-pointer">關閉</button>
    </div>
  )
}
