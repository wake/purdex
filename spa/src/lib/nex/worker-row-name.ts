// spa/src/lib/nex/worker-row-name.ts — the name of a row in the Workers lists (`ExecutionRowCompact`: the
// activity-bar list, the New Tab list, Settings → Worker → Workers). #1771: a worker created by a handoff has an
// empty brief, so a brief-only row had no name and several waiting workers could not be told apart.
import { basename } from '../storage-paths'
import { firstLine } from './format'
import { oneLine } from './worker-tab-title'
import type { ExecutionSummary } from './types'

/**
 * The first present of:
 * ① the brief's first line, exactly as the row showed it before #1771 (80-char cap, untrimmed) — a blank first line
 *   (empty or whitespace-only) counts as no brief;
 * ② `session_title.text`, one-lined (`oneLine`, the tab-title rule) — read only when `titleSupported` (the host's
 *   `selectSessionTitleSupported`, the same fail-closed gate as `workerTitleOf`);
 * ③ the cwd's last path segment;
 * ④ '' (the row's look before #1771).
 * Non-string fields (a garbage page seeded into the store) count as absent.
 */
export function workerRowName(
  row: Pick<ExecutionSummary, 'brief' | 'cwd' | 'session_title'>,
  titleSupported: boolean,
): string {
  const brief = firstLine(typeof row.brief === 'string' ? row.brief : '')
  if (brief.trim() !== '') return brief
  if (titleSupported) {
    const text = row.session_title?.text
    const title = oneLine(typeof text === 'string' ? text : '')
    if (title) return title
  }
  return typeof row.cwd === 'string' ? basename(row.cwd) : ''
}
