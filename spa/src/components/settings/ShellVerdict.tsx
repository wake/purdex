import { CheckCircle, CircleNotch, Question, XCircle } from '@phosphor-icons/react'
import type { ShellResolveVerdict } from '../../lib/host-api'

type TFn = (key: string, params?: Record<string, string | number>) => string

/**
 * Every `reason` the daemon can return. An unknown one is a newer daemon than
 * this build; it still renders as "did not resolve" rather than a raw token.
 */
const REASON_LABEL: Record<string, string> = {
  not_found: 'resume_template.verdict.not_found',
  shell_metacharacters: 'resume_template.verdict.shell_metacharacters',
  too_long: 'resume_template.verdict.too_long',
  timeout: 'resume_template.verdict.timeout',
  shell_failed: 'resume_template.verdict.shell_failed',
}

/** The shell resolve-command verdict, shared by resume templates and normal commands. */
export function ShellVerdict({ testId, verdict, t }: { testId: string; verdict: ShellResolveVerdict | 'pending'; t: TFn }) {
  const cls = 'flex items-center gap-1'
  if (verdict === 'pending') {
    return (
      <span data-testid={testId} data-status="pending" className={`${cls} text-text-secondary`}>
        <CircleNotch size={14} className="animate-spin" />
        {t('resume_template.verdict.pending')}
      </span>
    )
  }
  if (verdict.status === 'resolved') {
    return (
      <span data-testid={testId} data-status="resolved" className={`${cls} text-status-success`}>
        <CheckCircle size={14} />
        {t('resume_template.verdict.resolved', { detail: verdict.detail })}
      </span>
    )
  }
  if (verdict.status === 'unverifiable') {
    return (
      <span data-testid={testId} data-status="unverifiable" className={`${cls} text-text-secondary`}>
        <Question size={14} />
        {t('resume_template.verdict.unverifiable')}
      </span>
    )
  }
  return (
    <span data-testid={testId} data-status="unresolved" data-reason={verdict.reason} className={`${cls} text-status-warning`}>
      <XCircle size={14} />
      {t(REASON_LABEL[verdict.reason] ?? 'resume_template.verdict.unresolved')}
    </span>
  )
}
