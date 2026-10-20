import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'

const openWatchArea = vi.fn()
vi.mock('../lib/open-watch-area', () => ({ openWatchArea: (...a: unknown[]) => openWatchArea(...a) }))
import { WatchShieldButton } from './WatchShieldButton'
import { useWatchStatusStore } from '../../../lib/watch/status-store'
import { useAlertsStore } from '../../../lib/pool/alerts-store'
import type { WatchStatus } from '../../../lib/watch/status-types'
import { useWorkspaceStore, WATCH_WORKSPACE_ID } from '../store'
import zh from '../../../locales/zh-TW.json'
import { useI18nStore } from '../../../stores/useI18nStore'

const t = (k: string) => useI18nStore.getState().t(k)
const status = (state: 'awake' | 'resting' | 'none'): WatchStatus => ({ self: { host_id: 'd1', state, since: '2026-10-16T01:00:00Z' }, enabled: true, auto_wake: true, mode: 'patrol', targets: [] })
const answer = (state: 'awake' | 'resting' | 'none') => useWatchStatusStore.setState({ byHost: { h1: { kind: 'ok', status: status(state), at: 1 } } })

beforeEach(() => {
  cleanup()
  openWatchArea.mockReset()
  useWatchStatusStore.getState().reset()
  useAlertsStore.getState().reset()
  useWorkspaceStore.getState().reset()
})

describe('WatchShieldButton', () => {
  it.each([
    ['none', 'watch.shield.none', 'outline', 'text-text-muted'],
    ['awake', 'watch.shield.awake', 'filled', 'text-status-success-text'],
    ['resting', 'watch.shield.resting', 'filled', 'text-status-success-text'],
  ] as const)('%s looks %s', (state, key, look, cls) => {
    answer(state)
    render(<WatchShieldButton />)
    const b = screen.getByTestId('watch-shield')
    expect(b).toHaveAttribute('title', t(key))
    expect(b).toHaveAttribute('data-look', look)
    expect(b.className).toContain(cls)
  })

  it('unknown (no host answered) is outline and says it cannot read the status', () => {
    render(<WatchShieldButton />)
    const b = screen.getByTestId('watch-shield')
    expect(b).toHaveAttribute('title', t('watch.shield.unknown'))
    expect(b).toHaveAttribute('data-look', 'outline')
    expect(b.className).toContain('text-text-muted')
  })

  it('the four titles are the ones the plan names', () => {
    const z = zh as Record<string, string>
    expect([z['watch.shield.none'], z['watch.shield.unknown'], z['watch.shield.awake'], z['watch.shield.resting']]).toEqual(['守門員（未啟動）', '守門員（讀不到狀態）', '守門員（醒著）', '守門員（休息中）'])
  })

  it('a click opens the area on the guardian tab', () => {
    answer('awake')
    render(<WatchShieldButton />)
    fireEvent.click(screen.getByTestId('watch-shield'))
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'guardian' })
  })

  it('is aria-pressed only while the watch workspace is active', () => {
    answer('awake')
    render(<WatchShieldButton />)
    expect(screen.getByTestId('watch-shield')).toHaveAttribute('aria-pressed', 'false')
    cleanup()
    useWorkspaceStore.getState().ensureWatchWorkspace()
    useWorkspaceStore.getState().setActiveWorkspace(WATCH_WORKSPACE_ID)
    render(<WatchShieldButton />)
    expect(screen.getByTestId('watch-shield')).toHaveAttribute('aria-pressed', 'true')
  })

  it('follows the status when it changes', () => {
    answer('none')
    render(<WatchShieldButton />)
    expect(screen.getByTestId('watch-shield')).toHaveAttribute('data-look', 'outline')
    act(() => answer('awake'))
    expect(screen.getByTestId('watch-shield')).toHaveAttribute('data-look', 'filled')
  })
})

describe('WatchShieldButton — the alert count (M36, M40)', () => {
  const al = (key: string, over: Record<string, unknown> = {}) => ({ key, kind: 'disk_low', level: 'push', since: 1, last: 2, clearedAt: null, detail: key, ...over })
  const ok = (alerts: ReturnType<typeof al>[]) => ({ kind: 'ok' as const, known: true, alerts: alerts as never, at: 1 })
  const title = (n: number) => `${t('watch.shield.awake')} · ${useI18nStore.getState().t('watch.shield.events', { n })}`

  it('shows no badge while nothing is active, and a click goes to the guardian tab', () => {
    answer('awake')
    useAlertsStore.setState({ byHost: { mlab: ok([al('d', { clearedAt: 5 }), al('o', { level: 'digest' })]) } })
    render(<WatchShieldButton />)
    expect(screen.queryByTestId('watch-shield-count')).toBeNull()
    fireEvent.click(screen.getByTestId('watch-shield'))
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'guardian' })
  })

  it('mlab and air26 both list silence:mserv, mlab has a disk_low push and a digest: 2, and a click opens 事件', () => {
    answer('awake')
    const silence = al('silence:mserv', { kind: 'silence', silence: 'host_down' })
    useAlertsStore.setState({ byHost: { mlab: ok([silence, al('disk_low:mlab'), al('orphans:mlab', { kind: 'orphans', level: 'digest' })]), air26: ok([silence]) } })
    render(<WatchShieldButton />)
    expect(screen.getByTestId('watch-shield-count')).toHaveTextContent('2')
    expect(screen.getByTestId('watch-shield')).toHaveAttribute('title', title(2))
    fireEvent.click(screen.getByTestId('watch-shield'))
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'events' })
  })

  it('the badge caps its figures at 99+ and the title says the number', () => {
    answer('awake')
    useAlertsStore.setState({ byHost: { mlab: ok(Array.from({ length: 120 }, (_, i) => al(`k${i}`))) } })
    render(<WatchShieldButton />)
    expect(screen.getByTestId('watch-shield')).toHaveAttribute('title', title(120))
    expect(screen.getByTestId('watch-shield-count')).toHaveTextContent('99+')
  })

  // journal v5 U9 shrinks the host cells' count badge only (§3.6): the shield's keeps its 12 px size and 4 px offset.
  it('the badge keeps its size: 12px, 8px figures, 4px out of the corner (not the host cell\'s small one)', () => {
    answer('awake')
    useAlertsStore.setState({ byHost: { mlab: ok([al('a')]) } })
    render(<WatchShieldButton />)
    const cls = screen.getByTestId('watch-shield-count').className.split(' ')
    for (const c of ['h-[12px]', 'min-w-[12px]', 'text-[8px]', '-top-[4px]', '-right-[4px]']) expect(cls).toContain(c)
    for (const c of ['h-[9px]', 'min-w-[9px]', 'text-[7px]', '-top-[5px]', '-right-[5px]']) expect(cls).not.toContain(c)
  })

  it('follows the count when it changes', () => {
    answer('none')
    useAlertsStore.setState({ byHost: { mlab: ok([al('a')]) } })
    render(<WatchShieldButton />)
    expect(screen.getByTestId('watch-shield-count')).toHaveTextContent('1')
    act(() => useAlertsStore.setState({ byHost: { mlab: ok([al('a', { clearedAt: 9 })]) } }))
    expect(screen.queryByTestId('watch-shield-count')).toBeNull()
  })
})
