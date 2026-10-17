import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'

const openWatchArea = vi.fn()
vi.mock('../lib/open-watch-area', () => ({ openWatchArea: (...a: unknown[]) => openWatchArea(...a) }))
import { WatchShieldButton } from './WatchShieldButton'
import { useWatchStatusStore } from '../../../lib/watch/status-store'
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
