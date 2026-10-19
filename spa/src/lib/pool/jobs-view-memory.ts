// spa/src/lib/pool/jobs-view-memory.ts — what the jobs tab remembers across a tab switch (CLAUDE.md tab-hosted checklist). The pane unmounts when the reader leaves it (keepAliveCount 0), so a choice kept in
// `useState` would reset; it lives here, in module memory (not persisted: a restart starts at the default). One set: the tab exists once. 7b1-2b adds the history's filters, the selected job and the scroll positions.

/** 佇列與執行中 (live) or 歷史 (history). */
export type JobsMode = 'live' | 'history'

let mode: JobsMode = 'live'

export const readJobsMode = (): JobsMode => mode
export const writeJobsMode = (m: JobsMode): void => { mode = m }
export const clearJobsViewMemory = (): void => { mode = 'live' }
