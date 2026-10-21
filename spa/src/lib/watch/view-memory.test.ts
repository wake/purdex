import { describe, it, expect, beforeEach } from 'vitest'
import {
  readHostRange, writeHostRange, readHostTab, writeHostTab, readHostScroll, writeHostScroll,
  readQueueTab, writeQueueTab, clearViewMemory, readProcAt, writeProcAt, readObsExpanded, writeObsExpanded,
  readEventFilters, writeEventFilters, readEventsScroll, writeEventsScroll, readEventsObservations, writeEventsObservations,
} from './view-memory'

beforeEach(() => clearViewMemory())

describe('watch view memory — the host page\'s sub-tab (MON-6f-1)', () => {
  it('defaults to 監控, is kept per host_id and cleared with the rest', () => {
    expect(readHostTab('h1')).toBe('monitor')
    writeHostTab('h1', 'settings')
    writeHostTab('h2', 'procs')
    expect([readHostTab('h1'), readHostTab('h2'), readHostTab('h3')]).toEqual(['settings', 'procs', 'monitor'])
    clearViewMemory()
    expect(readHostTab('h1')).toBe('monitor')
  })
})

describe('watch view memory — the host page\'s range', () => {
  it('defaults to 30 minutes (the design\'s HP.range) and is kept per host', () => {
    expect(readHostRange('h1')).toBe('30m')
    writeHostRange('h1', '1h')
    writeHostRange('h2', '24h')
    expect([readHostRange('h1'), readHostRange('h2'), readHostRange('h3')]).toEqual(['1h', '24h', '30m'])
  })
  it('is not persisted', () => {
    writeHostRange('h1', '6h')
    expect(JSON.stringify(Object.entries(localStorage))).not.toContain('6h')
  })
})

describe('watch view memory — the host page\'s scroll', () => {
  it('is kept per host and per sub-tab, 0 by default', () => {
    expect(readHostScroll('h1', 'monitor')).toBe(0)
    writeHostScroll('h1', 'monitor', 120)
    writeHostScroll('h1', 'procs', 40)
    writeHostScroll('h2', 'monitor', 7)
    expect([readHostScroll('h1', 'monitor'), readHostScroll('h1', 'procs'), readHostScroll('h1', 'status'), readHostScroll('h2', 'monitor')]).toEqual([120, 40, 0, 7])
    clearViewMemory()
    expect(readHostScroll('h1', 'monitor')).toBe(0)
  })
})

describe('watch view memory — queue sub-tab (the old gate\'s, until MON-12)', () => {
  it('defaults to 當下 (now) and is kept per host', () => {
    expect(readQueueTab('h1')).toBe('now')
    writeQueueTab('h1', 'history')
    expect([readQueueTab('h1'), readQueueTab('h2')]).toEqual(['history', 'now'])
  })
  it('is cleared with the rest', () => {
    writeQueueTab('h1', 'history')
    clearViewMemory()
    expect(readQueueTab('h1')).toBe('now')
  })
})

describe('watch view memory — the events tab (MON-10c)', () => {
  it('defaults to no filter, no scroll and nothing read; keeps what is written; is cleared with the rest', () => {
    expect(readEventFilters()).toEqual({ host: 'all', source: 'all', status: 'all' })
    expect(readEventsScroll()).toBe(0)
    expect(readEventsObservations()).toEqual({ source: null, owners: {}, byHost: {}, forbidden: [], failed: [], truncated: [] })
    writeEventFilters({ host: 'h1', source: 'obs', status: 'live' })
    writeEventsScroll(240)
    writeEventsObservations({ source: 'src', owners: { mserv: 'h' }, byHost: { mserv: [] }, forbidden: ['mserv'], failed: [], truncated: [] })
    expect(readEventFilters()).toEqual({ host: 'h1', source: 'obs', status: 'live' })
    expect(readEventsScroll()).toBe(240)
    expect(readEventsObservations().forbidden).toEqual(['mserv'])
    clearViewMemory()
    expect([readEventFilters().host, readEventsScroll(), readEventsObservations().forbidden]).toEqual(['all', 0, []])
  })
})

describe('the 行程 tab\'s picked record (MON-8b)', () => {
  it('is the time of the record, per host; null is the live one; clearing forgets it', () => {
    expect(readProcAt('h1')).toBeNull()
    writeProcAt('h1', 1_800_000_000_000)
    writeProcAt('h2', 5)
    expect([readProcAt('h1'), readProcAt('h2')]).toEqual([1_800_000_000_000, 5])
    writeProcAt('h1', null)
    expect(readProcAt('h1')).toBeNull()
    expect(readProcAt('h2')).toBe(5)
    clearViewMemory()
    expect(readProcAt('h2')).toBeNull()
  })

  it('whether every observation record is shown is kept per host, and cleared with the rest', () => {
    expect(readObsExpanded('h1')).toBe(false)
    writeObsExpanded('h1', true)
    expect(readObsExpanded('h1')).toBe(true)
    expect(readObsExpanded('h2')).toBe(false)
    writeObsExpanded('h1', false)
    expect(readObsExpanded('h1')).toBe(false)
    writeObsExpanded('h2', true)
    clearViewMemory()
    expect(readObsExpanded('h2')).toBe(false)
  })
})
