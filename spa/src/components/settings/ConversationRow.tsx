// spa/src/components/settings/ConversationRow.tsx — one conversation in Settings → Worker → 已退出 / 已消失
// (conversation entity spec §13.3): title, cwd with home as `~` (the full path on hover), last activity relative
// ("3 小時前"), 上次在 terminal / Worker. An ended row ends with 重建… unless the cwd is unknown (nothing) or no
// longer exists (「工作目錄已不存在」, §13.4). A gone row is disabled as a whole (U3): aria-disabled, muted, no
// button, and it ends with「對話檔已清除，無法再啟動」.
import type { ReactNode } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { relativeAge } from '../../lib/nex/relative-age'
import { shortenHome } from '../../lib/nex/conversation-search'
import type { ConversationRow as ConversationRowData } from '../../lib/nex/conversations-api'

export interface ConversationRowProps {
  row: ConversationRowData
  state: 'ended' | 'gone'
  /** The host's home directory (`ConversationsPage.home`), shortened to `~` in the cwd. */
  home: string
  /** Unix ms the age is measured against. A last activity ahead of it reads as just now (clamped, never negative). */
  now: number
  /** Ended: the rebuild button is shown but cannot be used (R-4-1: the projects root could not be listed). A gone
   * row is disabled whatever this says (U3). */
  disabled?: boolean
  onRebuild?: () => void
}

export function ConversationRow({ row, state, home, now, disabled = false, onRebuild }: ConversationRowProps) {
  const t = useI18nStore((s) => s.t)
  const age = relativeAge(row.last_activity_at, now)
  const lastIn = row.last_in === 'worker'
    ? t('settings.worker.conversations.last_in_worker')
    : t('settings.worker.conversations.last_in_terminal')

  const gone = state === 'gone'
  let action: ReactNode = null
  if (gone) {
    action = (
      <span data-testid="conversation-row-gone-note" className="shrink-0 text-xs text-text-muted">
        {t('settings.worker.gone.note')}
      </span>
    )
  } else if (row.cwd) {
    action = row.cwd_exists ? (
      <button type="button" data-testid="conversation-row-rebuild" disabled={disabled} onClick={onRebuild}
        className="shrink-0 px-1.5 py-0.5 rounded text-xs text-text-secondary enabled:cursor-pointer enabled:hover:text-text-primary enabled:hover:bg-surface-hover disabled:opacity-50">
        {t('settings.worker.exited.rebuild')}
      </button>
    ) : (
      <span data-testid="conversation-row-cwd-missing" className="shrink-0 text-xs text-text-muted">
        {t('settings.worker.conversations.cwd_missing')}
      </span>
    )
  }

  return (
    <div role="listitem" data-testid="conversation-row" data-state={state} aria-disabled={gone ? 'true' : undefined}
      className={`flex items-center gap-2 px-2 py-1.5 text-sm border-b border-border-subtle${gone ? ' opacity-60' : ''}`}>
      <div className="flex-1 min-w-0 flex flex-col gap-0.5">
        <span className="truncate text-text-primary" title={row.title}>{row.title}</span>
        <div className="flex items-center gap-2 min-w-0 text-xs text-text-muted">
          {row.cwd && (
            <span data-testid="conversation-row-cwd" className="min-w-0 truncate" title={row.cwd}>{shortenHome(row.cwd, home)}</span>
          )}
          <span data-testid="conversation-row-age" className="shrink-0">
            {t(`settings.worker.conversations.age.${age.key}`, { n: age.n })}
          </span>
          <span data-testid="conversation-row-last-in" className="shrink-0 px-1 rounded bg-surface-tertiary text-text-secondary">
            {lastIn}
          </span>
        </div>
      </div>
      {action}
    </div>
  )
}
