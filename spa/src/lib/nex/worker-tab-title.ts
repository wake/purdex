// spa/src/lib/nex/worker-tab-title.ts — the worker (execution) tab title
// (worker-pane theme spec §8.4 / N1): `<primary> - <cwd basename>`.
import { basename } from '../storage-paths'
import { firstLine } from './format'

export interface WorkerTabTitleInput {
  /** Nexen `summary.session_title.text` — pass only when the host capability says the field exists (phase E). */
  sessionTitle?: string | null
  /** The pre-handoff terminal title recorded on the pane content at Hand-to-nex time. */
  fromTitle?: string
  brief?: string
  cwd?: string
}

/**
 * Single line, trimmed, not length-capped (the tab truncates by CSS); '' when there is nothing to show. The tab-title
 * rule for text that comes from a conversation; the conversation rebuild tab's label uses it too (pane-labels.ts).
 */
export function oneLine(text: string | null | undefined): string {
  return firstLine((text ?? '').replace(/\r/g, ''), Number.POSITIVE_INFINITY).trim()
}

/**
 * Primary is the first present of session_title → pre-handoff title →
 * first line of the brief (`last-prompt` is never used). Null when none has
 * text, so the caller keeps its own label.
 */
export function workerTabTitle({ sessionTitle, fromTitle, brief, cwd }: WorkerTabTitleInput): string | null {
  const primary = oneLine(sessionTitle) || oneLine(fromTitle) || oneLine(brief)
  if (!primary) return null
  const dir = cwd ? basename(cwd) : ''
  return dir ? `${primary} - ${dir}` : primary
}
