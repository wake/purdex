import { describe, it, expect, vi, beforeEach } from 'vitest'

const rereadWatchStatus = vi.fn()
vi.mock('./status-store', () => ({ rereadWatchStatus: (...a: unknown[]) => rereadWatchStatus(...a) }))
import { handleWatchEvent, isWatchEvent } from './watch-ws'

beforeEach(() => { rereadWatchStatus.mockReset() })

describe('handleWatchEvent', () => {
  it('knows only watch.changed: the retired watch.events push is not one', () => {
    expect([isWatchEvent('watch.changed'), isWatchEvent('watch.events'), isWatchEvent('fleet.changed')]).toEqual([true, false, false])
  })
  it('watch.changed reads the status again', () => {
    handleWatchEvent('h1', 'watch.changed', JSON.stringify({ watcher: null }))
    expect(rereadWatchStatus).toHaveBeenCalledWith('h1')
  })
  it('a watch.events frame does nothing', () => {
    handleWatchEvent('h1', 'watch.events', JSON.stringify({ host_id: 'd1', unhandled: 620, cause: 'recorded' }))
    expect(rereadWatchStatus).not.toHaveBeenCalled()
  })
})
