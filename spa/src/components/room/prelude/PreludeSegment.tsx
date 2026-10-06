// spa/src/components/room/prelude/PreludeSegment.tsx — one run of the
// prelude whose lines share an attribution (conversation entity spec §10.3):
// the lines an earlier worker stint wrote, or a plain transcript segment.
// A Fragment, never a box (see PreludeSection's header): it draws its entries
// (room) or blocks (chat) with the section's own ctx and span operations,
// exactly as the section drew them before it was cut into runs.
import type { ReactNode } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { TurnOperation } from '../../../lib/nex/operation-status'
import { chatToolsKey } from '../../../lib/nex/transcript-search'
import type { PreludeBlock, PreludeEntry, PreludeView } from '../../../lib/nex/prelude'
import { preludeId } from '../../../lib/nex/prelude'
import { isWorkerEntrypoint } from '../../../lib/nex/stint-attribution'
import { renderMessage, type RenderCtx } from '../render-message'
import ChatTurnBody from '../../chat/ChatTurnBody'
import PreludeMarker from './PreludeMarker'
import PreludeNote from './PreludeNote'

export type PreludeSegmentProps = {
  /** The earlier worker stint that wrote these lines; null = a plain transcript segment. */
  stintId: string | null
  view: PreludeView
  /** The section's: its index spans the whole prelude (a Task's subagent frames may sit in another run). */
  ctx: RenderCtx
} & (
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
)

export default function PreludeSegment(props: PreludeSegmentProps) {
  const t = useI18nStore((s) => s.t)
  const { view, ctx } = props

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
    const { blocks, spanOps, keyPrefix, posOf } = props
    return (
      <>
        {blocks.map((b, bi) => b.kind === 'entry'
          ? entryNode(b.entry)
          : (
            <ChatTurnBody key={`${keyPrefix}-prelude-span-${view.ids[b.end - 1]}`} messages={view.messages} turn={b}
              ops={spanOps[bi]} ctx={ctx} toolsKey={chatToolsKey(`${keyPrefix}-prelude`, view.ids[b.end - 1])}
              interrupted={t('stream.interrupted')} preludePoses={posOf.slice(b.start, b.end)} />
          ))}
      </>
    )
  }
  return (
    <>
      {props.entries.map((e) => (e.kind === 'message'
        ? (ctx.index.childIndexes.has(e.m) ? null : renderMessage(view.messages[e.m], e.m, ctx, e.pos))
        : entryNode(e)))}
    </>
  )
}
