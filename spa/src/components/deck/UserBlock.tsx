// spa/src/components/deck/UserBlock.tsx — the deck's one tinted block (U3 spec §4): a framed well with a caption by source
// (你 · HH:mm / 你 · HH:mm · 回合中送達 / 來自 <name> / 背景任務回報 / 排程喚醒). A bash-mode input shows as `! command`.
import { useI18nStore } from '../../stores/useI18nStore'
import { textOutput, userCaption, userTone, type UserTone } from '../../lib/conversations/deck-format'
import type { UserItem } from '../../lib/conversations/types'
import { OutputFold } from './OutputFold'
import { OWN_TEXT_SOURCES } from '../../lib/conversations/image-sniff'
import { UserText } from './UserText'
import { CjkText } from './CjkText'

// Three tones (c9d9c7-52 pt 4/7): the person's words are the loudest — an accent frame on a firmer tint; a peer's message is a teal frame on a faint
// tint with its body one step quieter; a system notice (task report, wake-up, a mod's notice) is the grey of the system rows, dashed and quieter still.
const TONE: Record<UserTone, { box: string; caption: string; body: string }> = {
  you: { box: 'rounded-md border border-accent bg-accent/12', caption: 'text-accent', body: 'text-text-primary' },
  peer: { box: 'rounded-md border border-peer/70 bg-peer/5', caption: 'text-peer', body: 'text-text-secondary' },
  notice: { box: 'rounded-md border border-dashed border-border-default', caption: 'text-text-system', body: 'text-text-system' },
}

export function UserBlock({ item, midTurn }: { item: UserItem; /** Not the first user message of its turn (a `queued` one then says it arrived mid-turn). */ midTurn?: boolean }) {
  const t = useI18nStore((s) => s.t)
  const cap = userCaption(item, midTurn)
  const caption =
    cap.kind === 'you' ? t('deck.user.you', { time: cap.time })
    : cap.kind === 'delivered' ? t('deck.user.delivered', { time: cap.time })
    : cap.kind === 'from' ? t('deck.user.from', { name: cap.name, time: cap.time })
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
  const tone = userTone(item)
  const c = TONE[tone]
  return (
    <div data-testid="deck-user" data-source={item.source} data-tone={tone} className={`${c.box} px-3 py-2`}>
      <div data-testid="deck-user-caption" className={`mb-1 text-xs font-medium ${c.caption}`}>{caption}</div>
      <div className={`whitespace-pre-wrap break-words text-sm ${c.body} ${bash ? 'font-mono' : ''}`}>
        {bash ? `! ${item.text}` : OWN_TEXT_SOURCES.has(item.source) ? <UserText text={item.text} /> : <CjkText text={item.text} />}
        {item.truncated && <span className="text-text-muted">…</span>}
      </div>
      {item.images && item.images.length > 0 && (
        <div data-testid="deck-user-images" className="mt-1 text-xs text-text-muted">{t('deck.user.images', { n: item.images.length })}</div>
      )}
    </div>
  )
}
