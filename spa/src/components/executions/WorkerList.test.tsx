// spa/src/components/executions/WorkerList.test.tsx
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, act, within } from '@testing-library/react'
import { WorkerList } from './WorkerList'
import { resetExecutionListForTests, useExecutionListStore } from '../../stores/useExecutionListStore'
import { useNexHostStore, type NexHostEntry } from '../../stores/useNexHostStore'
import { useHostStore } from '../../stores/useHostStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { subscriptionSlots } from '../../lib/nex/subscription-slots'
import * as api from '../../lib/nex/nex-api'
import * as sse from '../../lib/nex/nex-sse'

vi.mock('../../lib/nex/nex-api', () => ({ listExecutions: vi.fn(), attachControl: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), archiveExecution: vi.fn() }))
vi.mock('../../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))
vi.mock('../../lib/deeplink/deeplinkResolver', () => ({ openExecutionDetailTab: vi.fn() }))

const A = 'host-a'
const B = 'host-b'
const C = 'host-c'

const readyEntry: NexHostEntry = {
  info: { configured: true, mounted: true, ready: true, init_error: '', effective: null },
  capabilities: null, phase: 'ready', error: null, fetchedAt: 1, generation: 1, fingerprint: '1:1:t',
}
const entryWith = (patch: Partial<NexHostEntry>): NexHostEntry => ({ ...readyEntry, ...patch })
const disabledEntry = entryWith({ phase: 'disabled' })
const loadingEntry = entryWith({ info: null, phase: 'loading' })

let ensure: ReturnType<typeof vi.fn<(hostId: string) => Promise<void>>>

/** The host names of the rendered sections, top to bottom. */
const sectionNames = () =>
  screen.queryAllByTestId('executions-header').map((h) => h.textContent)

beforeEach(() => {
  subscriptionSlots.resetForTests()
  resetExecutionListForTests()
  useExecutionListStore.setState({ byHost: {} })
  ensure = vi.fn<(hostId: string) => Promise<void>>().mockResolvedValue(undefined)
  useNexHostStore.setState({ byHost: { [A]: readyEntry, [B]: readyEntry, [C]: readyEntry }, ensure })
  useHostStore.setState({
    hosts: {
      [A]: { id: A, name: 'Mini Lab', ip: '1', port: 1, token: 't', order: 0 },
      [B]: { id: B, name: 'Air', ip: '2', port: 2, token: 't', order: 1 },
      [C]: { id: C, name: 'Intel', ip: '3', port: 3, token: 't', order: 2 },
    },
    hostOrder: [A, B, C], activeHostId: A, runtime: {},
  })
  useShownHostsStore.setState({ ids: [A, B, C] })
  vi.mocked(sse.openNexSse).mockReset().mockImplementation(() => ({ close: vi.fn() }))
  vi.mocked(api.listExecutions).mockReset().mockResolvedValue({ items: [], next_cursor: '' })
})
afterEach(() => vi.clearAllMocks())

describe('WorkerList', () => {
  it('root is a flex column', () => {
    render(<WorkerList />)
    expect(screen.getByTestId('worker-list')).toHaveClass('flex', 'flex-col')
  })

  it('one section per host, in hostOrder', () => {
    useHostStore.setState({ hostOrder: [C, A, B] })
    render(<WorkerList />)
    expect(sectionNames()).toEqual(['Intel', 'Mini Lab', 'Air'])
    expect(screen.queryByTestId('worker-list-none')).toBeNull()
  })

  it('a host hidden in this workbench gets no section', () => {
    useShownHostsStore.setState({ ids: [A, C] })
    render(<WorkerList />)
    expect(sectionNames()).toEqual(['Mini Lab', 'Intel'])
  })

  it('a disabled host gets no section; a loading host keeps its section', () => {
    useNexHostStore.setState({ byHost: { [A]: disabledEntry, [B]: loadingEntry, [C]: disabledEntry } })
    render(<WorkerList />)
    expect(sectionNames()).toEqual(['Air'])
    const section = screen.getByTestId('executions-view')
    expect(within(section).getByTestId('executions-phase-dot')).toHaveAttribute('data-phase', 'loading')
    expect(screen.queryByTestId('worker-list-none')).toBeNull()
  })

  it('a host never ensured (no phase yet) keeps its section and no none line', () => {
    useNexHostStore.setState({ byHost: { [A]: disabledEntry } })
    useShownHostsStore.setState({ ids: [A, B] })
    render(<WorkerList />)
    expect(sectionNames()).toEqual(['Air'])
    expect(screen.queryByTestId('worker-list-none')).toBeNull()
  })

  it('every shown host disabled → the none line, and no section', () => {
    useNexHostStore.setState({ byHost: { [A]: disabledEntry, [B]: disabledEntry, [C]: readyEntry } })
    useShownHostsStore.setState({ ids: [A, B] }) // C is ready but hidden: it does not count
    render(<WorkerList />)
    expect(screen.queryAllByTestId('executions-view')).toHaveLength(0)
    expect(screen.getByTestId('worker-list-none')).toHaveTextContent('No hosts with Nexen')
  })

  it('no shown host at all → the none line', () => {
    useShownHostsStore.setState({ ids: [] })
    render(<WorkerList />)
    expect(screen.queryAllByTestId('executions-view')).toHaveLength(0)
    expect(screen.getByTestId('worker-list-none')).toBeInTheDocument()
  })

  it('ensures every shown host — a disabled one too — and no hidden one', () => {
    useNexHostStore.setState({ byHost: { [A]: disabledEntry, [B]: disabledEntry, [C]: disabledEntry } })
    useShownHostsStore.setState({ ids: [A, B] })
    render(<WorkerList />)
    expect(ensure).toHaveBeenCalledWith(A)
    expect(ensure).toHaveBeenCalledWith(B)
    expect(ensure).not.toHaveBeenCalledWith(C)
  })

  it('a disabled host whose Nexen turns ready gets its section back, and the none line goes', () => {
    useNexHostStore.setState({ byHost: { [A]: disabledEntry } })
    useShownHostsStore.setState({ ids: [A] })
    render(<WorkerList />)
    expect(screen.getByTestId('worker-list-none')).toBeInTheDocument()
    act(() => { useNexHostStore.setState({ byHost: { [A]: readyEntry } }) })
    expect(sectionNames()).toEqual(['Mini Lab'])
    expect(screen.queryByTestId('worker-list-none')).toBeNull()
  })
})
