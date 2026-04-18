import { useEffect, useMemo, useRef } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  events: ElectronStreamCheckEvent[]
  streaming: boolean
}

export function DevBuildLogPanel({ events, streaming }: Props) {
  const t = useI18nStore((s) => s.t)
  const preRef = useRef<HTMLPreElement>(null)

  const text = useMemo(() => formatEvents(events), [events])
  // Stick to bottom only if the user was already near the bottom before the
  // new content arrived. Prevents the scroll from jumping back down when
  // the user is reading earlier log lines during a long build.
  const stickyRef = useRef(true)

  useEffect(() => {
    const el = preRef.current
    if (!el) return
    if (stickyRef.current) el.scrollTop = el.scrollHeight
  }, [text])

  const handleScroll = () => {
    const el = preRef.current
    if (!el) return
    stickyRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 4
  }

  const handleCopy = () => {
    if (!text) return
    navigator.clipboard?.writeText(text).catch(() => {})
  }

  return (
    <div className="space-y-2">
      <pre
        ref={preRef}
        onScroll={handleScroll}
        data-testid="dev-build-log"
        className="max-h-64 overflow-auto bg-surface-input border border-border-default rounded text-xs text-text-primary font-mono p-2 whitespace-pre-wrap"
      >
        {text || (streaming ? t('settings.dev.log.waiting') : '')}
      </pre>
      <div className="flex gap-2">
        <button
          onClick={handleCopy}
          disabled={!text}
          className="px-2 py-0.5 text-xs rounded bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover disabled:opacity-50 cursor-pointer disabled:cursor-default"
        >
          {t('settings.dev.log.copy')}
        </button>
      </div>
    </div>
  )
}

function formatEvents(events: ElectronStreamCheckEvent[]): string {
  const parts: string[] = []
  for (const ev of events) {
    if (ev.type === 'phase' && ev.phase) {
      parts.push(`── ${ev.phase} ──`)
    } else if ((ev.type === 'stdout' || ev.type === 'stderr') && ev.line != null) {
      parts.push(ev.line)
    } else if (ev.type === 'error' && ev.error) {
      parts.push(`✖ ${ev.error}`)
    }
  }
  return parts.join('\n')
}
