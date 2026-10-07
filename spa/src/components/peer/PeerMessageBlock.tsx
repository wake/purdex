// spa/src/components/peer/PeerMessageBlock.tsx — another conversation's message
// (peer mailbox spec §7, design mock `ublock peer`): a left rule in the info
// colour, 「來自 <from_name> · <time>」 above, the body drawn like agent prose.
// Never the user's band or bubble — a peer turn must read as a peer message.
//
// Plain props and no execution state, so the terminal underlying can draw its
// native peer messages with the same block (spec §7, §9). The colour is the pane
// theme's `--wt-peer-color`.
import { useI18nStore } from '../../stores/useI18nStore'
import RoomProse from '../room/RoomProse'

export interface PeerMessageBlockProps {
  /** The sender's address as delivered (`mlab/purdex-54`). */
  fromName: string
  /** The peer's text, drawn as markdown. */
  text: string
  /** When it arrived (epoch ms); 0 = unknown, and the header shows no time. */
  at: number
  /** The search anchor of the body (`data-search-unit`). */
  searchUnit?: string
  /** Formats `at` in the header (system 12/24h, like the turn footer); injectable for tests. */
  timeFormat?: Intl.DateTimeFormat
  /** Extra classes on the block (chat caps its width like a bubble's). */
  className?: string
}

const DEFAULT_TIME_FORMAT = new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' })
const TITLE_FORMAT = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' })

export default function PeerMessageBlock({ fromName, text, at, searchUnit, timeFormat = DEFAULT_TIME_FORMAT, className }: PeerMessageBlockProps) {
  const t = useI18nStore((s) => s.t)
  const timed = Number.isFinite(at) && at > 0
  const header = timed
    ? t('peer.message.from', { name: fromName, time: timeFormat.format(new Date(at)) })
    : t('peer.message.from_untimed', { name: fromName })
  return (
    <div
      data-testid="peer-message"
      className={`min-w-0 rounded-l-[2px] rounded-r-lg border border-border-subtle border-l-[3px] border-l-[var(--wt-peer-color)] px-[11px] py-[7px]${className ? ` ${className}` : ''}`}
    >
      <div
        data-testid="peer-message-header"
        title={timed ? TITLE_FORMAT.format(new Date(at)) : undefined}
        className="mb-px text-xs font-bold text-[var(--wt-peer-color)] break-words"
      >
        {header}
      </div>
      <RoomProse content={text} searchUnit={searchUnit} />
    </div>
  )
}
