// spa/src/components/chat/ChatToolsLine.tsx — a turn's ordinary tools as one
// quiet line (spec §5, R2 plan T2.1): `Used 5 tools`, left side, no bubble.
// Expanded, it shows those operations as the room draws them, in place, so
// nothing is unreachable from chat. While any of them runs (or a call is still
// streaming its input) it reads `Using N tools…` — that line is chat's
// activity signal for tool work, so the pane's dots stay off meanwhile.
//
// Its expansion lives in the pane's fold memory (`useFold`), keyed per turn
// by the caller, so it registers with the turn and expand-all reaches it.
import type { ReactNode } from 'react'
import { CaretDown, CaretRight, Wrench } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useFold } from '../room/fold-context'

export interface ChatToolsLineProps {
  /** `${keyPrefix}-turn-${turnIndex}:chat-tools`. */
  foldKey: string
  /** Every plain operation of the turn, streaming calls included. */
  count: number
  /** Some of them are still at work. */
  running: boolean
  /** The room's blocks for those operations; only called when expanded. */
  renderOperations: () => ReactNode
}

export default function ChatToolsLine({ foldKey, count, running, renderOperations }: ChatToolsLineProps) {
  const t = useI18nStore((s) => s.t)
  const [expanded, toggle] = useFold(foldKey)
  const Caret = expanded ? CaretDown : CaretRight
  // The i18n store has no plural rules; the `_one` / `_other` pair is the
  // project's caller-side convention (performance_monitor.process_count_*).
  const key = `chat.${running ? 'tools_running' : 'tools_used'}_${count === 1 ? 'one' : 'other'}`

  return (
    <div>
      <button
        type="button"
        data-testid="chat-tools-line"
        aria-expanded={expanded}
        className="flex items-center gap-1.5 text-xs text-text-muted hover:text-text-primary cursor-pointer text-left"
        onClick={toggle}
      >
        <Wrench size={12} aria-hidden="true" />
        <span>{t(key, { count })}</span>
        <Caret size={10} weight="bold" aria-hidden="true" />
      </button>
      {expanded && (
        <div data-testid="chat-tools-ops" className="mt-1">
          {renderOperations()}
        </div>
      )}
    </div>
  )
}
