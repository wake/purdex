// spa/src/components/room/WorkingLine.tsx — the room's thinking indicator:
// a terminal-style text line `✻ Whirlpooling… (1m 40s)` in place of chat's
// three dots. Visibility is decided by the caller (the same gate that shows
// the dots); only the rendering differs. The glyph cycles a few frames unless
// the reader prefers reduced motion; the verb is fixed per turn; the elapsed
// time counts from `startedAt` (the send / the open turn's start), or from the
// moment the line appeared when that is not known.
// Output-token count (`· ↓ 7.5k tokens`) is NOT drawn: a live turn carries no
// token count in the events this pane receives (usage arrives only with the
// turn's final `result`), and an estimate would be an invention.
import { useEffect, useState } from 'react'
import { useElapsedTicker } from '../../hooks/useElapsedTicker'
import { pickWorkingVerb, formatWorkingElapsed, WORKING_FRAMES } from '../../lib/nex/working-line'

export const WORKING_FRAME_MS = 250
const STILL_FRAME = WORKING_FRAMES.indexOf('✻')

interface Props {
  visible: boolean
  /** Stable per turn; the verb is a function of it. */
  turnKey: string
  /** ms epoch the turn (or the send) started; absent → counted from when the line appeared. */
  startedAt?: number
}

function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    && window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

export default function WorkingLine({ visible, turnKey, startedAt }: Props) {
  if (!visible) return null
  return <Line turnKey={turnKey} startedAt={startedAt} />
}

function Line({ turnKey, startedAt }: { turnKey: string; startedAt?: number }) {
  const now = useElapsedTicker(true)
  const [appeared] = useState(() => Date.now())
  // Reduced motion: no interval, so the glyph stays on the solid frame.
  const still = prefersReducedMotion()
  const [frame, setFrame] = useState(still ? STILL_FRAME : 0)
  useEffect(() => {
    if (still) return
    const id = setInterval(() => setFrame((f) => (f + 1) % WORKING_FRAMES.length), WORKING_FRAME_MS)
    return () => clearInterval(id)
  }, [still])
  const verb = pickWorkingVerb(turnKey)
  const since = startedAt && startedAt > 0 ? startedAt : appeared
  return (
    <div data-testid="thinking-indicator" data-mode="room" role="status" className="text-sm text-text-muted font-mono">
      <span data-testid="working-glyph" aria-hidden="true" className="inline-block w-4 text-center text-accent-base">
        {WORKING_FRAMES[frame]}
      </span>
      <span data-testid="working-verb">{verb}…</span>{' '}
      <span data-testid="working-elapsed" className="tabular-nums">({formatWorkingElapsed(now - since)})</span>
    </div>
  )
}
