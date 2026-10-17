// spa/src/components/journal/JournalPages.tsx — the pieces of a session page (JV-4, spec §3.3): the header line naming the session's
// span, the notices (cut at the cap, earlier sessions possibly missing), the error block, and 上一頁 / 下一頁. Pure presentation: the card
// owns which page is shown (the unit's view memo) and what is read.
import { CaretLeft, CaretRight } from '@phosphor-icons/react'
import { dayKey, timeOf } from '../../lib/journal/stream'
import { useI18nStore } from '../../stores/useI18nStore'
import type { SessionLink } from '../../lib/journal/types'

const BTN = 'flex items-center gap-1 px-2 py-0.5 rounded text-[11px] text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer disabled:opacity-40 disabled:cursor-default disabled:hover:bg-transparent disabled:hover:text-text-secondary'

interface HeaderProps {
  /** The session being read; undefined while the chain is not read yet. */
  link: SessionLink | undefined
  current: boolean
  /** The chain's oldest listed session is shown and a predecessor reader is missing. */
  incomplete: boolean
  truncated: boolean
}

export function PageHeader({ link, current, incomplete, truncated }: HeaderProps) {
  const t = useI18nStore((s) => s.t)
  const start = link ? (link.startedAt > 0 ? t('journal.page.header', { start: `${dayKey(link.startedAt)} ${timeOf(link.startedAt)}` }) : t('journal.page.unknownStart')) : ''
  return (
    <div className="shrink-0 px-3 pb-1 text-[11px] text-text-muted">
      <div data-testid="journal-page-header" className="flex items-center gap-2 truncate">
        {start && <span className="truncate">{start}</span>}
        {current && <span className="px-1.5 rounded-full bg-accent-muted text-text-primary">{t('journal.page.current')}</span>}
      </div>
      {incomplete && <div data-testid="journal-page-incomplete">{t('journal.page.incomplete')}</div>}
      {truncated && <div data-testid="journal-page-truncated">{t('journal.page.truncated')}</div>}
    </div>
  )
}

export function PageError({ onRetry, testId = 'journal-page-error', messageKey = 'journal.page.error' }: { onRetry: (() => void) | null; testId?: string; messageKey?: string }) {
  const t = useI18nStore((s) => s.t)
  return (
    <div data-testid={testId} role="alert" className="shrink-0 flex items-center gap-2 px-3 py-2 text-[11.5px] text-status-error-text">
      <span className="flex-1">{t(messageKey)}</span>
      {onRetry && <button type="button" data-testid="journal-page-retry" onClick={onRetry} className={BTN}>{t('journal.page.retry')}</button>}
    </div>
  )
}

export function PagePager({ prev, next, onGo }: { prev: SessionLink | null; next: SessionLink | null; onGo: (link: SessionLink) => void }) {
  const t = useI18nStore((s) => s.t)
  return (
    <div data-testid="journal-pager" className="shrink-0 flex items-center justify-between px-3 py-1 border-t border-border-default">
      <button type="button" data-testid="journal-page-prev" disabled={prev === null} onClick={() => prev && onGo(prev)} className={BTN}><CaretLeft size={11} />{t('journal.page.prev')}</button>
      <button type="button" data-testid="journal-page-next" disabled={next === null} onClick={() => next && onGo(next)} className={BTN}>{t('journal.page.next')}<CaretRight size={11} /></button>
    </div>
  )
}
