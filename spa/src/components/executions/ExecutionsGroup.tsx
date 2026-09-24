// spa/src/components/executions/ExecutionsGroup.tsx — one `labels.source`
// bucket of the sidebar Executions view (P-C spec §4.3): `local` and
// `purdex` have i18n headers, any other source is shown raw (the Aigora
// hook — no Aigora code).
import { useI18nStore } from '../../stores/useI18nStore'
import type { ExecutionGroup } from '../../lib/nex/execution-groups'
import { ExecutionRowCompact } from './ExecutionRowCompact'

const KNOWN_SOURCES: Record<string, string> = {
  local: 'executions.group.local',
  purdex: 'executions.group.purdex',
}

interface Props {
  group: ExecutionGroup
  /** The daemon's own host id (`capabilities.host_id`) — what it stamps into `origin`; null until known. */
  daemonHostId: string | null
  now: number
  /** Absent → the rows are listed but not openable (the host is hidden in this workbench — plan H2d-2). */
  onOpen?: (executionId: string) => void
}

export function ExecutionsGroup({ group, daemonHostId, now, onOpen }: Props) {
  const t = useI18nStore((s) => s.t)
  const key = KNOWN_SOURCES[group.source]
  const label = key ? t(key) : group.source

  return (
    <div data-testid={`executions-group-${group.source}`} className="flex flex-col">
      <div className="px-3 pt-2 pb-0.5 text-[10px] uppercase tracking-wide text-text-muted truncate">{label}</div>
      {group.rows.map((row) => (
        <ExecutionRowCompact key={row.id} row={row} daemonHostId={daemonHostId} now={now} onOpen={onOpen ? () => onOpen(row.id) : undefined} />
      ))}
    </div>
  )
}
