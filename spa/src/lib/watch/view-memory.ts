// spa/src/lib/watch/view-memory.ts — what a watcher host tab remembers across a tab switch (CLAUDE.md tab-hosted checklist). The pane
// unmounts when the reader leaves it (keepAliveCount 0), so a choice kept in `useState` would reset; it lives here, per fleet host,
// in module memory (not persisted: a restart starts at the default). W5b1 / W5c1 add the queue sub-tab and 狀態｜設定 here; W5e adds the 事件 tab's three chip filters.
import type { HistoryRange } from '../fleet/history'
import { DEFAULT_FILTERS, type EventFilters } from './events-view'

const ranges = new Map<string, HistoryRange>()
/** The queue block's sub-tab: 當下 (now) or 歷史 (history, W5d). */
export type QueueTab = 'now' | 'history'
const queueTabs = new Map<string, QueueTab>()
/** The 歷史 body's class switch: 重指令 or 輕指令. */
export type HistoryClass = 'heavy' | 'light'
const historyClasses = new Map<string, HistoryClass>()

/** The 事件 tab's three chip filters (one set: the tab exists once). */
let eventFilters: EventFilters = DEFAULT_FILTERS

export const readRange = (fleetHostId: string): HistoryRange => ranges.get(fleetHostId) ?? '5m'
export const writeRange = (fleetHostId: string, range: HistoryRange): void => { ranges.set(fleetHostId, range) }
export const readQueueTab = (fleetHostId: string): QueueTab => queueTabs.get(fleetHostId) ?? 'now'
export const writeQueueTab = (fleetHostId: string, tab: QueueTab): void => { queueTabs.set(fleetHostId, tab) }
export const readHistoryClass = (fleetHostId: string): HistoryClass => historyClasses.get(fleetHostId) ?? 'heavy'
export const writeHistoryClass = (fleetHostId: string, c: HistoryClass): void => { historyClasses.set(fleetHostId, c) }
export const readEventFilters = (): EventFilters => eventFilters
export const writeEventFilters = (f: EventFilters): void => { eventFilters = f }
/** The host tab's page: 狀態 (the readings) or 設定 (W5c1). */
export type HostPage = 'status' | 'settings'
const pages = new Map<string, HostPage>()
export const readHostPage = (fleetHostId: string): HostPage => pages.get(fleetHostId) ?? 'status'
export const writeHostPage = (fleetHostId: string, page: HostPage): void => { pages.set(fleetHostId, page) }
export const clearViewMemory = (): void => { ranges.clear(); queueTabs.clear(); historyClasses.clear(); pages.clear(); eventFilters = DEFAULT_FILTERS }
