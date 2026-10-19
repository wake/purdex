// spa/src/features/workspace/components/TaskGroupSeatClosed.tsx — a team seat no open tab shows, in the task-group view: dressed like InlineTab's row (avatar or agent icon with its light, host badge, bold name, label) but dimmed, and a click opens the seat.
// The agent store is keyed by (hostId, sessionCode), so the icon, light and host badge come from the team panel's seat pieces, which read it without a tab.
import { Avatar } from '../../../components/Avatar'
import { TeamSeatIcon, TeamSeatHostBadge } from '../../../components/team/TeamSeatIcon'
import { useSeat } from '../../../lib/names/seat'
import { useSessionStore } from '../../../stores/useSessionStore'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'

interface Props {
  address: string
  label: string
  /** The SPA host the seat lives on; null = a host this Mac lacks (neutral host glyph, no light). */
  hostId: string | null
  /** The seat's tmux session name; with the host it names the session whose code keys the agent store. */
  tmuxSession?: string
  onOpen?: () => void
}

export function TaskGroupSeatClosed({ address, label, hostId, tmuxSession, onOpen }: Props) {
  const seat = useSeat(address)
  const badgeEnabled = useUISettingsStore((s) => s.hostBadgeSidebarEnabled)
  // name -> code as the team panel reads it (the first row of a name wins); '' while the session is not listed.
  const code = useSessionStore((s) => (hostId !== null && tmuxSession ? s.sessions[hostId]?.find((r) => r.name === tmuxSession)?.code ?? '' : ''))
  const host = hostId ?? ''
  return (
    <button
      type="button" data-testid="task-group-seat-closed" onClick={onOpen} title={address}
      className="group mx-2 flex items-center gap-1.5 pl-[18px] pr-1.5 py-1 text-left text-xs text-text-muted opacity-60 cursor-pointer hover:bg-surface-hover hover:opacity-100 rounded-md"
    >
      <TeamSeatIcon hostId={host} sessionCode={code} subagents avatar={seat !== null ? <Avatar size={18} code={seat.code} name={seat.name} src={seat.avatar} /> : undefined} />
      {badgeEnabled && <TeamSeatHostBadge hostId={host} sessionCode={code} />}
      <span className="min-w-0 flex-1 truncate">
        {seat !== null ? (
          <>
            <span data-testid="task-group-seat-name" className="font-semibold text-text-secondary group-hover:text-text-primary">{seat.name}</span>
            {label !== '' && label !== seat.name && <span className="ml-1.5">{label}</span>}
          </>
        ) : label}
      </span>
    </button>
  )
}
