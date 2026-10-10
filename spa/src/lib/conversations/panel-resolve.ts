// spa/src/lib/conversations/panel-resolve.ts — turns the panel's remembered REFERENCE (panel-memory) into the current
// steps of the conversation. A reference whose turn or step is gone (a reset, a window that moved) resolves to null and the
// panel says so instead of showing stale items.
import type { ConversationItem, StepItem } from './types'
import type { PanelContent } from './panel-memory'
import { turnRows } from './turn-row'

export interface PanelTurn { id: string; index: number; items: ConversationItem[] }

export type PanelView =
  | { kind: 'chain'; turnIndex: number; position: number; count: number; steps: StepItem[] }
  | { kind: 'output'; step: StepItem }
  | { kind: 'subagent'; step: StepItem; items: ConversationItem[] }

/** The panel header's title (the chain one names the turn and the chain's position in it). */
export function panelTitle(view: PanelView, t: (k: string, p?: Record<string, string | number>) => string): string {
  switch (view.kind) {
    case 'chain': return t('panel.chain', { turn: view.turnIndex + 1, k: view.position, n: view.count })
    case 'output': return t(view.step.diff && !view.step.output ? 'panel.diff' : 'panel.output')
    case 'subagent': return t('panel.subagent', { name: view.step.subagent?.description ?? view.step.subagent?.type ?? view.step.summary })
  }
}

/** A step by id among items, descending into a subagent step's loaded children (they nest as deep as agents spawn agents). */
const findIn = (items: ReadonlyArray<{ type: string; id: string }>, id: string): StepItem | null => {
  for (const it of items) {
    if (it.type !== 'step') continue
    const s = it as StepItem
    if (s.id === id) return s
    const hit = s.children ? findIn(s.children, id) : null
    if (hit) return hit
  }
  return null
}
/** Only inside the named turn: the same step id in another turn is another step. */
const findStep = (turns: PanelTurn[], turnId: string, id: string): StepItem | null => {
  const turn = turns.find((t) => t.id === turnId)
  return turn ? findIn(turn.items, id) : null
}

export function resolvePanel(content: PanelContent, turns: PanelTurn[]): PanelView | null {
  if (content.kind === 'chain') {
    const turn = turns.find((t) => t.id === content.turnId)
    if (!turn) return null
    const runs = turnRows(turn).runs
    const at = runs.findIndex((r) => r.stepIds[0] === content.firstStepId)
    if (at < 0) return null
    return { kind: 'chain', turnIndex: turn.index, position: at + 1, count: runs.length, steps: runs[at].steps }
  }
  const step = findStep(turns, content.turnId, content.stepId)
  if (!step) return null
  if (content.kind === 'output') return { kind: 'output', step }
  // A subagent's items are loaded on demand and kept on the step (types.ts `children`); the placement `index` is not theirs.
  const items = (step.children ?? []).map((c, index) => ({ ...c, index }) as ConversationItem)
  return { kind: 'subagent', step, items }
}
