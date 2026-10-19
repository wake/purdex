// spa/src/lib/team/unattended-log.ts — what the unattended panel's log (UnattendedLogSection) works out of the entries: the merge across hosts and a decision's status tag.
import type { UnattendedLogDecision, UnattendedLogEntry } from './types'

export interface LogRow { hostId: string; entry: UnattendedLogEntry }

/** The newest `max` entries of every host's log, newest first. */
export function mergeLogs(logs: readonly { hostId: string; log: readonly UnattendedLogEntry[] }[], max = 30): LogRow[] {
  return logs.flatMap(({ hostId, log }) => log.map((entry) => ({ hostId, entry }))).sort((a, b) => b.entry.at - a.entry.at).slice(0, max)
}

type T = (key: string, params?: Record<string, string | number>) => string

export const TAG_CLASS = {
  green: 'bg-status-success/15 text-status-success-text',
  blue: 'bg-accent/15 text-accent',
  red: 'bg-status-error/15 text-status-error-text',
  yellow: 'bg-status-warning/20 text-status-warning-text',
  muted: 'bg-surface-hover text-text-secondary',
} as const

/** The status tag of a decision: who decided matters for approved and denied (the daemon on its own, or the person). */
export function tagOf(e: UnattendedLogDecision, t: T): { text: string; tone: keyof typeof TAG_CLASS } {
  // approved / denied always come with who decided; one that does not (a truncated or drifted answer) is shown plain rather than put on the person or the daemon
  if (e.state !== 'timeout' && e.decided_by === undefined) return { text: t(e.state === 'approved' ? 'approval.state.approved' : 'approval.state.denied'), tone: 'muted' }
  const auto = e.decided_by?.kind === 'unattended'
  if (e.state === 'timeout') return { text: t('unattended.log.tag_timeout'), tone: 'yellow' }
  if (e.state === 'approved') return auto ? { text: t('unattended.log.tag_auto_approved'), tone: 'green' } : { text: t('unattended.log.tag_approved'), tone: 'blue' }
  return auto ? { text: t('unattended.log.tag_auto_refused'), tone: 'red' } : { text: t('unattended.log.tag_refused'), tone: 'red' }
}
