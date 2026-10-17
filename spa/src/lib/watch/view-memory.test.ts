import { describe, it, expect, beforeEach } from 'vitest'
import { readRange, writeRange, readQueueTab, writeQueueTab, clearViewMemory } from './view-memory'

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
