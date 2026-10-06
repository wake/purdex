// spa/src/components/room/prelude/PreludeSegment.tsx — one run of the
// prelude whose lines share an attribution (conversation entity spec §10.3):
// the lines an earlier worker stint wrote, or a plain transcript segment.
// A Fragment, never a box (see PreludeSection's header): it draws its entries
// (room) or blocks (chat) with the section's own ctx and span operations,
// exactly as the section drew them before it was cut into runs.
// A worker stint's segment is enriched from that stint's own events (§10.4):
// once they are in, its calls show the stint's real status and duration (and
// chat re-sorts its spans' operations by them), its Task calls their subagent
// task rows, and past the event budget one muted line says so. Each prompt span
// (the spans chat draws, restricted to the run, in both modes) also gets its
// turn's cost footer, found by the assistant message ids the span holds.
// Until then, or when the fetch fails, it is drawn exactly as a plain one.
import { Fragment, useMemo, type ReactElement, type ReactNode } from 'react'
import type { TurnCost } from '../../../lib/nex/cost-summary'
import { useI18nStore } from '../../../stores/useI18nStore'
import { classifyTurnOperations, type TurnOperation } from '../../../lib/nex/operation-status'
import { chatToolsKey } from '../../../lib/nex/transcript-search'
import type { PreludeBlock, PreludeEntry, PreludeView } from '../../../lib/nex/prelude'
import { preludeBlocks, preludeId } from '../../../lib/nex/prelude'
import { isWorkerEntrypoint } from '../../../lib/nex/stint-attribution'
import { costSummary } from '../../../lib/nex/cost-summary'
import { costIncludesPriorHistory } from '../../../lib/nex/prior-history'
import { ENRICHMENT_EVENT_BUDGET } from '../../../lib/nex/stint-enrichment'
import type { ExecutionSummary } from '../../../lib/nex/types'
import { useStintEnrichment } from '../../../hooks/useStintEnrichment'
import { renderMessage, type RenderCtx } from '../render-message'
import ChatTurnBody from '../../chat/ChatTurnBody'
import PreludeMarker from './PreludeMarker'
import PreludeNote from './PreludeNote'
import PreludeCostFooter from './PreludeCostFooter'

/** What one segment draws: its entries (room), or its blocks and their operations (chat). */
export type PreludeSegmentSlice =
  | { mode: 'room'; entries: readonly PreludeEntry[] }
  | {
      mode: 'chat'
      blocks: readonly PreludeBlock[]
      /** Index-aligned with `blocks`: each span's operations. */
      spanOps: readonly TurnOperation[][]
      /** The transcript's keyPrefix. */
      keyPrefix: string
      /** Each message's entry pos, by its index in `view.messages`. */
      posOf: readonly string[]
    }

export type PreludeSegmentProps = {
  /** The pane's host: where `stintId`'s events are fetched from. */
  hostId: string
  /** The earlier worker stint that wrote these lines; null = a plain transcript segment. */
  stintId: string | null
  /** That stint's listed row (its prior-history hint); null for a plain segment. */
  summary: ExecutionSummary | null
  view: PreludeView
  /** The section's: its index spans the whole prelude (a Task's subagent frames may sit in another run). */
  ctx: RenderCtx
} & PreludeSegmentSlice

