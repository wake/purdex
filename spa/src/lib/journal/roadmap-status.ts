// spa/src/lib/journal/roadmap-status.ts — how the roadmap tab shows a status (J-2d, spec §11, plan §7). Theme tokens only. The colours
// are the journal's kind tokens (each checked >= 4.5:1 on every theme): 進行中 blue (exec), 審查中 purple (comm), 暫停 orange (task_new).
// The word always comes with the colour, so the colour is never the only cue. The 暫停 dot is NOT `bg-status-warning`: on the light
// themes that is a pale fill and the dot would nearly vanish.
import type { RoadmapStatus } from './types'

export interface RoadmapStatusStyle {
  /** The i18n key suffix: `journal.roadmap.status.<id>`. */
  id: 'todo' | 'doing' | 'review' | 'done' | 'paused'
  /** Text colour class. */
  text: string
  /** Dot colour class. */
  dot: string
}

export const ROADMAP_STATUS_STYLE: Record<RoadmapStatus, RoadmapStatusStyle> = {
  未開始: { id: 'todo', text: 'text-text-muted', dot: 'bg-text-muted' },
  進行中: { id: 'doing', text: 'text-journal-kind-exec', dot: 'bg-journal-kind-exec' },
  審查中: { id: 'review', text: 'text-journal-kind-comm', dot: 'bg-journal-kind-comm' },
  完成: { id: 'done', text: 'text-status-success-text', dot: 'bg-status-success-text' },
  暫停: { id: 'paused', text: 'text-journal-kind-task-new', dot: 'bg-journal-kind-task-new' },
}

export const ROADMAP_STATUS_IDS: readonly RoadmapStatusStyle['id'][] = Object.values(ROADMAP_STATUS_STYLE).map((s) => s.id)
