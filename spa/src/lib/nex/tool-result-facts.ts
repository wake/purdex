// spa/src/lib/nex/tool-result-facts.ts — the tool_result header facts span
// (P-B3.2 spec §4.4 R4). Pure: no React, no store; `t` is injected.
//
// `ToolResultFacts` is OperationBlock's `facts` prop. `toolResultFacts()`
// itself rendered ToolResultBlock's header segments and has had no caller
// since T3.3 deleted that component; the new block shows the diff and the
// fold count instead. Kept, with its tests, until spec §4.2's header decides
// what the segments become.
import type { ToolActivity } from './tool-activity'

/**
 * The N2 tool_result facts a caller hands to OperationBlock. `Partial`
 * because `status` is required on the ToolActivity entry but a caller may
 * only have some of the facts (tests, or a raw block with no N2 overlay).
 */
export type ToolResultFacts = Partial<Pick<ToolActivity, 'output' | 'file' | 'diff' | 'status'>>

/** U+2212 MINUS SIGN — same width as `+` in tabular-nums, unlike the hyphen. */
const MINUS = '−'

/**
 * Header segments, in a fixed order: line count (from `file`, else from
 * `output` when there is more than one line and no `diff`), `+N −M` for a
 * diff (always — `+0 −0` is a normal edit result, contract rule 7), then the
 * `truncated` / `non-text` markers. `status` is not rendered here: the
 * denied override reads `facts.status` directly in the component.
 */
export function toolResultFacts(
  facts: ToolResultFacts | undefined,
  t: (key: string, params?: Record<string, string | number>) => string,
): string[] {
  if (!facts) return []
  const { file, diff, output } = facts
  const out: string[] = []
  if (file) out.push(t('execution.tool.lines', { n: file.lines }))
  if (diff) out.push(`+${diff.added} ${MINUS}${diff.removed}`)
  if (!file && !diff && output && output.totalLines > 1) out.push(t('execution.tool.lines', { n: output.totalLines }))
  if (output?.truncated) out.push(t('execution.tool.truncated'))
  if (output?.hasNonText) out.push(t('execution.tool.non_text'))
  return out
}