export default function PreludeSegment(props: PreludeSegmentProps) {
  const t = useI18nStore((s) => s.t)
  const { view } = props
  // null (failed) draws like undefined (loading, or a plain segment): the transcript's own.
  const enrichment = useStintEnrichment(props.hostId, props.stintId) ?? null
  // The stint's entry wins by tool_use_id (own keys both sides: spread never touches the prototype).
  const tools = useMemo(() => (enrichment ? { ...view.tools, ...enrichment.tools } : null), [view.tools, enrichment])
  const chatBlocks = props.mode === 'chat' ? props.blocks : null
  const { index, idOf } = props.ctx
  // Chat draws a call's status from its span's operations, not from ctx.tools: re-sort them by the merged tools.
  const enrichedOps = useMemo(
    () => (chatBlocks && tools
      ? chatBlocks.map((b) => (b.kind === 'span' ? classifyTurnOperations(view.messages, b, index, tools, idOf) : []))
      : null),
    [chatBlocks, tools, view.messages, index, idOf],
  )
  // Not enriched: the section's very own ctx object.
  const ctx: RenderCtx = enrichment && tools ? { ...props.ctx, tools, subagentTasks: enrichment.subagentTasks } : props.ctx
  // The footer under each prompt span, by the span's last message index: a turn's cost sits once, under the last span holding one of its lines.
  const entries = props.mode === 'room' ? props.entries : null
  const footerAt = useMemo(() => {
    const out = new Map<number, ReactElement>()
    if (!enrichment) return out
    const spans: number[][] = []
    if (chatBlocks) {
      for (const b of chatBlocks) if (b.kind === 'span') spans.push(Array.from({ length: b.end - b.start }, (_, i) => b.start + i))
    } else if (entries) {
      const inRun = new Set<number>()
      for (const e of entries) if (e.kind === 'message') inRun.add(e.m)
      for (const b of preludeBlocks(view)) {
        if (b.kind !== 'span') continue
        const held = Array.from({ length: b.end - b.start }, (_, i) => b.start + i).filter((i) => inRun.has(i))
        if (held.length > 0) spans.push(held)
      }
    }
    const lastSpanOf = new Map<TurnCost, number>()
    for (const held of spans) {
      // The turn of the span's last mapped assistant line.
      for (let k = held.length - 1; k >= 0; k--) {
        const msg = view.messages[held[k]] as { type?: unknown; parent_tool_use_id?: unknown; message?: { id?: unknown } }
        const id = msg.type === 'assistant' && msg.parent_tool_use_id == null ? msg.message?.id : undefined
        const turn = typeof id === 'string' ? enrichment.costByMessageId.get(id) : undefined
        if (turn) { lastSpanOf.set(turn, held[held.length - 1]); break }
      }
    }
    if (lastSpanOf.size === 0) return out
    const prior = costIncludesPriorHistory(props.summary, costSummary(enrichment.messages))
    for (const [turn, last] of lastSpanOf) {
      out.set(last, <PreludeCostFooter turn={turn} title={prior && turn.index === 1 ? t('execution.cost.includesPriorHistory') : undefined} />)
    }
    return out
  }, [enrichment, chatBlocks, entries, view, props.summary, t])
  const budgetLine = enrichment?.truncated
    ? <div data-testid="prelude-enrichment-truncated" className="text-xs text-text-muted">{t('worker.prelude.enrichment_truncated', { n: ENRICHMENT_EVENT_BUDGET })}</div>
    : null

  const entryNode = (e: Exclude<PreludeEntry, { kind: 'message' }>): ReactNode => {
    const id = preludeId(e.pos)
    if (e.kind === 'segment') {
      const label = e.entrypoint === 'cli' ? t('worker.prelude.segment_cli')
        : isWorkerEntrypoint(e.entrypoint) ? t('worker.prelude.segment_headless')
        : e.entrypoint
      return <PreludeMarker key={id} testId="prelude-segment" label={label} pos={e.pos} />
    }
    if (e.kind === 'compaction') {
      const label = e.trigger === 'auto' ? t('worker.prelude.compaction_auto')
        : e.trigger === 'manual' ? t('worker.prelude.compaction_manual')
        : t('worker.prelude.compaction')
      return <PreludeMarker key={id} testId="prelude-compaction" label={label} pos={e.pos} />
    }
    return <PreludeNote key={id} id={id} pos={e.pos} source={e.source} text={e.text} truncated={e.truncated} totalBytes={e.totalBytes} stream={e.stream} />
  }

  if (props.mode === 'chat') {
    const { blocks, keyPrefix, posOf } = props
    const spanOps = enrichedOps ?? props.spanOps
    return (
      <>
        {blocks.map((b, bi) => b.kind === 'entry'
          ? entryNode(b.entry)
          : (
            <ChatTurnBody key={`${keyPrefix}-prelude-span-${view.ids[b.end - 1]}`} messages={view.messages} turn={b}
              ops={spanOps[bi]} ctx={ctx} toolsKey={chatToolsKey(`${keyPrefix}-prelude`, view.ids[b.end - 1])}
              interrupted={t('stream.interrupted')} preludePoses={posOf.slice(b.start, b.end)} footer={footerAt.get(b.end - 1)} />
          ))}
        {budgetLine}
      </>
    )
  }
  // A footer is a sibling right after its span's last row (the row's own key is untouched).
  const rows: ReactNode[] = []
  for (const e of props.entries) {
    if (e.kind !== 'message') { rows.push(entryNode(e)); continue }
    rows.push(ctx.index.childIndexes.has(e.m) ? null : renderMessage(view.messages[e.m], e.m, ctx, e.pos))
    const footer = footerAt.get(e.m)
    if (footer) rows.push(<Fragment key={`cost-${e.pos}`}>{footer}</Fragment>)
  }
  return (
    <>
      {rows}
      {budgetLine}
    </>
  )
}
