// spa/src/lib/pool/jobs-view-memory.test.ts — what the jobs tab remembers across a tab switch (module memory, not persisted): the mode, the history's three filters, the selected job and the history's scroll position.
import { describe, it, expect, beforeEach } from 'vitest'
import {
  clearJobsViewMemory, readJobsMode, writeJobsMode, readHistoryFilter, writeHistoryFilter, readSelectedJob, writeSelectedJob, readHistoryScroll, writeHistoryScroll, readLogScroll, writeLogScroll, readLiveScroll, writeLiveScroll, jobKey,
} from './jobs-view-memory'

beforeEach(() => clearJobsViewMemory())

describe('jobs view memory', () => {
  it('starts at the defaults: live, no filter, nothing selected, scrolled to the top', () => {
    expect(readJobsMode()).toBe('live')
    expect(readHistoryFilter()).toEqual({ group: 'all', host: '', repo: '' })
    expect(readSelectedJob()).toBeNull()
    expect(readHistoryScroll()).toBe(0)
  })

  it('keeps what was written, and clear puts every one back', () => {
    writeJobsMode('history')
    writeHistoryFilter({ group: 'failed', host: 'mserv', repo: 'purdex' })
    writeSelectedJob(jobKey('mlab', 'j1'))
    writeHistoryScroll(120)
    expect(readJobsMode()).toBe('history')
    expect(readHistoryFilter()).toEqual({ group: 'failed', host: 'mserv', repo: 'purdex' })
    expect(readSelectedJob()).toBe('mlab/j1')
    expect(readHistoryScroll()).toBe(120)
    clearJobsViewMemory()
    expect(readJobsMode()).toBe('live')
    expect(readHistoryFilter()).toEqual({ group: 'all', host: '', repo: '' })
    expect(readSelectedJob()).toBeNull()
    expect(readHistoryScroll()).toBe(0)
  })

  it('what is read is a copy: changing it does not change the memory', () => {
    const f = readHistoryFilter()
    f.group = 'failed'
    expect(readHistoryFilter().group).toBe('all')
  })

  it('a job is told apart by its origin as well as its id (an id is only unique within the host that queued it)', () => {
    expect(jobKey('mlab', 'j1')).not.toBe(jobKey('mserv', 'j1'))
  })

  it('the live view\'s scroll position starts at the top, is kept, and clear forgets it', () => {
    expect(readLiveScroll()).toBe(0)
    writeLiveScroll(88)
    expect(readLiveScroll()).toBe(88)
    clearJobsViewMemory()
    expect(readLiveScroll()).toBe(0)
  })

  it('the log scroll position is the selected job\'s: another job starts at the top, and clear forgets it', () => {
    writeLogScroll('mlab/a', 120)
    expect(readLogScroll('mlab/a')).toBe(120)
    expect(readLogScroll('mlab/b')).toBe(0)
    clearJobsViewMemory()
    expect(readLogScroll('mlab/a')).toBe(0)
  })
})
