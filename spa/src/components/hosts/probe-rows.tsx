// spa/src/components/hosts/probe-rows.tsx — the badge and status dot of a probe host row (rows themselves: use-probe-rows.ts).
import { resolveHostColors, DEFAULT_HOST_ICON, isIconWeight, isPhosphorIconName, type ResolvedHostColors } from '../../lib/host-color'
import { HostBadge } from '../HostBadge'
import { useI18nStore } from '../../stores/useI18nStore'
import type { ProbeRowView, ProbeStatus } from './use-probe-rows'

export function ProbeBadge({ row, box = 16 }: { row: Pick<ProbeRowView, 'icon' | 'iconWeight' | 'colors'>; box?: number }) {
  const colors: ResolvedHostColors | null = resolveHostColors({ colors: row.colors }, 'console')
  const icon = row.icon && isPhosphorIconName(row.icon) ? row.icon : DEFAULT_HOST_ICON
  return <HostBadge colors={colors} icon={icon} iconWeight={isIconWeight(row.iconWeight) ? row.iconWeight : undefined} box={box} inset={Math.round(box / 6)} radius={4} lineColor="host" testId="probe-badge" />
}

const DOT: Record<ProbeStatus, string> = { ok: 'bg-green-400', offline: 'bg-text-muted', error: 'bg-red-400', unknown: 'bg-border-default' }

export function ProbeStatusDot({ status }: { status: ProbeStatus }) {
  const label = useI18nStore((s) => s.t)(`fleet_hosts.status.${status}`)
  return <span role="img" aria-label={label} title={label} data-testid="probe-status" data-status={status} className={`inline-block w-2 h-2 rounded-full shrink-0 ${DOT[status]}`} />
}
