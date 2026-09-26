// spa/src/components/chat/ChatFailedLine.tsx — a failed or denied tool is
// never hidden in chat (spec §5, R2 plan T2.1): one red line, `name · first
// line of the error`, expanding into the room's block for that operation.
// Not counted in the turn's tools line.
import type { ReactNode } from 'react'
import { CaretDown, CaretRight, Warning } from '@phosphor-icons/react'
import { useFold } from '../room/fold-context'

export interface ChatFailedLineProps {
  /** The operation's fold key (its block key); the line folds at `${foldKey}:chat-failed`. */
  foldKey: string
  /** The tool's name as the room's block names it. */
  name: string
  /** The result text; only its first non-blank line is shown. '' → the name alone. */
  message: string
  /** The room's block; only called when expanded. */
  renderOperation: () => ReactNode
}

function firstLine(text: string): string {
  for (const line of text.split('\n')) {
    const trimmed = line.trim()
    if (trimmed) return trimmed
  }
  return ''
}

export default function ChatFailedLine({ foldKey, name, message, renderOperation }: ChatFailedLineProps) {
  const [expanded, toggle] = useFold(`${foldKey}:chat-failed`)
  const Caret = expanded ? CaretDown : CaretRight
  const detail = firstLine(message)

  return (
    <div>
      <button
        type="button"
        data-testid="chat-failed-line"
        aria-expanded={expanded}
        className="flex items-start gap-1.5 text-xs text-status-error hover:underline cursor-pointer text-left min-w-0 max-w-full"
        onClick={toggle}
      >
        <Warning size={12} className="mt-0.5 shrink-0" aria-hidden="true" />
        <span className="break-all">{detail ? `${name} · ${detail}` : name}</span>
        <Caret size={10} weight="bold" className="mt-0.5 shrink-0" aria-hidden="true" />
      </button>
      {expanded && (
        <div data-testid="chat-failed-op" className="mt-1">
          {renderOperation()}
        </div>
      )}
    </div>
  )
}
