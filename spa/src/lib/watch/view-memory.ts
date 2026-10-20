// spa/src/lib/watch/view-memory.ts — what a watcher host tab remembers across a tab switch (CLAUDE.md tab-hosted checklist). The pane
// unmounts when the reader leaves it (keepAliveCount 0), so a choice kept in `useState` would reset; it lives here, per pool host
// (`host_id`), in module memory (not persisted: a restart starts at the default). The host page keeps its sub-tab, its range and the scroll of each sub-tab here (MON-6f-1);
// the queue block's sub-tab and the 歷史 class switch are the old gate's, until MON-12. The 事件 tab's three chip filters, its scroll and the observations it last read are here too (W5e, MON-10c).
import type { HostRange } from '../pool/host-history'
import type { EventFilters } from '../pool/events-model'
import type { PoolObservation } from '../pool/observations-api'

/** The host page's sub-tabs (M41–M46): 監控, 狀態, 行程, 設定. */
export type HostSubTab = 'monitor' | 'status' | 'procs' | 'settings'
const hostTabs = new Map<string, HostSubTab>()
const hostRanges = new Map<string, HostRange>()
/** `${host_id}\0${sub-tab}` → scrollTop. */
const scrolls = new Map<string, number>()

/** The queue block's sub-tab: 當下 (now) or 歷史 (history, W5d). */
export type QueueTab = 'now' | 'history'
const queueTabs = new Map<string, QueueTab>()
/** The 歷史 body's class switch: 重指令 or 輕指令. */
export type HistoryClass = 'heavy' | 'light'
const historyClasses = new Map<string, HistoryClass>()

/** The 事件 tab's three chip filters (one set: the tab exists once). */
export const DEFAULT_EVENT_FILTERS: EventFilters = { host: 'all', source: 'all', status: 'all' }
let eventFilters: EventFilters = DEFAULT_EVENT_FILTERS
let eventsScroll = 0
/** What the 事件 tab last read of the observations (by pool alias), so coming back shows it at once; `forbidden`: a device token cannot read them. */
export interface EventsObservations { byHost: Record<string, readonly PoolObservation[]>; forbidden: boolean }
let eventsObservations: EventsObservations = { byHost: {}, forbidden: false }

export const readHostTab = (hostId: string): HostSubTab => hostTabs.get(hostId) ?? 'monitor'
export const writeHostTab = (hostId: string, tab: HostSubTab): void => { hostTabs.set(hostId, tab) }
/** 30 minutes by default, as the design's `HP.range`. */
export const readHostRange = (hostId: string): HostRange => hostRanges.get(hostId) ?? '30m'
export const writeHostRange = (hostId: string, range: HostRange): void => { hostRanges.set(hostId, range) }
export const readHostScroll = (hostId: string, sub: HostSubTab): number => scrolls.get(`${hostId}\0${sub}`) ?? 0
export const writeHostScroll = (hostId: string, sub: HostSubTab, top: number): void => { scrolls.set(`${hostId}\0${sub}`, top) }

export const readQueueTab = (fleetHostId: string): QueueTab => queueTabs.get(fleetHostId) ?? 'now'
export const writeQueueTab = (fleetHostId: string, tab: QueueTab): void => { queueTabs.set(fleetHostId, tab) }
export const readHistoryClass = (fleetHostId: string): HistoryClass => historyClasses.get(fleetHostId) ?? 'heavy'
export const writeHistoryClass = (fleetHostId: string, c: HistoryClass): void => { historyClasses.set(fleetHostId, c) }
export const readEventFilters = (): EventFilters => eventFilters
export const writeEventFilters = (f: EventFilters): void => { eventFilters = f }
export const readEventsScroll = (): number => eventsScroll
export const writeEventsScroll = (top: number): void => { eventsScroll = top }
export const readEventsObservations = (): EventsObservations => eventsObservations
export const writeEventsObservations = (o: EventsObservations): void => { eventsObservations = o }
export const clearViewMemory = (): void => {
  hostTabs.clear(); hostRanges.clear(); scrolls.clear(); queueTabs.clear(); historyClasses.clear(); eventFilters = DEFAULT_EVENT_FILTERS
  eventsScroll = 0; eventsObservations = { byHost: {}, forbidden: false }
}
