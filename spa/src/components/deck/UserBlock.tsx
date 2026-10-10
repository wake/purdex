// spa/src/components/deck/UserBlock.tsx — the deck's one tinted block (U3 spec §4): a framed well with a caption by source
// (你 · HH:mm / 你 · 排隊中 / 來自 <name> / 背景任務回報 / 排程喚醒). A bash-mode input shows as `! command`.
import { useI18nStore } from '../../stores/useI18nStore'
import { textOutput, userCaption } from '../../lib/conversations/deck-format'
import type { UserItem } from '../../lib/conversations/types'
import { OutputFold } from './OutputFold'

export function UserBlock({ item }: { item: UserItem }) {
  const t = useI18nStore((s) => s.t)
  const cap = userCaption(item)
  const caption =
    cap.kind === 'you' ? t('deck.user.you', { time: cap.time })
    : cap.kind === 'queued' ? t('deck.user.queued')
    : cap.kind === 'from' ? t('deck.user.from', { name: cap.name })
    : cap.kind === 'background' ? t('deck.user.background')
    : t('deck.user.schedule')
  const bash = item.source === 'bash'
  // A command's output sits under the bash-mode input that ran it, folded like a step's output (spec §4).
  if (item.source === 'command_output') {
    return (
      <div data-testid="deck-user" data-source={item.source} className="pl-6">
        <OutputFold foldKey={`${item.id}:out`} output={textOutput(item.text, item.truncated)} />
      </div>
    )
  }
  return (
    <div data-testid="deck-user" data-source={item.source} className="rounded-lg border border-border-subtle bg-surface-secondary px-3 py-2">
      <div data-testid="deck-user-caption" className="mb-1 text-xs text-text-muted">{caption}</div>
      <div className={`whitespace-pre-wrap break-words text-sm text-text-primary ${bash ? 'font-mono' : ''}`}>
        {bash ? `! ${item.text}` : item.text}
        {item.truncated && <span className="text-text-muted">…</span>}
      </div>
      {item.images && item.images.length > 0 && (
        <div data-testid="deck-user-images" className="mt-1 text-xs text-text-muted">{t('deck.user.images', { n: item.images.length })}</div>
      )}
    </div>
  )
}
