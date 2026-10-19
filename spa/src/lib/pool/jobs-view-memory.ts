// spa/src/lib/pool/jobs-view-memory.ts — what the jobs tab remembers across a tab switch (CLAUDE.md tab-hosted checklist). The pane unmounts when the reader leaves it (keepAliveCount 0), so a choice kept in
// `useState` would reset; it lives here, in module memory (not persisted: a restart starts at the default). One set: the tab exists once.
// Today: the mode, the history's three filters, the selected job (7b1-3 opens its detail) and the history's scroll position (7b1-3 adds the log's).
import type { StateGroup } from './jobs-view'

/** 佇列與執行中 (live) or 歷史 (history). */
export type JobsMode = 'live' | 'history'

/** The history's filters: a state group (`all` = every ended job), and the host it ran on and the repo (`''` = any). */
export interface HistoryFilterChoice { group: StateGroup | 'all'; host: string; repo: string }

const DEFAULT_FILTER: HistoryFilterChoice = { group: 'all', host: '', repo: '' }

let mode: JobsMode = 'live'
let filter: HistoryFilterChoice = DEFAULT_FILTER
let selected: string | null = null
let historyScroll = 0

/** An id is only unique within the host that queued the job. */
export const jobKey = (origin: string, id: string): string => `${origin}/${id}`

export const readJobsMode = (): JobsMode => mode
export const writeJobsMode = (m: JobsMode): void => { mode = m }
export const readHistoryFilter = (): HistoryFilterChoice => ({ ...filter })
export const writeHistoryFilter = (f: HistoryFilterChoice): void => { filter = { ...f } }
export const readSelectedJob = (): string | null => selected
export const writeSelectedJob = (key: string | null): void => { selected = key }
export const readHistoryScroll = (): number => historyScroll
export const writeHistoryScroll = (top: number): void => { historyScroll = top }

export const clearJobsViewMemory = (): void => { mode = 'live'; filter = DEFAULT_FILTER; selected = null; historyScroll = 0 }
