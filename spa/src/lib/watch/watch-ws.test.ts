import { describe, it, expect, vi, beforeEach } from 'vitest'

const rereadWatchStatus = vi.fn()
const rereadWatchEvents = vi.fn()
const applyWatchUnhandled = vi.fn()
vi.mock('./status-store', () => ({ rereadWatchStatus: (...a: unknown[]) => rereadWatchStatus(...a) }))
vi.mock('./events-store', () => ({
  rereadWatchEvents: (...a: unknown[]) => rereadWatchEvents(...a),
  applyWatchUnhandled: (...a: unknown[]) => applyWatchUnhandled(...a),
}))
import { handleWatchEvent, isWatchEvent } from './watch-ws'

beforeEach(() => { rereadWatchStatus.mockReset(); rereadWatchEvents.mockReset(); applyWatchUnhandled.mockReset() })

describe('handleWatchEvent', () => {
  it('knows its two types', () => {
    expect([isWatchEvent('watch.changed'), isWatchEvent('watch.events'), isWatchEvent('fleet.changed')]).toEqual([true, true, false])
  })
  it('watch.changed reads the status again and touches nothing else', () => {
    handleWatchEvent('h1', 'watch.changed', JSON.stringify({ watcher: null }))
    expect(rereadWatchStatus).toHaveBeenCalledWith('h1')
    expect(rereadWatchEvents).not.toHaveBeenCalled()
    expect(applyWatchUnhandled).not.toHaveBeenCalled()
  })
  it('watch.events sets the count from the payload and reads the events again', () => {
    handleWatchEvent('h1', 'watch.events', JSON.stringify({ host_id: 'd1', unhandled: 620, cause: 'recorded' }))
    expect(applyWatchUnhandled).toHaveBeenCalledWith('h1', 620)
    expect(rereadWatchEvents).toHaveBeenCalledWith('h1')
    expect(rereadWatchStatus).not.toHaveBeenCalled()
  })
  it('a payload that is not JSON, or has no number, still reads the events again and sets no count', () => {
    for (const value of ['not json', JSON.stringify({ host_id: 'd1' }), JSON.stringify({ unhandled: '3' }), JSON.stringify(null)]) {
      applyWatchUnhandled.mockReset(); rereadWatchEvents.mockReset()
      handleWatchEvent('h1', 'watch.events', value)
      expect(applyWatchUnhandled).not.toHaveBeenCalled()
      expect(rereadWatchEvents).toHaveBeenCalledWith('h1')
    }
  })
  it('accepts a payload that already arrived parsed', () => {
    handleWatchEvent('h1', 'watch.events', { unhandled: 2 } as never)
    expect(applyWatchUnhandled).toHaveBeenCalledWith('h1', 2)
  })
})
