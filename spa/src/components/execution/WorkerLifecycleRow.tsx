// spa/src/components/execution/WorkerLifecycleRow.tsx — where a worker is in its life, as one system row in the deck's stream (U4-4a; spec §3.5): queued,
// starting, rejected / failed to start (with the reason), terminated (with its reason). The system rows' grey, left-aligned, one size under the text,
// like the live line; not interactive. A running or idle worker has none: the live line and the light say it.
import { useI18nStore } from '../../stores/useI18nStore'
import { lifecycleText } from '../../lib/nex/worker-ended'
import type { ExecutionSummary } from '../../lib/nex/types'

export function WorkerLifecycleRow({ summary, starting = false }: { summary: ExecutionSummary; starting?: boolean }) {
  const t = useI18nStore((s) => s.t)
  const text = lifecycleText(summary, starting, t)
  if (!text) return null
  return <div data-testid="worker-lifecycle" role="status" className="text-left deck-t-down text-text-system">{text}</div>
}
