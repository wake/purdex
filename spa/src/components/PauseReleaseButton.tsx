// spa/src/components/PauseReleaseButton.tsx — 「解除」 for one paused session, shared by the unattended panel's 被擋下 section and
// the team panel's roster rows (UP-5b). No confirm dialog. A press is guarded by a ref (a double-click sends one POST) and the
// button is disabled while sending. 200 and 404 not_paused (already released) re-read the list quietly; 400 (bad claim) and any
// other failure show the error beside the button and enable it again.
import { useRef, useState } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { postPauseRelease } from '../lib/team/paused-api'
import { refreshPaused } from '../lib/team/paused-refresh'

export function PauseReleaseButton({ hostId, sessionId }: { hostId: string; sessionId: string }) {
  const t = useI18nStore((s) => s.t)
  const inFlight = useRef(false)
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const press = async () => {
    if (inFlight.current) return
    inFlight.current = true
    setSending(true)
    setError(null)
    try {
      const r = await postPauseRelease(hostId, sessionId)
      if (r.kind === 'released' || r.kind === 'not_paused') await refreshPaused(hostId)
      else setError(r.kind === 'bad_claim' ? r.detail : r.message)
    } finally {
      inFlight.current = false
      setSending(false)
    }
  }

  return (
    <span className="inline-flex items-center gap-1.5 flex-shrink-0">
      <button
        type="button"
        data-testid="pause-release"
        disabled={sending}
        // The team panel's row opens the seat on a click: this button is not that.
        onMouseDown={(e) => { e.stopPropagation() }}
        onKeyDown={(e) => e.stopPropagation()}
        onClick={(e) => { e.stopPropagation(); void press() }}
        className="px-2 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer disabled:opacity-40 disabled:pointer-events-none"
      >
        {t('paused.release')}
      </button>
      {error !== null && <span data-testid="pause-release-error" role="alert" className="text-status-warning-text">{t('paused.release.failed', { detail: error })}</span>}
    </span>
  )
}
