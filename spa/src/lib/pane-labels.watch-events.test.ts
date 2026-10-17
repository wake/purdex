// W5e: the events tab's label carries the number of events waiting (「事件 n」).
import { describe, it, expect, afterEach } from 'vitest'
import { getPaneLabel } from './pane-labels'
import type { TFunction } from './pane-labels'
import type { PaneContent } from '../types/tab'
import { useWatchEventsStore } from './watch/events-store'

const mockT: TFunction = (key, params) => (params ? `${key}:${JSON.stringify(params)}` : key)
const ev = (id: string, status: 'pending' | 'handled') => ({ id, host: 'm', kind: 'pressure_high', at: '2026-10-17T01:00:00Z', status })
const label = (c: PaneContent) => getPaneLabel(c, { getByCode: () => undefined }, { getById: () => undefined }, mockT)

afterEach(() => useWatchEventsStore.getState().reset())

describe('the events tab label', () => {
  const content: PaneContent = { kind: 'watch-events' }
  it('is 事件 alone while nothing waits (handled events and no hosts count none)', () => {
    expect(label(content)).toBe('watch.pane.events')
    useWatchEventsStore.setState({ byHost: { h1: { kind: 'ok', at: 1, events: [ev('1', 'handled')] } } })
    expect(label(content)).toBe('watch.pane.events')
  })
  it('is 事件 n with the pending events of every host', () => {
    useWatchEventsStore.setState({ byHost: {
      h1: { kind: 'ok', at: 1, events: [ev('1', 'pending'), ev('2', 'pending'), ev('3', 'handled')] },
      h2: { kind: 'ok', at: 1, events: [ev('4', 'pending')] },
    } })
    expect(label(content)).toBe('watch.pane.events_n:{"n":3}')
  })
})
