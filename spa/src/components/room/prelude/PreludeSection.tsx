// spa/src/components/room/prelude/PreludeSection.tsx — the conversation
// before this worker's first turn (worker prelude spec §5.3), drawn above
// turn 1 with the room's own renderer. Its messages are named by stable ids
// (`p<pos>`), so loading an older page never re-keys what is on screen. It
// is not a RoomTurnGroup: no data-turn-index (the scroll memory's first
// turn stays the worker's), no hover strip.
import { useCallback, useMemo, type ReactNode } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { indexOperations } from '../../../lib/nex/operations'
import { classifyTurnOperations } from '../../../lib/nex/operation-status'
import { chatToolsKey } from '../../../lib/nex/transcript-search'
import type { PreludeEntry, PreludeState, PreludeView } from '../../../lib/nex/prelude'
import { preludeBlocks, preludeId } from '../../../lib/nex/prelude'
import { renderMessage, type RenderCtx } from '../render-message'
import { FoldContext, useInheritedFoldMemory } from '../fold-context'
import ChatTurnBody from '../../chat/ChatTurnBody'
import PreludeMarker from './PreludeMarker'
import PreludeNote from './PreludeNote'
import PreludeSentinel from './PreludeSentinel'

export interface PreludeSectionProps {
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
}

export default function PreludeSection({ view, status, done, error, keyPrefix, now, mode, pages, onLoadOlder, onRetry }: PreludeSectionProps) {
  const t = useI18nStore((s) => s.t)
  // Inherit the transcript's fold memory; a section mounted alone still folds.
  const folds = useInheritedFoldMemory()
  const idOf = useCallback((i: number) => view.ids[i], [view.ids])
  const index = useMemo(() => indexOperations(view.messages, idOf), [view.messages, idOf])
  // Chat's spans and each span's operations (hooks stay above the early return).
  const blocks = useMemo(() => (mode === 'chat' ? preludeBlocks(view) : []), [mode, view])
  const spanOps = useMemo(
    () => blocks.map((b) => (b.kind === 'span' ? classifyTurnOperations(view.messages, b, index, view.tools, idOf) : [])),
    [blocks, view, index, idOf],
  )
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

  const entryNode = (e: Exclude<PreludeEntry, { kind: 'message' }>): ReactNode => {
    const id = preludeId(e.pos)
    if (e.kind === 'segment') {
      const label = e.entrypoint === 'cli' ? t('worker.prelude.segment_cli')
        : e.entrypoint.startsWith('sdk') ? t('worker.prelude.segment_headless')
        : e.entrypoint
      return <PreludeMarker key={id} testId="prelude-segment" label={label} />
    }
    if (e.kind === 'compaction') {
      const label = e.trigger === 'auto' ? t('worker.prelude.compaction_auto')
        : e.trigger === 'manual' ? t('worker.prelude.compaction_manual')
        : t('worker.prelude.compaction')
      return <PreludeMarker key={id} testId="prelude-compaction" label={label} />
    }
    return <PreludeNote key={id} id={id} source={e.source} text={e.text} truncated={e.truncated} totalBytes={e.totalBytes} stream={e.stream} />
  }

  return (
    <FoldContext.Provider value={folds}>
      <section data-testid="worker-prelude" className="space-y-4">
        {status === 'ok' && !done && <PreludeSentinel onVisible={onLoadOlder} generation={pages} />}
        {top}
        {mode === 'chat'
          ? blocks.map((b, bi) => b.kind === 'entry'
            ? entryNode(b.entry as Exclude<PreludeEntry, { kind: 'message' }>)
            : (
              <ChatTurnBody key={`${keyPrefix}-prelude-span-${view.ids[b.start]}`} messages={view.messages} turn={b}
                ops={spanOps[bi]} ctx={ctx} toolsKey={chatToolsKey(`${keyPrefix}-prelude`, view.ids[b.start])}
                interrupted={t('stream.interrupted')} />
            ))
          : view.entries.map((e) => (e.kind === 'message'
            ? (index.childIndexes.has(e.m) ? null : renderMessage(view.messages[e.m], e.m, ctx))
            : entryNode(e)))}
      </section>
    </FoldContext.Provider>
  )
}
