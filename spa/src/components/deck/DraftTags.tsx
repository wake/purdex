// spa/src/components/deck/DraftTags.tsx — the `[Image #N]` placeholders of the draft drawn as tags (v3 pt 5). The box stays a plain
// <textarea> (typing, IME, selection, caret, undo and screen readers are the browser's own); this is a mirror BEHIND it with the
// same font, padding and wrapping whose text is transparent and whose only visible parts are the backgrounds of the placeholders.
// The mark takes no padding or margin (that would move the text off the real one), so the tag is a tinted, outlined box exactly
// around the characters. aria-hidden and pointer-events none: it is paint, nothing else.
import { useLayoutEffect, useRef, type RefObject } from 'react'
import { placeholderRanges } from '../../lib/conversations/image-placeholder'

interface Props {
  draft: string
  /** Image numbers whose placeholder is drawn as a tag (the chips that are alive). */
  seqs: ReadonlySet<number>
  /** The textarea this mirrors; its scroll position and scrollbar width are followed. */
  box: RefObject<HTMLTextAreaElement | null>
}

/** Parts of the draft: plain text and the placeholders. */
function pieces(draft: string, ranges: Array<{ start: number; end: number }>): Array<{ text: string; tag: boolean }> {
  const out: Array<{ text: string; tag: boolean }> = []
  let at = 0
  for (const r of ranges) {
    if (r.start > at) out.push({ text: draft.slice(at, r.start), tag: false })
    out.push({ text: draft.slice(r.start, r.end), tag: true })
    at = r.end
  }
  out.push({ text: draft.slice(at), tag: false })
  return out
}

export const DraftTags = ({ draft, seqs, box }: Props) => {
  const mirror = useRef<HTMLDivElement>(null)
  const ranges = placeholderRanges(draft, seqs)
  // the box may be narrower by a scrollbar, and may be scrolled: the mirror wraps and sits exactly as the box does
  useLayoutEffect(() => {
    const el = box.current, m = mirror.current
    if (!el || !m) return
    m.style.paddingRight = `${Math.max(0, el.offsetWidth - el.clientWidth)}px`
    m.scrollTop = el.scrollTop
  })
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    const follow = () => { if (mirror.current) mirror.current.scrollTop = el.scrollTop }
    el.addEventListener('scroll', follow)
    return () => el.removeEventListener('scroll', follow)
  }, [box])
  if (ranges.length === 0) return null
  return (
    <div
      ref={mirror}
      data-testid="draft-tags"
      aria-hidden="true"
      style={{ overflowWrap: 'anywhere' }}
      className="pointer-events-none absolute inset-0 overflow-hidden whitespace-pre-wrap pb-1.5 pt-0 text-[13px] leading-[1.65] text-transparent"
    >
      {pieces(draft, ranges).map((p, i) => (p.tag
        ? <mark key={i} className="rounded-[3px] bg-accent/20 text-transparent ring-1 ring-accent/50">{p.text}</mark>
        : <span key={i}>{p.text}</span>))}
      {'​'}
    </div>
  )
}
