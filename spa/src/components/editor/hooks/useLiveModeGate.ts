// spa/src/components/editor/hooks/useLiveModeGate.ts
//
// Spec 2.3: the safety gate in front of Live Mode. Tiptap models a lossy subset
// of markdown and the loss happens at PARSE time — a construct it cannot
// represent is already gone before the first keystroke, and the next save writes
// the degraded document back over the original file. So a markdown file that
// cannot round-trip losslessly opens in raw mode instead, with the reason stated.
import { useMemo } from 'react'
import { assessMarkdownRoundTrip } from '../../../lib/markdown/round-trip-safety'

/** Blocker keys the UI has its own copy for; everything else uses the fallback. */
const NAMED_BLOCKERS = new Set(['html', 'frontmatter', 'footnote', 'image-in-mark', 'bracketed-url'])

/**
 * `assessMarkdownRoundTrip` is default-deny: an unrecognised token contributes
 * its own type as the blocker key, so the set of keys is open-ended by design
 * and a message must exist for keys we have never seen.
 */
function describeBlockers(blockers: string[], t: (key: string, params?: Record<string, string>) => string): string {
  const reasons = blockers.map((blocker) =>
    NAMED_BLOCKERS.has(blocker)
      ? t(`editor.live_mode.blocker.${blocker}`)
      : t('editor.live_mode.blocker.unknown', { blocker }),
  )
  return t('editor.live_mode.blocked', { reasons: reasons.join(', ') })
}

export interface LiveModeGate {
  /** True when the content cannot round-trip, so Live Mode must not be the default. */
  forcesRaw: boolean
  /** Human-readable reason, present only when `forcesRaw` is true. */
  reason?: string
}

/**
 * The gate is a question about the FILE — "can what is on disk survive a Live
 * Mode round trip?" — so it is evaluated against `savedContent`, never the live
 * buffer. Evaluating the draft made the verdict flip mid-edit: deleting the
 * front matter of a raw-opened file flipped it to "safe" on that keystroke,
 * swapped Monaco for Tiptap under the user's cursor and lost the cursor and
 * scroll position. `savedContent` only moves at load (`openBuffer`), save
 * (`markSaved`) and external reload (`reloadBuffer`) — the exact moments where
 * re-deciding is what the user expects. An untitled buffer starts empty, which
 * is trivially safe, so new markdown still opens in Live Mode.
 *
 * The verdict is memoized on that text: it is a pure function of it, so lexing
 * again on every render would make typing quadratic for no gain. (The buffer key
 * is deliberately NOT a dependency — it carries no information the content does
 * not, and `react-hooks/exhaustive-deps` rejects it as an unnecessary
 * dependency.)
 *
 * A throwing lexer must not take the whole pane down, and it must not be read as
 * "safe" either: the failure is treated as unsafe, which opens the file raw —
 * the outcome that cannot lose data.
 */
export function useLiveModeGate(
  savedContent: string | undefined,
  isMarkdown: boolean,
  t: (key: string, params?: Record<string, string>) => string,
): LiveModeGate {
  const verdict = useMemo(() => {
    if (!isMarkdown || savedContent === undefined) return null
    try {
      return assessMarkdownRoundTrip(savedContent)
    } catch {
      return { safe: false, blockers: ['parse-error'] }
    }
  }, [savedContent, isMarkdown])

  return useMemo(() => {
    if (!verdict || verdict.safe) return { forcesRaw: false }
    return { forcesRaw: true, reason: describeBlockers(verdict.blockers, t) }
  }, [verdict, t])
}
