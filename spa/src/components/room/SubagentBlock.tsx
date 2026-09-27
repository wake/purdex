// spa/src/components/room/SubagentBlock.tsx — a Task call's subagent, drawn
// inside the Task's own block as a nested rail (spec §4.5, #1263).
//
// Folded it is one line — the subagent's name and how many tools it called;
// the Task header above already carries the description and the duration, so
// neither is said twice. Unfolded it is the child's own messages, drawn by the
// same renderer as the top level, one indent deeper on a second rail. The
// hand-back is not drawn here: it is the Task's result, and OperationBlock
// puts it after this block, so the transcript reads child output → hand-back.
//
// The expansion lives in the pane's fold memory at `${foldKey}:subagent`, so
// it survives a re-keyed row and the turn's expand-all reaches it.
//
// It takes the children as a callback rather than importing the renderer:
// the renderer is what draws this block, and the callback keeps that a
// one-way dependency.
//
// R4 T3.3 (Q2): with the subagent's task row (nexen v0.13 task events) the
// folded line closes out — running adds the elapsed; ended shows usage
// (tokens · tools · duration) in place of the counted tools, then a status
// word. No row (older daemon, not found) → exactly as before.
import type { ReactNode } from 'react'
import { CaretDown, CaretRight } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { closeOut, type CloseOut } from '../../lib/nex/subagent-close-out'
import type { WorkerTask } from '../../lib/nex/types'
import { useFold } from './fold-context'

/** `failed` in red; `stopped` / `interrupted` neutral (lost is "unknown", never a failure). */
export function StatusWord({ status }: { status: NonNullable<CloseOut['status']> }) {
  return <span data-testid="subagent-status" className={status.error ? 'text-status-error' : undefined}>{status.text}</span>
}

/**
 * The close-out on a Task call's own header, for a subagent with no children
 * to fold (backgrounded, or its frames not here): `· 26k tokens · 8 tools ·
 * 12s · failed`. Nothing at all when there is nothing to say.
 */
export function SubagentTaskSuffix({ task, now }: { task: WorkerTask; now?: number }) {
  const t = useI18nStore((s) => s.t)
  const c = closeOut(task, now, t)
  const parts = [...(c.usage ?? []), ...(c.elapsed ? [c.elapsed] : [])]
  if (parts.length === 0 && !c.status) return null
  return (
    <span data-testid="subagent-task-suffix" className="text-xs text-text-muted shrink-0">
      · {parts.join(' · ')}{parts.length > 0 && c.status && ' · '}{c.status && <StatusWord status={c.status} />}
    </span>
  )
}

export interface SubagentBlockProps {
  /** The subagent's kind (`subagent_type`), or the tool name when the call did not say. */
  name: string
  /** tool_use blocks the child itself made (not its own subagents'). */
  toolCount: number
  /** The Task call's fold key. */
  foldKey: string
  /** How deep the child's rail sits: 1 under a top-level Task. */
  depth: number
  /** The child's own messages; only called when expanded. */
  renderChildren: () => ReactNode
  /** The subagent's task row (looked up by the Task call's tool_use_id), if the daemon has one. */
  task?: WorkerTask
  /** The pane's clock, for a running task's elapsed. */
  now?: number
}

export default function SubagentBlock({ name, toolCount, foldKey, depth, renderChildren, task, now }: SubagentBlockProps) {
  const t = useI18nStore((s) => s.t)
  const [expanded, toggle] = useFold(`${foldKey}:subagent`)
  const Caret = expanded ? CaretDown : CaretRight
  const c = task ? closeOut(task, now, t) : null
  const summary = c?.usage
    ? [name, ...c.usage].join(' · ')
    : `${t('room.subagent.summary', { name, tools: toolCount })}${c?.elapsed ? ` · ${c.elapsed}` : ''}`

  return (
    <div data-testid="subagent-block" className="my-1">
      <button
        type="button"
        data-testid="subagent-toggle"
        aria-expanded={expanded}
        className="flex items-center gap-1 text-xs text-text-secondary hover:text-text-primary cursor-pointer text-left"
        onClick={toggle}
      >
        <Caret size={10} weight="bold" aria-hidden="true" />
        <span>{summary}{c?.status && <> · <StatusWord status={c.status} /></>}</span>
      </button>
      {expanded && (
        <div
          data-testid="subagent-rail"
          data-depth={depth}
          className="mt-1 ml-[3px] border-l border-border-subtle pl-3 space-y-4"
        >
          {renderChildren()}
        </div>
      )}
    </div>
  )
}
