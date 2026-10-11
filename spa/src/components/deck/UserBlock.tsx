// spa/src/components/deck/UserBlock.tsx — the deck's one tinted block (U3 spec §4): a framed well with a caption by source
// (你 · HH:mm / 你 · HH:mm · 回合中送達 / 來自 <name> / 背景任務回報 / 排程喚醒). A bash-mode input shows as `! command`.
import { useI18nStore } from '../../stores/useI18nStore'
import { textOutput, userCaption, userTone, type UserTone } from '../../lib/conversations/deck-format'
import type { UserItem } from '../../lib/conversations/types'
import { OutputFold } from './OutputFold'
import { OWN_TEXT_SOURCES } from '../../lib/conversations/image-sniff'
import { UserText } from './UserText'
import { CjkText } from './CjkText'
import { usePeerCollapse } from './usePeerCollapse'

// Three tones (c9d9c7-52 pt 4/7): the person's words are the loudest — an accent frame on a firmer tint; a peer's message is a teal frame on a faint
// tint with its body one step quieter; a system notice (task report, wake-up, a mod's notice) is the grey of the system rows, dashed and quieter still.
const TONE: Record<UserTone, { box: string; caption: string; body: string }> = {
  // the same frame and tint as the sidebar's selected workspace row (WorkspaceRow isActive), so the person's words read as "selected"; the caption is primary text
  // because the accent on that tint falls under 4.5:1 in dark (3.2)
  // one small step dimmer than the first cut (ring purple-400 → 500, tint 25% → 20%): Mac feedback 10-11
  you: { box: 'rounded-md ring-1 ring-purple-500 bg-[#8b5cf6]/20', caption: 'text-text-primary', body: 'text-text-primary' },
  peer: { box: 'rounded-md border border-peer/70 bg-peer/5', caption: 'text-peer', body: 'text-text-secondary' },
  notice: { box: 'rounded-md border border-dashed border-border-default', caption: 'text-text-system', body: 'text-text-system' },
}

/** A message the agent has not accepted yet (held by the App, or in Claude Code's queue): the first cut of the person's block — a dark well in a thin accent frame, the caption in accent. */
export const PENDING_USER_BOX = 'rounded-md border border-accent bg-accent/12'
export const PENDING_USER_CAPTION = 'text-accent'

type Fold = ReturnType<typeof usePeerCollapse>
interface BlockProps { item: UserItem; /** Not the first user message of its turn (a `queued` one then says it arrived mid-turn). */ midTurn?: boolean }

/** A peer message folds to three lines (deck rules 2026-10-11); the fold memory is only asked for here, so the other tones need no provider. */
function PeerBlock({ item, midTurn }: BlockProps) {
  const fold = usePeerCollapse(`${item.id}:peerfold`, item.text)
  return <UserBox item={item} midTurn={midTurn} fold={fold} />
}

export function UserBlock(props: BlockProps) {
  return userTone(props.item) === 'peer' && props.item.source !== 'command_output' ? <PeerBlock {...props} /> : <UserBox {...props} />
}

function UserBox({ item, midTurn, fold }: BlockProps & { fold?: Fold }) {
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
    <div data-testid="deck-user" data-source={item.source} data-tone={tone} className={`${c.box} px-3 py-2 ${fold?.boxClass ?? ''}`} {...fold?.boxProps}>
      {fold?.collapsible && <button type="button" {...fold.toggleProps} />}
      <div data-testid="deck-user-caption" className={`mb-1 deck-t-down font-medium ${c.caption}`}>{caption}</div>
      <div ref={fold?.bodyRef} id={fold?.bodyId} className={`whitespace-pre-wrap break-words deck-t-up ${c.body} ${bash ? 'font-mono' : ''} ${fold?.bodyClass ?? ''}`}>
        {bash ? `! ${item.text}` : OWN_TEXT_SOURCES.has(item.source) ? <UserText text={item.text} /> : <CjkText text={item.text} />}
        {item.truncated && <span className="text-text-muted">…</span>}
      </div>
      {item.images && item.images.length > 0 && (
        <div data-testid="deck-user-images" className="mt-1 deck-t-down text-text-muted">{t('deck.user.images', { n: item.images.length })}</div>
      )}
    </div>
  )
}
