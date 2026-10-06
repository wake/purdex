// spa/src/components/room/prelude/PreludeSegment.tsx — one run of the
// prelude whose lines share an attribution (conversation entity spec §10.3):
// the lines an earlier worker stint wrote, or a plain transcript segment.
// A Fragment, never a box (see PreludeSection's header): it draws its entries
// (room) or blocks (chat) with the section's own ctx and span operations,
// exactly as the section drew them before it was cut into runs.
// A worker stint's segment is enriched from that stint's own events (§10.4):
// once they are in, its calls show the stint's real status and duration (and
// chat re-sorts its spans' operations by them), its Task calls their subagent
// task rows, and past the event budget one muted line says so. Until then, or
// when the fetch fails, it is drawn exactly as a plain one.
import { useMemo, type ReactNode } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { classifyTurnOperations, type TurnOperation } from '../../../lib/nex/operation-status'
import { chatToolsKey } from '../../../lib/nex/transcript-search'
import type { PreludeBlock, PreludeEntry, PreludeView } from '../../../lib/nex/prelude'
import { preludeId } from '../../../lib/nex/prelude'
import { isWorkerEntrypoint } from '../../../lib/nex/stint-attribution'
import { ENRICHMENT_EVENT_BUDGET } from '../../../lib/nex/stint-enrichment'
import { useStintEnrichment } from '../../../hooks/useStintEnrichment'
import { renderMessage, type RenderCtx } from '../render-message'
import ChatTurnBody from '../../chat/ChatTurnBody'
import PreludeMarker from './PreludeMarker'
import PreludeNote from './PreludeNote'

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
              interrupted={t('stream.interrupted')} preludePoses={posOf.slice(b.start, b.end)} />
          ))}
        {budgetLine}
      </>
    )
  }
  return (
    <>
      {props.entries.map((e) => (e.kind === 'message'
        ? (ctx.index.childIndexes.has(e.m) ? null : renderMessage(view.messages[e.m], e.m, ctx, e.pos))
        : entryNode(e)))}
      {budgetLine}
    </>
  )
}
