// spa/src/components/execution/BufferCountdown.tsx — the subtle "Esc to undo · 3s"
// tag on the optimistic line while its message is held in the send buffer.
import { useElapsedTicker } from '../../hooks/useElapsedTicker'
import { useI18nStore } from '../../stores/useI18nStore'

export default function BufferCountdown({ until }: { until: number }) {
  const t = useI18nStore((s) => s.t)
  const now = useElapsedTicker(true)
  const left = Math.max(1, Math.ceil((until - now) / 1000))
  return <span data-testid="send-buffer-countdown" className="text-[10px] font-normal text-text-muted">{t('execution.buffer.undo', { s: left })}</span>
}
