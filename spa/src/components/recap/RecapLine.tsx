// spa/src/components/recap/RecapLine.tsx — the second line of a team row and of the personal card: the newest recap's headline with 「截至 HH:MM」 (plan T14b;
// look from docs/pages/recap-view-mock.html). A click opens the session's recap history and goes no further (the row behind it switches tabs).
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { keepFocus } from '../../lib/keep-focus'
import { clockOf } from './recap-text'
import { useRecapLine } from './useRecapLine'

interface Props {
  hostId: string
  sessionId: string
  /** The session has ended: the headline is the last one it left (`最後：…`). */
  ended?: boolean
  onOpen: () => void
}

export function RecapLine({ hostId, sessionId, ended = false, onOpen }: Props) {
  const t = useI18nStore((s) => s.t)
  const line = useRecapLine(hostId, sessionId)
  if (line === null) return null
  const prefix = (text: string) => (ended ? t('personal.last', { line: text }) : text)
  let main: string
  let tip: string
  let lead: React.ReactNode = null
  let tail: React.ReactNode = null
  let quiet = false
  switch (line.kind) {
    case 'recap':
      main = prefix(line.headline)
      tip = t('recap.line.hint', { headline: main })
      tail = line.generating
        ? <span data-testid="recap-line-asof" className="text-text-muted flex-shrink-0 flex items-center gap-[3px]"><ArrowsClockwise size={10} className="animate-spin" />{t('recap.line.generating')}</span>
        : <span data-testid="recap-line-asof" className="text-text-muted flex-shrink-0 tabular-nums">{t('recap.line.as_of', { time: clockOf(line.at) })}</span>
      break
    case 'first_generating':
      main = t('recap.line.first_generating')
      tip = main
      quiet = true
      tail = <ArrowsClockwise size={10} className="animate-spin text-text-muted flex-shrink-0" />
      break
    case 'turn':
      main = line.title
      tip = t('recap.line.turn_hint')
      quiet = true
      lead = <span data-testid="recap-line-turn" className="flex-shrink-0 text-[9.5px] leading-[15px] px-1 rounded bg-surface-hover text-text-muted tabular-nums">{t('recap.line.turn', { n: line.no })}</span>
      break
  }
  return (
    <button
      type="button"
      data-testid="recap-line"
      title={tip}
      onMouseDown={keepFocus}
      onClick={(e) => { e.stopPropagation(); onOpen() }}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') e.stopPropagation() }}
      className="group/recap mt-1 w-full flex items-center gap-1.5 min-w-0 rounded text-left text-[11px] leading-4 text-text-secondary cursor-pointer"
    >
      {lead}
      <span className={`truncate flex-1 min-w-0 group-hover/recap:text-text-primary group-hover/recap:underline underline-offset-2 decoration-border-default ${quiet ? 'text-text-muted' : ''}`}>{main}</span>
      {tail}
    </button>
  )
}
