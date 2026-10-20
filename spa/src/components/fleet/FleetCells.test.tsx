import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { FleetCells } from './FleetCells'
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'
import { usePoolStore } from '../../lib/pool/store'
import { resetRememberedPoolHosts } from '../../lib/pool/use-monitor-hosts'
import { usePoolHostsStore } from '../../stores/usePoolHostsStore'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useFleetStore } from '../../lib/fleet/store'
import { parseFacts, parseHostsAnswer } from '../../lib/pool/parse'
import darwinJson from '../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../lib/pool/__fixtures__/pool_hosts_response.json'
import type { PoolHost } from '../../lib/pool/types'

vi.mock('../../features/workspace/lib/open-watch-area', () => ({ openWatchArea: vi.fn() }))

const A = '82b11c521df16c12'
const B = '0123456789abcdef'
const C = 'aaaaaaaaaaaaaaaa'
const facts = parseFacts(darwinJson)!
const base = parseHostsAnswer(hostsJson)![0]
const host = (host_id: string, alias: string, over: Partial<PoolHost> = {}): PoolHost => ({ ...base, host_id, alias, reachable: true, facts: { ...facts, at: Date.now() }, ...over }) as PoolHost
const answer = (hosts: PoolHost[]) => usePoolStore.setState({ hosts, sourceHostId: 'cfg-a' } as never)

beforeEach(() => {
  vi.mocked(openWatchArea).mockClear()
  resetRememberedPoolHosts()
  usePoolStore.setState({ start: vi.fn(), stop: vi.fn(), hosts: [], sourceHostId: null, lastFactsAt: {}, error: null } as never)
  usePoolHostsStore.setState({ order: [], looks: {} })
  useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {} } as never)
  useNexHostStore.setState({ byHost: {} } as never)
  useFleetStore.getState().reset()
})

describe('FleetCells — from the pool only', () => {
  it('nothing to show: nothing rendered', () => {
    const { container } = render(<FleetCells variant="wide" />)
    expect(container.firstChild).toBeNull()
  })
  it('one cell per pool host in the pool\'s order, in the strip of the variant', () => {
    answer([host(A, 'mlab'), host(B, 'mserv'), host(C, 'air26')])
    render(<FleetCells variant="wide" />)
    const strip = screen.getByTestId('fleet-cells')
    expect(strip.dataset.variant).toBe('wide')
    expect(within(strip).getAllByTestId(/^host-tile-/).map((e) => e.dataset.variant)).toEqual(['wide', 'wide', 'wide'])
    expect(within(strip).getAllByTestId(/^host-tile-/).map((e) => e.getAttribute('data-testid'))).toEqual([`host-tile-${A}`, `host-tile-${B}`, `host-tile-${C}`])
  })
  it('the stored order wins; the rest follow (M13)', () => {
    answer([host(A, 'mlab'), host(B, 'mserv'), host(C, 'air26')])
    usePoolHostsStore.setState({ order: [C, A] })
    render(<FleetCells variant="narrow" />)
    expect(screen.getAllByTestId(/^host-tile-/).map((e) => e.getAttribute('data-testid'))).toEqual([`host-tile-${C}`, `host-tile-${A}`, `host-tile-${B}`])
    expect(screen.getByTestId('fleet-cells').dataset.variant).toBe('narrow')
  })
  it('a host\'s colour and icon come from its look in the pool hosts store', () => {
    answer([host(A, 'mlab')])
    usePoolHostsStore.setState({ looks: { [A]: { icon: 'Cube', colors: { console: { main: { color: '#ff0000', alpha: 100 } } } as never } } })
    render(<FleetCells variant="wide" />)
    expect(screen.getByTestId(`host-badge-${A}`)).toBeTruthy()
  })
  it('a host that is not ok keeps its cell: the badge dimmed', () => {
    answer([host(A, 'mlab', { reachable: false, facts: null, liveness: 'host_down' })])
    render(<FleetCells variant="wide" />)
    expect(screen.getByTestId(`host-badge-dim-${A}`).className).toContain('opacity-40')
  })
  it('a fleet host the pool does not list is not drawn: the fleet is not read', () => {
    answer([host(A, 'mlab')])
    render(<FleetCells variant="wide" />)
    expect(screen.getAllByTestId(/^host-tile-/)).toHaveLength(1)
  })
  it('click opens the watcher area on that pool host (id and alias)', () => {
    answer([host(A, 'mlab'), host(B, 'mserv')])
    render(<FleetCells variant="wide" />)
    fireEvent.click(screen.getByTestId(`host-tile-${B}`))
    expect(openWatchArea).toHaveBeenCalledWith({ poolHost: { hostId: B, alias: 'mserv' } })
  })
})
