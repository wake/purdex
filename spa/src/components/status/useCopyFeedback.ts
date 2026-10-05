// spa/src/components/status/useCopyFeedback.ts — the status bar's one copy confirmation (peer status bar spec §4.2).
import { useCallback, useEffect, useRef, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { copyText } from '../../lib/copy-text'

/** How long a copy confirmation stays in the fixed slot. */
const COPY_FEEDBACK_MS = 1500

export interface CopyFeedback {
  /** The confirmation text; '' when the slot is empty. */
  feedback: string
  failed: boolean
  /** Copy `value` and confirm it under the segment name `what` (already translated). */
  copy: (what: string, value: string) => Promise<void>
}

/**
 * One confirmation for every copy button of a bar, shown in a fixed slot (`StatusBarLayout`), so a copy never
 * reflows the row.
 */
export function useCopyFeedback(): CopyFeedback {
  const t = useI18nStore((s) => s.t)
  const [feedback, setFeedback] = useState('')
  const [failed, setFailed] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (timer.current) clearTimeout(timer.current) }, [])

  const copy = useCallback(async (what: string, value: string) => {
    let didFail = false
    try {
      // `copyText` genuinely rejects: the Electron window over plain http has
      // no `navigator.clipboard`, which is the reason that helper exists.
      await copyText(value)
    } catch {
      didFail = true
    }
    setFailed(didFail)
    setFeedback(didFail ? t('peer.copy_failed') : t('peer.copied', { what }))
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => { setFeedback(''); timer.current = null }, COPY_FEEDBACK_MS)
  }, [t])

  return { feedback, failed, copy }
}
