// spa/src/components/fleet/FleetCells.tsx — the host cells at the bottom of the sidebar: one cell per pool host, under the BottomNav. The wide bar lays them out like the session list's member beads
// (TeamMemberBeads: flex-wrap, no separators), so they wrap with the sidebar's width; the narrow bar stacks them in a column. No gap between cells in either (journal v5 U9: the hosts closer together; the cells'
// own 3 px sides still part them): at 0, five 44 px cells fit one line at the default 240 px width, at 1 px the fifth would wrap. The list, its order, each host's state and look come from the pool only
// (`useMonitorHosts`); nothing here reads the fleet or knows the wire.
import { useMemo } from 'react'
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'
import { cellOf } from '../../lib/pool/cell-model'
import { useMonitorHosts } from '../../lib/pool/use-monitor-hosts'
import { HostCell } from './HostCell'

export interface FleetCellsProps {
  variant: 'wide' | 'narrow'
}

export function FleetCells({ variant }: FleetCellsProps) {
  const hosts = useMonitorHosts()
  const cells = useMemo(() => hosts.map(cellOf), [hosts])
  if (cells.length === 0) return null
  const wide = variant === 'wide'

  return (
    <div
      data-testid="fleet-cells"
      data-variant={variant}
      className={wide ? 'flex flex-wrap shrink-0 items-center content-start gap-0 px-2 pb-2 pt-1' : 'flex shrink-0 flex-col items-center gap-0 pb-1'}
    >
      {cells.map((c) => (
        <HostCell key={c.hostId} view={c} variant={variant} isActive={false} onOpen={() => openWatchArea({ poolHost: { hostId: c.hostId, alias: c.name } })} />
      ))}
    </div>
  )
}
