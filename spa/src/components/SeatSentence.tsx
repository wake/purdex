// spa/src/components/SeatSentence.tsx — a person inside a sentence (name pool v2 spec §9.3): `[avatar] Name` where the text says who, the full address
// kept as the tooltip. A name that is no persona stays the plain text it was, so a conversation with no personas reads as it always did.
import { Avatar } from './Avatar'
import { useSeat } from '../lib/names/seat'
import { useHostLook } from '../lib/host-look'
import { resolveHostColors } from '../lib/host-color'

/** What a localized template is given for its `{{name}}` slot; <SeatSentence> puts the person there. */
export const SEAT_MARK = ''

interface InlineProps {
  /** The sender as the wire gives it: `<host>/<name>` or the bare name. It is the tooltip, and what `seatOf` resolves. */
  address: string
  /** What stands for the person when no persona matches. */
  fallback: string
  /** The host whose colour the initials sit on when the persona has no picture. */
  hostId: string | null
  size?: 14 | 16 | 18
  /** Show the address as this element's own tooltip (default). Off where the surrounding control already carries a fuller one. */
  titled?: boolean
}

/** `[avatar] Name` for a persona; the plain fallback text otherwise. */
export function SeatInline({ address, fallback, hostId, size = 14, titled = true }: InlineProps) {
  const seat = useSeat(address)
  const look = useHostLook(hostId)
  if (seat === null) return <>{fallback}</>
  const faceColor = resolveHostColors({ colors: look.colors, color: look.color }, 'terminal')?.main ?? '#6b7280'
  return (
    <span data-testid="seat-inline" data-seat={seat.code} title={titled ? address : undefined} className="inline-flex items-center gap-1 align-middle">
      <Avatar size={size} code={seat.code} name={seat.name} src={seat.avatar} fallbackColor={faceColor} decorative />
      <span>{seat.name}</span>
    </span>
  )
}

/** `template` is a translated string whose `{{name}}` was filled with SEAT_MARK: the text around it stays, the mark becomes the person. */
export function SeatSentence({ template, ...rest }: InlineProps & { template: string }) {
  const at = template.indexOf(SEAT_MARK)
  if (at < 0) return <>{template}</>
  return <>{template.slice(0, at)}<SeatInline {...rest} />{template.slice(at + SEAT_MARK.length)}</>
}

/** Whether a message's sender may be shown as a person: another session whose identity the transport established. A mod's notice (kind plugin) is no person, and an
 *  `unverified` sender is only what the message text says (the daemon's own caveat): its name is never resolved to a persona's face. */
export function isVerifiedPeer(item: { from?: { kind: string; unverified?: boolean } }): boolean {
  return item.from?.kind === 'peer' && item.from.unverified !== true
}
