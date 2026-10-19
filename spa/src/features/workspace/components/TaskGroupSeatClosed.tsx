// spa/src/features/workspace/components/TaskGroupSeatClosed.tsx — a team seat no open tab shows, in the task-group view: dressed like InlineTab's persona row (avatar, host badge, bold name, label) but dimmed, and a click opens the seat.
import { Avatar } from '../../../components/Avatar'
import { HostBadge } from '../../../components/HostBadge'
import { useSeat } from '../../../lib/names/seat'
import { useHostLook } from '../../../lib/host-look'
import { hasHostBadge, resolveHostBadge } from '../../../lib/host-color'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'

interface Props {
  address: string
  label: string
  /** The SPA host the seat lives on; null = a host this Mac lacks (no badge). */
  hostId: string | null
  onOpen?: () => void
}

export function TaskGroupSeatClosed({ address, label, hostId, onOpen }: Props) {
  const seat = useSeat(address)
  const badgeEnabled = useUISettingsStore((s) => s.hostBadgeSidebarEnabled)
  const badgeLineColor = useUISettingsStore((s) => s.hostBadgeSidebarLineColor)
  const badgeBox = useUISettingsStore((s) => s.hostBadgeSidebarBox)
  const badgeInset = useUISettingsStore((s) => s.hostBadgeSidebarInset)
  const badgeRadius = useUISettingsStore((s) => s.hostBadgeSidebarRadius)
  const look = useHostLook(hostId)
  const badge = hostId === null ? null : resolveHostBadge(look, 'console')
  return (
    <button
      type="button" data-testid="task-group-seat-closed" onClick={onOpen} title={address}
      className="group mx-2 flex items-center gap-1.5 pl-[18px] pr-1.5 py-1 text-left text-xs text-text-muted opacity-60 cursor-pointer hover:bg-surface-hover hover:opacity-100 rounded-md"
    >
      {seat !== null && <Avatar size={18} code={seat.code} name={seat.name} src={seat.avatar} />}
      {badgeEnabled && badge !== null && hasHostBadge(badge) && (
        <HostBadge colors={badge.colors} icon={badge.icon} iconWeight={badge.iconWeight} box={badgeBox} inset={badgeInset} radius={badgeRadius} lineColor={badgeLineColor} />
      )}
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
