// spa/src/lib/task-groups/loader.test.ts — a host's task groups are read only when its daemon declares `task_groups.v1` (name pool v2 spec §7, NA-6a). The Nex store's capability entry is the gate, as for the
// personas: a stale entry (`fetchedAt` 0, invalidated by a reconnect and not yet refetched) never counts, and a daemon without the capability is never asked.
import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('../../stores/useTaskGroupsStore', () => {
  const load = vi.fn(async () => {})
  return { useTaskGroupsStore: { getState: () => ({ load }) } }
})

import { useTaskGroupsStore } from '../../stores/useTaskGroupsStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { loadTaskGroupsIfSupported, taskGroupsSupported } from './loader'

const entry = (caps: string[], fetchedAt = 1) => ({ phase: 'ready', daemonCapabilities: caps, fetchedAt }) as never
const setEntry = (hostId: string, caps: string[], fetchedAt = 1) => useNexHostStore.setState({ byHost: { [hostId]: entry(caps, fetchedAt) } })
const load = () => useTaskGroupsStore.getState().load as ReturnType<typeof vi.fn>

beforeEach(() => {
  load().mockClear()
  useNexHostStore.setState({ byHost: {} })
})

describe('task groups loader', () => {
  it('loads from a daemon that declares task_groups.v1', async () => {
    setEntry('h1', ['task_groups.v1'])
    expect(taskGroupsSupported('h1')).toBe(true)
    await loadTaskGroupsIfSupported('h1')
    expect(load()).toHaveBeenCalledWith('h1')
  })

  it.each([
    ['a daemon without the capability', () => setEntry('h1', ['personas.v1'])],
    ['an entry not yet fetched from the daemon now answering', () => setEntry('h1', ['task_groups.v1'], 0)],
    ['a host with no entry', () => {}],
  ])('asks nothing of %s', async (_n, setup) => {
    setup()
    expect(taskGroupsSupported('h1')).toBe(false)
    await loadTaskGroupsIfSupported('h1')
    expect(load()).not.toHaveBeenCalled()
  })
})
