// 7a-3b: the pool host page — header, 現在, Worker (read only, with the link to the host tab's 設定), labels by group, toolchain.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'
import { PoolHostPage } from './PoolHostPage'
import type { PoolHostRow } from '../../lib/pool/use-pool-host-rows'
import { parseFacts, parseHostsAnswer } from '../../lib/pool/parse'
import darwinJson from '../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../lib/pool/__fixtures__/pool_hosts_response.json'
import { readHostPage, clearViewMemory } from '../../lib/watch/view-memory'

const openWatchArea = vi.fn()
vi.mock('../../features/workspace/lib/open-watch-area', () => ({ openWatchArea: (...a: unknown[]) => openWatchArea(...a) }))

const darwin = parseFacts(darwinJson)!
const baseHost = parseHostsAnswer(hostsJson)![0]
const host = (over: Record<string, unknown> = {}) => ({ ...baseHost, alias: 'mserv', version: '1.0.0-alpha.755', proto: 1, platform: 'linux', arch: 'amd64', labels: ['os:linux', 'arch:amd64', 'repo:purdex', 'x:custom'], facts: { ...darwin, os_version: 'Ubuntu 26.04' }, ...over })
const row = (over: Partial<PoolHostRow> = {}): PoolHostRow => ({
  id: 'p-mserv', name: 'mserv', kind: 'probe', look: { icon: 'Desktop', iconWeight: '', colors: undefined } as never,
  state: 'live', dot: 'live', zone: 'normal', worker: { enabled: true, slots: { heavy: 2, light: 4 }, running: { heavy: 1, light: 0 } },
  headroom: { heavy: 1, light: 4 }, version: '1.0.0-alpha.755', since: null, host: host() as never, ...over,
})

beforeEach(() => { cleanup(); openWatchArea.mockReset(); clearViewMemory() })
afterEach(() => cleanup())

describe('the pool host page', () => {
  it('the header: name, light, zone word, and wwd version · proto · platform · OS', () => {
    render(<PoolHostPage row={row()} />)
    expect(screen.getByTestId('pool-page-name').textContent).toBe('mserv')
    expect(screen.getByTestId('pool-page-status').textContent).toBe('Normal')
    expect(screen.getByTestId('pool-page-sub').textContent).toBe('wwd 1.0.0-alpha.755 · proto 1 · linux amd64 · Ubuntu 26.04')
  })

  it('a host the pool does not list: no sub line, the sections say unknown rather than invent', () => {
    render(<PoolHostPage row={row({ host: null, version: '', state: 'fallback', dot: 'warn', worker: null, headroom: null })} />)
    expect(screen.queryByTestId('pool-page-sub')).toBeNull()
    expect(screen.getByTestId('pool-page-worker').textContent).toContain('Unknown')
    expect(screen.getByTestId('pool-page-now').textContent).toContain('Unknown')
    expect(screen.getByTestId('pool-page-tools').textContent).toContain('Unknown')
  })

  it('現在 reads the facts: CPU, memory, pressure, disk', () => {
    render(<PoolHostPage row={row()} />)
    const now = screen.getByTestId('pool-page-now')
    expect(within(now).getByTestId('pool-page-cpu').textContent).toMatch(/CPU \d+%/)
    expect(within(now).getByTestId('pool-page-mem').textContent).toMatch(/Memory \d+%/)
    expect(within(now).getByTestId('pool-page-disk').textContent).toMatch(/Disk/)
  })

  it('Worker is read only: no switch, no slots input, and a link that opens the host tab on 設定', () => {
    render(<PoolHostPage row={row()} />)
    const w = screen.getByTestId('pool-page-worker')
    expect(w.textContent).toContain('On · heavy 1/2 · light 0/4')
    expect(within(w).queryByRole('checkbox')).toBeNull()
    expect(within(w).queryByRole('spinbutton')).toBeNull()
    fireEvent.click(within(w).getByTestId('pool-page-worker-settings'))
    expect(readHostPage('p-mserv')).toBe('settings')
    expect(openWatchArea).toHaveBeenCalledWith({ host: 'p-mserv' })
  })

  it('a worker that is off says so', () => {
    render(<PoolHostPage row={row({ worker: { enabled: false, slots: { heavy: 2, light: 4 }, running: { heavy: 0, light: 0 } }, headroom: { heavy: 0, light: 0 } })} />)
    expect(screen.getByTestId('pool-page-worker').textContent).toContain('Off')
  })

  it('labels by group: platform, repo, other; none → says none', () => {
    const { rerender } = render(<PoolHostPage row={row()} />)
    const labels = screen.getByTestId('pool-page-labels')
    expect(within(labels).getByTestId('pool-labels-platform').textContent).toContain('os:linux')
    expect(within(labels).getByTestId('pool-labels-platform').textContent).toContain('arch:amd64')
    expect(within(labels).getByTestId('pool-labels-repo').textContent).toContain('repo:purdex')
    expect(within(labels).getByTestId('pool-labels-other').textContent).toContain('x:custom')
    rerender(<PoolHostPage row={row({ host: host({ labels: [] }) as never })} />)
    expect(screen.getByTestId('pool-page-labels').textContent).toContain('None')
  })

  it('toolchain: a version, several versions, and a missing tool', () => {
    render(<PoolHostPage row={row({ host: host({ facts: { ...darwin, tools: { go: '1.26.0', php: ['7.4.33', '8.4.21'], pnpm: null } } }) as never })} />)
    const tools = screen.getByTestId('pool-page-tools')
    expect(within(tools).getByTestId('pool-tool-go').textContent).toContain('1.26.0')
    expect(within(tools).getByTestId('pool-tool-php').textContent).toContain('7.4.33, 8.4.21')
    expect(within(tools).getByTestId('pool-tool-pnpm').textContent).toContain('Not installed')
  })

  it('children sit under 「樣式與 SSH 備援」', () => {
    render(<PoolHostPage row={row()}><div data-testid="editor-here" /></PoolHostPage>)
    const sec = screen.getByTestId('pool-page-style')
    expect(sec.textContent).toContain('Style and SSH fallback')
    expect(within(sec).getByTestId('editor-here')).toBeTruthy()
  })

  it('no children → no style section (a host with a daemon sets its style in the overview)', () => {
    render(<PoolHostPage row={row({ kind: 'daemon' })} />)
    expect(screen.queryByTestId('pool-page-style')).toBeNull()
  })
})
