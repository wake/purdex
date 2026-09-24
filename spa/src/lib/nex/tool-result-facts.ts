// spa/src/lib/nex/tool-result-facts.ts — the shape of the N2 tool_result facts
// a caller hands to `OperationBlock`. Pure types: no React, no store.
//
// `toolResultFacts()`, which built ToolResultBlock's header facts span, is
// gone with that component (T3.3b). Every segment it produced now has a home
// of its own: `+N −M` travels with the diff (`room/ToolDiffView`), `non-text`
// sits on the operation's rail (`room/OperationBlock`), the line count is the
// fold affordance's `+N lines` and `truncated` is its daemon-truncation note
// (`room/FoldedOutput`) — spec §3.1.1 #3, which gives size to the fold and
// keeps the stat with the diff. A helper with no caller grows a second,
// disagreeing implementation, so it was deleted rather than kept.
import type { ToolActivity } from './tool-activity'

/**
 * The N2 tool_result facts a caller hands to OperationBlock. `Partial`
 * because `status` is required on the ToolActivity entry but a caller may
 * only have some of the facts (tests, or a raw block with no N2 overlay).
 */
export type ToolResultFacts = Partial<Pick<ToolActivity, 'output' | 'file' | 'diff' | 'status'>>
