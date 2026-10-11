// spa/src/lib/nex/worker-info.ts — what the status row says about a worker when the pointer rests on it (U4-5a, coverage E): provider, profile,
// the full cwd and the session id, one fact per line — the facts the old info popover held. Pure text over the execution summary.
import type { ExecutionSummary } from './types'

/** Requested and effective differ → `requested → effective`; else whichever is known. */
export function profileText(s: Pick<ExecutionSummary, 'requested_profile' | 'effective_profile'>): string {
  const { requested_profile: req, effective_profile: eff } = s
  if (req && eff && req !== eff) return `${req} → ${eff}`
  return eff ?? req ?? '—'
}

/** `label: value` lines; `t` is the i18n lookup (the `room.info.*` labels). */
export function workerInfoText(s: ExecutionSummary, t: (key: string) => string): string {
  const session = s.session_id || s.resume_session_id
  return [
    `${t('room.info.provider')}: ${s.provider}`,
    `${t('room.info.profile')}: ${profileText(s)}`,
    `${t('room.info.cwd')}: ${s.cwd}`,
    ...(session ? [`${t('room.info.session')}: ${session}`] : []),
    ...(s.archived ? [t('room.info.archived')] : []),
  ].join('\n')
}
