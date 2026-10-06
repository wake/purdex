// spa/src/components/room/prelude/PreludeSection.tsx — the conversation
// before this worker's first turn (worker prelude spec §5.3), drawn above
// turn 1 with the room's renderer or, in chat mode, as spans of ChatTurnBody.
// Its messages are named by stable ids (`p<pos>`), so loading an older page
// never re-keys what is on screen; a chat span is keyed by its LAST message
// (pages only grow at the front, so a span's end never moves). It
// is not a RoomTurnGroup: no data-turn-index (the scroll memory's first
// turn stays the worker's), no hover strip. Instead every drawn row, span,
// note and marker carries the pos it starts at, `data-prelude-pos`, and a
// chat span every pos it holds, `data-prelude-poses` — the scroll memory's
// anchor inside the prelude (#1534). On the element's own root: a wrapper
// would be one more box (and margin) above turn 1.
// The rows are drawn in runs of one attribution (conversation entity spec
// §10.3: the earlier worker stint that wrote them, or none), a PreludeSegment
// each, keyed like a span by its LAST pos. A segment is a Fragment, never a
// wrapper element: the section's `space-y-4` spaces its direct children, the
// scroll anchor reads `data-prelude-pos` off each row's own root, and a box
// would add one more above turn 1. So attribution never changes the DOM.
import { useCallback, useMemo, type ReactNode } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { indexOperations } from '../../../lib/nex/operations'
import { classifyTurnOperations } from '../../../lib/nex/operation-status'
import type { PreludeBlock, PreludeEntry, PreludeState, PreludeView } from '../../../lib/nex/prelude'
import { preludeBlocks } from '../../../lib/nex/prelude'
import type { Stint } from '../../../lib/nex/entity-stints'
import { attributionRuns, NO_ATTRIBUTION, type Attribution, type PreludeRun } from '../../../lib/nex/stint-attribution'
import type { RenderCtx } from '../render-message'
import { FoldContext, useInheritedFoldMemory } from '../fold-context'
import PreludeMarker from './PreludeMarker'
import PreludeSegment, { type PreludeSegmentSlice } from './PreludeSegment'
import PreludeSentinel from './PreludeSentinel'

export interface PreludeSectionProps {
  /** The pane's host: an attributed segment fetches its stint's events from it (spec §10.4). */
  hostId: string
  view: PreludeView
  status: PreludeState['status']
  done: boolean
  error: string | null
  /** The transcript's keyPrefix; the section keys its rows under `${keyPrefix}-prelude`. */
  keyPrefix: string
  now?: number
  /** 'chat' groups the messages into spans drawn by ChatTurnBody (spec §5.3). */
  mode: 'room' | 'chat'
  /** PreludeState.pages — re-arms the sentinel after every page, an empty one included. */
  pages: number
  onLoadOlder: () => void
  onRetry: () => void
  /** Which earlier worker stint wrote each line, by pos (conversation entity spec §10.3); absent = none. */
  attribution?: Attribution
  /** The conversation's earlier stints: an attributed segment reads its stint's summary from here. */
  stints?: readonly Stint[]
}

const entryPoses = (e: PreludeEntry): [string, string] => [e.pos, e.pos]

export default function PreludeSection({ hostId, view, status, done, error, keyPrefix, now, mode, pages, onLoadOlder, onRetry, attribution = NO_ATTRIBUTION, stints }: PreludeSectionProps) {
  const t = useI18nStore((s) => s.t)
  // Inherit the transcript's fold memory; a section mounted alone still folds.
  const folds = useInheritedFoldMemory()
  const idOf = useCallback((i: number) => view.ids[i], [view.ids])
  const index = useMemo(() => indexOperations(view.messages, idOf), [view.messages, idOf])
  // Each message's entry pos, by its index in `view.messages`.
  const posOf = useMemo(() => {
    const out: string[] = []
    for (const e of view.entries) if (e.kind === 'message') out[e.m] = e.pos
    return out
  }, [view.entries])
  // Chat's spans and each span's operations (hooks stay above the early return).
  const blocks = useMemo(() => (mode === 'chat' ? preludeBlocks(view) : []), [mode, view])
  const spanOps = useMemo(
    () => blocks.map((b) => (b.kind === 'span' ? classifyTurnOperations(view.messages, b, index, view.tools, idOf) : [])),
    [blocks, view, index, idOf],
  )
  // Room: runs of entries; chat: runs of blocks, a span by its first and last message.
  // A worker → worker rebuild starts at its first send, an opening line even when it carries only
  // attachments (#1614, fixed), so a span splits at the boundary and the new stint's lines run under it.
  // Spans stay whole (ChatTurnBody grouping, search's blocks): a span that still straddles a boundary (one at
  // a non-opening line) runs under its first line's stint; enrichment joins by unique id, so its tail loses it, never mismatches.
  // The tail holds no opening line (one would have split the span), so the thumbnails' prompt pairing never counts it.
  // Each run carries its slice, kept across renders: an enriched chat segment memoizes its spans' operations on it.
  const runs = useMemo((): Array<PreludeRun & { slice: PreludeSegmentSlice }> => {
    if (mode !== 'chat') {
      return attributionRuns(view.entries, entryPoses, attribution)
        .map((r) => ({ ...r, slice: { mode: 'room', entries: view.entries.slice(r.start, r.end) } }))
    }
    const blockPoses = (b: PreludeBlock): [string, string] => (b.kind === 'span' ? [posOf[b.start], posOf[b.end - 1]] : entryPoses(b.entry))
    return attributionRuns(blocks, blockPoses, attribution).map((r) => ({
      ...r, slice: { mode: 'chat', blocks: blocks.slice(r.start, r.end), spanOps: spanOps.slice(r.start, r.end), keyPrefix, posOf },
    }))
  }, [mode, view.entries, blocks, spanOps, keyPrefix, posOf, attribution])
  if (view.entries.length === 0 && (status === 'idle' || status === 'none')) return null
  const ctx: RenderCtx = { messages: view.messages, index, tools: view.tools, now, keyPrefix: `${keyPrefix}-prelude`, depth: 0, idOf }

  const top: ReactNode =
    status === 'loading' ? <div data-testid="prelude-loading" className="text-xs text-text-muted text-center">{t('worker.prelude.loading')}</div>
    : status === 'error' ? (
      <div data-testid="prelude-error" className="flex items-center justify-center gap-2 text-xs text-status-error">
        <span>{t('worker.prelude.error', { message: error ?? '' })}</span>
        <button type="button" onClick={onRetry} className="underline hover:text-text-primary">{t('worker.prelude.retry')}</button>
      </div>
    )
    : status === 'gone' ? <div data-testid="prelude-gone" className="text-xs text-text-muted text-center">{t('worker.prelude.gone')}</div>
    : null

  return (
    <FoldContext.Provider value={folds}>
      <section data-testid="worker-prelude" className="space-y-4">
        {status === 'ok' && !done && <PreludeSentinel onVisible={onLoadOlder} generation={pages} />}
        {top}
        {runs.map((r) => <PreludeSegment key={r.key} hostId={hostId} stintId={r.stintId} summary={stints?.find((x) => x.id === r.stintId)?.summary ?? null} view={view} ctx={ctx} {...r.slice} />)}
        {/* The handoff into this worker is itself a switch (D2) and Nexen never sends a segment for the worker's own run. */}
        {view.entries.length > 0 && <PreludeMarker testId="prelude-handoff" label={t('worker.prelude.segment_headless')} />}
      </section>
    </FoldContext.Provider>
  )
}
