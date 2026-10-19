// spa/src/lib/task-groups/lifecycle.test.ts — a host's cached task groups belong to the daemon they were read from: removing the host, or moving it to another address/port/token under the same id, forgets them
// (name pool v2 spec §7, NA-6b), so the view never shows one daemon's groups under another's label.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

vi.mock('../../stores/useTaskGroupsStore', () => {
  const forget = vi.fn()
  return { useTaskGroupsStore: { getState: () => ({ forget }) } }
})

import { useTaskGroupsStore } from '../../stores/useTaskGroupsStore'
import { useHostStore } from '../../stores/useHostStore'
import { startTaskGroupsLifecycle } from './loader'

const forget = () => useTaskGroupsStore.getState().forget as ReturnType<typeof vi.fn>
const host = (ip: string, port = 7860, token = 't') => ({ id: 'h1', name: 'h1', ip, port, token }) as never

let stop: () => void
beforeEach(() => {
  forget().mockClear()
  useHostStore.setState({ hosts: { h1: host('10.0.0.1') } })
  stop = startTaskGroupsLifecycle()
})
afterEach(() => stop())

describe('task groups lifecycle', () => {
  it('forgets a removed host', () => {
    useHostStore.setState({ hosts: {} })
    expect(forget()).toHaveBeenCalledWith('h1')
  })

  it.each([['address', host('10.0.0.2')], ['port', host('10.0.0.1', 7861)], ['token', host('10.0.0.1', 7860, 'other')]])('forgets a host re-pointed to another %s', (_n, next) => {
    useHostStore.setState({ hosts: { h1: next } })
    expect(forget()).toHaveBeenCalledWith('h1')
  })

  it('forgets a host whose endpoint now answers as another daemon (daemonId mismatch)', () => {
    const rt = (observed: string) => ({ h1: { status: 'connected', daemonIdMismatch: { stored: 'D1', observed, endpoint: '10.0.0.1:7860' } } }) as never
    useHostStore.setState({ runtime: rt('D2') })
    expect(forget()).toHaveBeenCalledTimes(1)
    expect(forget()).toHaveBeenCalledWith('h1')
    useHostStore.setState({ runtime: rt('D2') }) // the same finding again: nothing new
    expect(forget()).toHaveBeenCalledTimes(1)
    useHostStore.setState({ runtime: rt('D3') }) // yet another daemon there
    expect(forget()).toHaveBeenCalledTimes(2)
  })

  it('keeps the cache when only the name changes', () => {
    useHostStore.setState({ hosts: { h1: { ...host('10.0.0.1'), name: 'renamed' } as never } })
    expect(forget()).not.toHaveBeenCalled()
  })

  it('stops listening after the returned stop', () => {
    stop()
    useHostStore.setState({ hosts: {} })
    expect(forget()).not.toHaveBeenCalled()
  })
})
