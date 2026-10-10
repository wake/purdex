// spa/src/components/deck/ChatPeerLine.tsx — peer messages in the chat, as iOS 0.6.44 draws them: one collapsed line,
// 「↪ <sender>：<first 40 characters of the first line>…」 for one message, 「↪ N 則 peer 訊息」 for several (no sender
// list); a click opens one line per message with its sender, 「（未驗證）」 after an unverified one. The whole line is
// dimmed one step only when EVERY message is unverified. A plugin sender reads 「<name> plugin」. Whether it is open lives
// in the pane's fold memory (the chat unmounts with its tab).
import { useI18nStore } from '../../stores/useI18nStore'
import type { UserItem } from '../../lib/conversations/types'
import { useFold } from '../room/fold-context'

const PREVIEW_CHARS = 40

/** The first line cut to its first 40 characters (code points, so a surrogate pair is not split); `cut` when anything was left out. */
function peerPreview(text: string): { text: string; cut: boolean } {
  const lines = text.trim().split('\n')
  const chars = Array.from(lines[0].trim())
  return { text: chars.slice(0, PREVIEW_CHARS).join(''), cut: chars.length > PREVIEW_CHARS || lines.length > 1 }
}

export function PeerLine({ items }: { items: UserItem[] }) {
  const t = useI18nStore((s) => s.t)
  const [open, toggle] = useFold(`${items[0].id}:peer`)
  const sender = (i: UserItem) => (i.from?.kind === 'plugin' ? t('chat.peer.plugin', { name: i.from.name ?? '' }) : (i.from?.name ?? i.from?.kind ?? ''))
  const dim = items.every((i) => i.from?.unverified === true)
  const flag = (i: UserItem) => (i.from?.unverified === true ? t('chat.peer.unverified') : '')
  const one = items.length === 1 ? peerPreview(items[0].text) : null
  const head = one
    ? t('chat.peer.single', { name: sender(items[0]), flag: flag(items[0]), text: one.text, more: one.cut ? '…' : '' })
    : t('chat.peer.many', { n: items.length })
  return (
    <div data-testid="chat-peer" data-count={items.length} className={`text-xs text-text-muted ${dim ? 'opacity-60' : ''}`}>
      <button type="button" data-testid="chat-peer-head" aria-expanded={open} onClick={toggle} className="block w-full cursor-pointer truncate text-left hover:text-text-primary">
        {head}
      </button>
      {open && (
        <div className="mt-1 space-y-1 border-l border-border-subtle pl-3">
          {items.map((i) => (
            <div key={i.id} data-testid="chat-peer-item" className="whitespace-pre-wrap break-words text-text-secondary">
              {t('chat.peer.item', { sender: sender(i), flag: flag(i), text: i.text })}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
