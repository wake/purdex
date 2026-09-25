// spa/src/components/execution/WorkerInfoPanel.tsx — the worker-info popover
// that opens from the header's name (worker pane spec §4.7): the facts the
// slimmed header no longer carries — provider, profile, the full cwd, the
// session id and whether the worker is archived. Pure presentation over the
// execution summary; `FloatingPanel` owns placement, Escape and outside-click.
import type { RefObject } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { FloatingPanel } from '../FloatingPanel'
import type { ExecutionSummary } from '../../lib/nex/types'

export interface WorkerInfoPanelProps {
  summary: ExecutionSummary
  anchorRef: RefObject<HTMLElement | null>
  onClose: () => void
}

/** Requested and effective differ → `requested → effective`; else whichever is known. */
function profileText(s: ExecutionSummary): string {
  const { requested_profile: req, effective_profile: eff } = s
  if (req && eff && req !== eff) return `${req} → ${eff}`
  return eff ?? req ?? '—'
}

export default function WorkerInfoPanel({ summary, anchorRef, onClose }: WorkerInfoPanelProps) {
  const t = useI18nStore((s) => s.t)
  const name = summary.cwd.split('/').filter(Boolean).pop() ?? summary.cwd
  const session = summary.session_id || summary.resume_session_id
  const rows: Array<{ key: string; label: string; value: string; mono?: boolean }> = [
    { key: 'provider', label: t('room.info.provider'), value: summary.provider },
    { key: 'profile', label: t('room.info.profile'), value: profileText(summary) },
    { key: 'cwd', label: t('room.info.cwd'), value: summary.cwd, mono: true },
    ...(session ? [{ key: 'session', label: t('room.info.session'), value: session, mono: true }] : []),
  ]
  return (
    <FloatingPanel title={name || summary.id} anchorRef={anchorRef} onClose={onClose} width={360} testId="worker-info-panel">
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        {rows.map((r) => (
          <div key={r.key} className="contents">
            <dt className="text-text-muted">{r.label}</dt>
            <dd data-testid={`worker-info-${r.key}`}
              className={`text-text-primary select-text break-all ${r.mono ? 'font-mono' : ''}`}>{r.value}</dd>
          </div>
        ))}
      </dl>
      {summary.archived && (
        <div data-testid="worker-info-archived" className="mt-2 text-xs text-status-warning">{t('room.info.archived')}</div>
      )}
    </FloatingPanel>
  )
}
