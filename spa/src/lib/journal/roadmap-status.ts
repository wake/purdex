// spa/src/lib/journal/roadmap-status.ts — how the roadmap tab shows a status (J-2d, spec §11). Theme tokens only. The word always
// comes with the colour: 審查中 has no colour token of its own (no purple in the themes), so it is told apart by its label.
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
  進行中: { id: 'doing', text: 'text-accent', dot: 'bg-accent' },
  審查中: { id: 'review', text: 'text-text-secondary', dot: 'bg-text-secondary' },
  完成: { id: 'done', text: 'text-status-success-text', dot: 'bg-status-success-text' },
  暫停: { id: 'paused', text: 'text-status-warning-text', dot: 'bg-status-warning' },
}

export const ROADMAP_STATUS_IDS: readonly RoadmapStatusStyle['id'][] = Object.values(ROADMAP_STATUS_STYLE).map((s) => s.id)
