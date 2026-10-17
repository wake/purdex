import { describe, it, expect, beforeEach } from 'vitest'
import { readRange, writeRange, readQueueTab, writeQueueTab, readHostPage, writeHostPage, clearViewMemory } from './view-memory'

describe('watch view memory — 狀態｜設定', () => {
  it('defaults to 狀態, is kept per host and cleared with the rest', () => {
    expect(readHostPage('h1')).toBe('status')
    writeHostPage('h1', 'settings')
    expect([readHostPage('h1'), readHostPage('h2')]).toEqual(['settings', 'status'])
    clearViewMemory()
    expect(readHostPage('h1')).toBe('status')
  })
})

beforeEach(() => clearViewMemory())

describe('watch view memory', () => {
  it('defaults to 5 minutes', () => expect(readRange('h1')).toBe('5m'))
  it('keeps the range per host', () => {
    writeRange('h1', '1h')
    writeRange('h2', '24h')
    expect([readRange('h1'), readRange('h2'), readRange('h3')]).toEqual(['1h', '24h', '5m'])
  })
  it('is not persisted', () => {
    writeRange('h1', '6h')
    expect(JSON.stringify(Object.entries(localStorage))).not.toContain('6h')
  })
})

describe('watch view memory — queue sub-tab', () => {
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
