// spa/src/lib/watch/view-memory.ts — what a watcher host tab remembers across a tab switch (CLAUDE.md tab-hosted checklist). The pane
// unmounts when the reader leaves it (keepAliveCount 0), so a choice kept in `useState` would reset; it lives here, per fleet host,
// in module memory (not persisted: a restart starts at the default). W5b1 / W5c1 add the queue sub-tab and 狀態｜設定 here.
import type { HistoryRange } from '../fleet/history'

const ranges = new Map<string, HistoryRange>()

export const readRange = (fleetHostId: string): HistoryRange => ranges.get(fleetHostId) ?? '5m'
export const writeRange = (fleetHostId: string, range: HistoryRange): void => { ranges.set(fleetHostId, range) }
export const clearViewMemory = (): void => { ranges.clear() }
