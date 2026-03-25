// spa/src/components/HandoffButton.tsx
import { Terminal } from '@phosphor-icons/react'
import type { SessionStatus } from './SessionStatusBadge'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  inProgress: boolean
  progress?: string
  sessionStatus?: SessionStatus
  onHandoff: () => void
}

function isCCRunning(status?: SessionStatus): boolean {
  return status === 'cc-idle' || status === 'cc-running' || status === 'cc-waiting'
}

export default function HandoffButton({ inProgress, progress = '', sessionStatus, onHandoff }: Props) {
  const t = useI18nStore((s) => s.t)
  const ccAvailable = isCCRunning(sessionStatus)
  const disabled = inProgress || !ccAvailable

  function progressLabel(p: string): string {
    switch (p) {
      case 'starting': return t('stream.handoff.starting')
      case 'detecting': return t('stream.handoff.detecting')
      case 'stopping-cc': return t('stream.handoff.stopping_cc')
      case 'extracting-id': return t('stream.handoff.extracting')
      case 'exiting-cc': return t('stream.handoff.exiting_cc')
      case 'launching': return t('stream.handoff.launching')
      case 'stopping-relay': return t('stream.handoff.stopping_relay')
      case 'waiting-shell': return t('stream.handoff.waiting_shell')
      case 'launching-cc': return t('stream.handoff.launching_cc')
      default: return p || t('stream.handoff.connecting')
    }
  }

  return (
    <div className="flex flex-col items-center justify-center h-full gap-3">
      <button
        onClick={onHandoff}
        disabled={disabled}
        className="flex items-center gap-2 px-4 py-2 rounded-lg bg-blue-600 text-white text-sm hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        <Terminal size={16} />
        {inProgress ? progressLabel(progress) : t('stream.handoff.button')}
      </button>
      {!ccAvailable && !inProgress && (
        <p className="text-xs text-text-muted">{t('stream.handoff.no_cc')}</p>
      )}
    </div>
  )
}
