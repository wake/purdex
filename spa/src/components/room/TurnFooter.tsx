// spa/src/components/room/TurnFooter.tsx — the per-turn footer line (spec
// §7.2, F1–F3): "✻ Worked for 38m 2s · done 5:48 PM". Rendered as the last
// child of a completed turn by RoomTranscript / ChatTranscript, keyed by
// `turnMeta[turn.boundary]`. Fixed English (F1) and not a search unit — it
// carries no `data-search-unit`.
import type { TurnMeta } from '../../lib/nex/event-reducer'
import { turnFooterText } from '../../lib/nex/turn-footer'

export interface TurnFooterProps {
  meta: TurnMeta
  /** Formats the completion time shown in the text (F2: system 12/24h setting); injectable for tests. */
  timeFormat?: Intl.DateTimeFormat
}

const DEFAULT_TIME_FORMAT = new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' })

export default function TurnFooter({ meta, timeFormat = DEFAULT_TIME_FORMAT }: TurnFooterProps) {
  const result = turnFooterText(meta, (ms) => timeFormat.format(new Date(ms)))
  if (!result) return null
  const toneColor = result.tone === 'error' ? 'text-[var(--wt-footer-error-color)]' : 'text-[var(--wt-footer-color)]'
  return (
    <div data-testid="turn-footer" title={result.title}
      className={`mt-1 text-[length:var(--wt-font-size)] ${toneColor} select-none`}>
      {result.text}
    </div>
  )
}
