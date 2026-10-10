// spa/src/components/status/StatusSegments.tsx — the building blocks both status bars share: the row's three
// containers, the click-to-copy segment, the separator rule, and the host segment (the tmux bar and the worker bar,
// shell cleanup spec §9.2).
import { useEffect, useRef, type ReactNode } from 'react'
import { useHostLook } from '../../lib/host-look'
import { keepFocus } from '../../lib/keep-focus'
import { useI18nStore } from '../../stores/useI18nStore'

/**
 * How long a segment that also has a double-click gesture waits before copying.
 *
 * A browser dispatches two `click`s *before* `dblclick`, so an element carrying
 * both gestures runs the single-click action twice on the way to the double one.
 * The host segment carries both — click copies the host name, double-click opens
 * host settings, which it did before this feature existed — and the collision is
 * silent: the user navigates and finds the clipboard overwritten. So the copy is
 * deferred by one double-click window and cancelled if `dblclick` arrives.
 *
 * The alternative was to separate the gestures, which means taking one of them
 * off the segment: the double-click is muscle memory that predates this feature,
 * and the single click is what §4.2 promises for every segment. A quarter-second
 * on a clipboard write whose confirmation lands in a fixed slot anyway is the
 * cheaper side of that trade; the delay applies only to segments that have a
 * second gesture, which today is the host alone.
 */
const DOUBLE_CLICK_GRACE_MS = 250

/**
 * A rule between two segments.
 *
 * A `|` glyph would be selected and copied along with the text the user is
 * trying to grab, so the separator carries no text at all (spec §4.1). It
 * takes the drop class of the segment it introduces, or the row would keep a
 * dangling rule where a dropped segment used to be.
 */
export function Separator({ className = '' }: { className?: string }) {
  return <span aria-hidden="true" data-testid="status-separator" className={`mx-1 h-3 shrink-0 self-center border-l border-border-subtle ${className}`} />
}

/**
 * One click-to-copy segment.
 *
 * A `<button>`, not a `<span>` with a handler: the status bar had no keyboard
 * path at all, and these are the first things in it worth reaching. The
 * displayed text and the copied value differ for the peer id — see
 * `peerIdText`.
 */
export function CopySegment({ testId, display, value, what, title, dim, rtl, className = '', onCopy, onDoubleClick }: {
  testId: string
  /** What the row shows; '—' stands in for a value that is not there. */
  display: string
  /** What a click puts on the clipboard. Empty disables the button. */
  value: string
  /** The segment's name, already translated, for the confirmation message. */
  what: string
  title?: string
  dim?: boolean
  /** Truncate from the left instead of the right (paths: the tail informs). */
  rtl?: boolean
  className?: string
  onCopy: (what: string, value: string) => void
  /** The host segment keeps its pre-existing double-click to host settings. */
  onDoubleClick?: () => void
}) {
  // Only set while a copy is waiting out the double-click window; see
  // DOUBLE_CLICK_GRACE_MS. A segment without a second gesture copies at once.
  const pendingCopy = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (pendingCopy.current) clearTimeout(pendingCopy.current) }, [])

  const handleClick = () => {
    if (!onDoubleClick) {
      onCopy(what, value)
      return
    }
    if (pendingCopy.current) clearTimeout(pendingCopy.current)
    pendingCopy.current = setTimeout(() => {
      pendingCopy.current = null
      onCopy(what, value)
    }, DOUBLE_CLICK_GRACE_MS)
  }

  const handleDoubleClick = () => {
    if (pendingCopy.current) {
      clearTimeout(pendingCopy.current)
      pendingCopy.current = null
    }
    onDoubleClick?.()
  }

  return (
    <button
      type="button"
      data-testid={testId}
      data-dim={dim ? 'true' : undefined}
      disabled={value === ''}
      title={title}
      // A mouse press leaves focus on the pane (shell polish spec §4); click and double-click still fire.
      onMouseDown={keepFocus}
      onClick={handleClick}
      onDoubleClick={onDoubleClick ? handleDoubleClick : undefined}
      // `bdi` keeps the path itself left-to-right inside an RTL box, so the
      // ellipsis lands at the start without reordering the text.
      style={rtl ? { direction: 'rtl', textAlign: 'left' } : undefined}
      // `text-text-secondary` is on the base, not left to each call site: the
      // row's container is `text-text-muted`, so a segment that forgets the
      // class inherits a dimmer colour than the host and session name beside
      // it — which is exactly what shipped in alpha.365 and read as three
      // greyed-out segments next to two normal ones.
      //
      // `dim` no longer changes the text at all. Two rounds of trying to make
      // "uncertain" a *brightness* landed on the same complaint both times:
      // the row reads as one line, and a segment that is darker than its
      // neighbours looks broken rather than provisional. The uncertainty is
      // signalled beside the value instead (the refresh control), where it
      // costs no legibility; `data-dim` stays as the tested state.
      className={`min-w-0 truncate text-left text-text-secondary ${value === '' ? 'cursor-default' : 'cursor-pointer'} ${className}`}
    >
      {rtl ? <bdi>{display}</bdi> : display}
    </button>
  )
}

/**
 * The host segment, the first segment of both bars: the host's display name, click to copy it, double-click to open
 * the host's settings.
 */
export function HostSegment({ hostId, onCopy, onNavigateToHost }: {
  /** The host the bar's pane lives on; null (or '') when there is none to name or navigate to. */
  hostId: string | null
  onCopy: (what: string, value: string) => void
  onNavigateToHost?: (hostId: string) => void
}) {
  const t = useI18nStore((s) => s.t)
  const hostName = useHostLook(hostId || null).name ?? 'Unknown'
  return (
    <CopySegment
      testId="status-seg-host"
      display={hostName}
      value={hostName}
      what={t('peer.label.host')}
      title={`${t('peer.copy_hint')} · ${t('status.open_host_hint')}`}
      className="max-[500px]:max-w-[8ch] text-text-secondary"
      onCopy={onCopy}
      onDoubleClick={hostId ? () => onNavigateToHost?.(hostId) : undefined}
    />
  )
}

/**
 * The row's three containers, the same for every bar: the segments (everything in there may truncate), the slack
 * with the copy confirmation at its start, and the controls. `ml-auto` lives on the controls and nowhere else.
 */
export function StatusBarLayout({ segments, controls, copyFeedback, copyFailed }: {
  segments: ReactNode
  controls?: ReactNode
  /** The confirmation text from `useCopyFeedback`; '' leaves the slot empty. */
  copyFeedback: string
  copyFailed: boolean
}) {
  return (
    <div data-testid="status-bar" className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted flex-shrink-0 relative z-10">
      {/* Left: the segment group. Everything in here may truncate. */}
      <div data-testid="status-segments" className="flex min-w-0 items-center">
        {segments}
      </div>

      {/* Middle: the slack. The feedback slot sits at its start with a fixed
          width, so a copy confirmation never moves anything. */}
      <div className="flex min-w-0 flex-1 items-center">
        <span
          data-testid="status-copy-feedback"
          aria-live="polite"
          className={`ml-2 w-[14ch] shrink-0 truncate ${copyFailed ? 'text-status-error' : 'text-text-muted'}`}
        >
          {copyFeedback}
        </span>
      </div>

      {/* Right: the controls. `ml-auto` lives here and nowhere else. */}
      <div data-testid="status-controls" className="ml-auto flex shrink-0 items-center gap-3">
        {controls}
      </div>
    </div>
  )
}
