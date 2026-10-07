// A worker's light is cleared when its row leaves a host list that answered in FULL (the list asks for unarchived
// executions only: archived since). A walk that dropped a malformed row, or stopped on a repeated cursor, is still
// committed ready with the rows it has — but a row missing from it says nothing, so the worker keeps its light.
//
// Real: the host list store and its effects, the walk (`listAllExecutions`) and the page sanitizer, the worker
// projection and the agent store. Faked: the list REST call and the host's site-wide stream.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { startWorkerAgentProjection } from './useWorkerAgentProjection'
import { useAgentStore } from '../stores/useAgentStore'
import { useTabStore } from '../stores/useTabStore'
import { useExecutionStore } from '../stores/useExecutionStore'
import { LIST_REFRESH_DEBOUNCE_MS, resetExecutionListForTests, useExecutionListStore } from '../stores/useExecutionListStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useHostStore } from '../stores/useHostStore'
import { subscriptionSlots } from '../lib/nex/subscription-slots'
import { compositeKey } from '../lib/composite-key'
import type { ExecutionSummary, ExecutionsPage } from '../lib/nex/types'
import type { NexSseOptions } from '../lib/nex/nex-sse'
import type { Tab } from '../types/tab'
import * as api from '../lib/nex/nex-api'
import * as sse from '../lib/nex/nex-sse'

vi.mock('../lib/nex/nex-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/nex/nex-api')>()), listExecutions: vi.fn(),
}))
vi.mock('../lib/nex/nex-sse', async (importOriginal) => ({ ...(await importOriginal<typeof import('../lib/nex/nex-sse')>()), openNexSse: vi.fn() }))

const H = 'h', E = 'exc_target'
const KEY = compositeKey(H, 'exec-exc_target')

const row = (id: string, over: Partial<ExecutionSummary> = {}): ExecutionSummary =>
  ({ id, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {},
    created_at: 0, updated_at: 100, duration_ms: null, event_count: 0, observers: 0, archived: false, turn_count: 1, ...over }) as ExecutionSummary

const execTab: Tab = {
  id: 't-exec', pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: 'p-exec', content: { kind: 'execution', executionId: E, host: H } } },
}

/** The pages the next walk reads, by the cursor it asks with ('' = the first page). */
let pages: Record<string, unknown>
let siteStream: NexSseOptions | null
let seq: number
let stop: (() => void) | null

/** A frame on the host's site-wide stream; the debounced list refetch lands after it. */
async function hostEvent() {
  expect(siteStream).not.toBeNull()
  seq += 1
  siteStream!.onFrame({ id: String(seq), event: 'execution.running', data: '{}' })
  await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS)
}

const light = () => useAgentStore.getState().statuses[KEY]

beforeEach(() => {
  vi.useFakeTimers()
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  subscriptionSlots.resetForTests()
  resetExecutionListForTests()
  useExecutionListStore.setState({ byHost: {} })
  useExecutionStore.setState({ executions: {} })
  useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, oscTitles: {}, ccStatus: {}, unread: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useHostStore.setState({ hosts: { [H]: { id: H, name: 'mlab', ip: '1', port: 1 } } as never, hostOrder: [H], runtime: {} })
  useNexHostStore.setState({
    byHost: { [H]: { info: { configured: true, mounted: true, ready: true, init_error: '', effective: null }, capabilities: {} as never,
      phase: 'ready', error: null, fetchedAt: 0, generation: 0, fingerprint: '' } },
    ensure: vi.fn<(hostId: string) => Promise<void>>().mockResolvedValue(undefined),
  })
  siteStream = null
  seq = 0
  vi.mocked(sse.openNexSse).mockReset().mockImplementation((opts) => { siteStream = opts; return { close: vi.fn() } })
  vi.mocked(api.listExecutions).mockReset().mockImplementation(async (_hostId, opts) => {
    const page = pages[opts?.cursor ?? '']
    if (!page) throw new Error(`no page for cursor ${String(opts?.cursor)}`)
    return page as ExecutionsPage
  })
  stop = null
})

afterEach(() => {
  stop?.()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

/** The App with the worker's tab open; the first walk lists it running. */
async function boot() {
  pages = { '': { items: [row('exc_other'), row(E, { state: 'running' })], next_cursor: '' } }
  stop = startWorkerAgentProjection()
  useTabStore.setState({ tabs: { [execTab.id]: execTab }, tabOrder: [execTab.id] })
  await vi.advanceTimersByTimeAsync(0)
  expect(useExecutionListStore.getState().byHost[H].phase).toBe('ready')
  expect(light()).toBe('running')
}

describe('a row missing from a walk that did not answer in full never clears the worker', () => {
  it('the worker\'s own row malformed (dropped by the sanitizer): the light stays; a full answer without it clears it', async () => {
    await boot()
    pages = { '': { items: [row('exc_other'), { ...row(E, { state: 'running' }), state: 42 }], next_cursor: '' } }
    await hostEvent()
    expect(useExecutionListStore.getState().byHost[H].items.map((r) => r.id)).toEqual(['exc_other'])
    expect(light()).toBe('running')

    // A full answer without the row: archived since.
    pages = { '': { items: [row('exc_other')], next_cursor: '' } }
    await hostEvent()
    expect(light()).toBeUndefined()
  })

  it('a walk stopped by a repeated cursor before the worker\'s page: the light stays', async () => {
    await boot()
    pages = {
      '': { items: [row('exc_other')], next_cursor: 'c1' },
      // Nexen hands back the cursor it was asked with: the walk stops here, the worker's page never read.
      c1: { items: [], next_cursor: 'c1' },
    }
    await hostEvent()
    expect(useExecutionListStore.getState().byHost[H].phase).toBe('ready')
    expect(light()).toBe('running')

    // The next walk reaches it: its row decides again.
    pages = {
      '': { items: [row('exc_other')], next_cursor: 'c1' },
      c1: { items: [row(E, { state: 'idle', turn_count: 1, updated_at: 300 })], next_cursor: '' },
    }
    await hostEvent()
    expect(light()).toBe('idle')
  })
})
