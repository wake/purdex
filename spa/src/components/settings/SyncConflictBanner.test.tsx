import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SyncConflictBanner } from './SyncConflictBanner'
import type { ConflictItem, SyncBundle } from '../../lib/sync/types'

const makeBundle = (): SyncBundle => ({ version: 1, timestamp: 1000, device: 'MacBook', collections: {} })

const mkConflict = (c: string, f: string, l: unknown, r: unknown): ConflictItem => ({
  contributor: c,
  field: f,
  lastSynced: 'baseline',
  local: l,
  remote: { value: r, device: 'MacBook' },
})

describe('SyncConflictBanner', () => {
  it('collapsed: shows count + view details button', () => {
    const conflicts = [mkConflict('prefs', 'theme', 'dark', 'light')]
    render(
      <SyncConflictBanner
        conflicts={conflicts}
        remoteBundle={makeBundle()}
        pendingAt={Date.now()}
        onResolve={vi.fn()}
        onDismiss={vi.fn()}
      />,
    )
    expect(screen.getByText(/1/)).toBeTruthy()
    expect(screen.getByRole('button', { name: /view details|查看詳情/i })).toBeTruthy()
  })

  it('expanded: shows per-row with local + remote radios', () => {
    const conflicts = [
      mkConflict('prefs', 'theme', 'dark', 'light'),
      mkConflict('layout', 'tabPos', 'top', 'bottom'),
    ]
    render(
      <SyncConflictBanner
        conflicts={conflicts}
        remoteBundle={makeBundle()}
        pendingAt={Date.now()}
        onResolve={vi.fn()}
        onDismiss={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /view details|查看詳情/i }))
    const radios = screen.getAllByRole('radio')
    expect(radios.length).toBe(4)
  })

  it('apply is disabled until every row has a choice', () => {
    const conflicts = [
      mkConflict('prefs', 'theme', 'dark', 'light'),
      mkConflict('layout', 'tabPos', 'top', 'bottom'),
    ]
    render(
      <SyncConflictBanner
        conflicts={conflicts}
        remoteBundle={makeBundle()}
        pendingAt={Date.now()}
        onResolve={vi.fn()}
        onDismiss={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /view details|查看詳情/i }))
    const applyBtn = screen.getByRole('button', { name: /apply|套用/i }) as HTMLButtonElement
    expect(applyBtn.disabled).toBe(true)
    const radios = screen.getAllByRole('radio')
    fireEvent.click(radios[0])
    expect(applyBtn.disabled).toBe(true)
    fireEvent.click(radios[2])
    expect(applyBtn.disabled).toBe(false)
  })

  it('keep-all-local fills every row with local', () => {
    const conflicts = [
      mkConflict('prefs', 'theme', 'dark', 'light'),
      mkConflict('layout', 'tabPos', 'top', 'bottom'),
    ]
    const onResolve = vi.fn()
    render(
      <SyncConflictBanner
        conflicts={conflicts}
        remoteBundle={makeBundle()}
        pendingAt={Date.now()}
        onResolve={onResolve}
        onDismiss={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /view details|查看詳情/i }))
    fireEvent.click(screen.getByRole('button', { name: /keep all local|全部保留本地/i }))
    fireEvent.click(screen.getByRole('button', { name: /apply|套用/i }))
    expect(onResolve).toHaveBeenCalledWith({ theme: 'local', tabPos: 'local' })
  })

  it('use-all-remote fills every row with remote', () => {
    const conflicts = [mkConflict('prefs', 'theme', 'dark', 'light')]
    const onResolve = vi.fn()
    render(
      <SyncConflictBanner
        conflicts={conflicts}
        remoteBundle={makeBundle()}
        pendingAt={Date.now()}
        onResolve={onResolve}
        onDismiss={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /view details|查看詳情/i }))
    fireEvent.click(screen.getByRole('button', { name: /use all remote|全部採用遠端/i }))
    fireEvent.click(screen.getByRole('button', { name: /apply|套用/i }))
    expect(onResolve).toHaveBeenCalledWith({ theme: 'remote' })
  })

  it('cancel calls onDismiss but not onResolve', () => {
    const onResolve = vi.fn()
    const onDismiss = vi.fn()
    render(
      <SyncConflictBanner
        conflicts={[mkConflict('prefs', 'theme', 'dark', 'light')]}
        remoteBundle={makeBundle()}
        pendingAt={Date.now()}
        onResolve={onResolve}
        onDismiss={onDismiss}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /view details|查看詳情/i }))
    fireEvent.click(screen.getByRole('button', { name: /cancel|取消/i }))
    expect(onDismiss).toHaveBeenCalled()
    expect(onResolve).not.toHaveBeenCalled()
  })

  it('pendingAt older than 24h shows stale warning', () => {
    const stalePending = Date.now() - 25 * 60 * 60 * 1000
    render(
      <SyncConflictBanner
        conflicts={[mkConflict('prefs', 'theme', 'dark', 'light')]}
        remoteBundle={makeBundle()}
        pendingAt={stalePending}
        onResolve={vi.fn()}
        onDismiss={vi.fn()}
      />,
    )
    expect(screen.getByText(/24.*(hour|小時)/i)).toBeTruthy()
  })

  it('plural: count === 1 uses singular banner form', () => {
    render(
      <SyncConflictBanner
        conflicts={[mkConflict('prefs', 'theme', 'dark', 'light')]}
        remoteBundle={makeBundle()}
        pendingAt={Date.now()}
        onResolve={vi.fn()}
        onDismiss={vi.fn()}
      />,
    )
    // "⚠ 1 field conflict" — note: no trailing 's'
    expect(screen.getByText(/1 field conflict$/)).toBeTruthy()
    expect(screen.queryByText(/conflicts/)).toBeNull()
  })

  it('plural: count > 1 uses plural banner form', () => {
    const conflicts = [
      mkConflict('prefs', 'theme', 'dark', 'light'),
      mkConflict('layout', 'tabPos', 'top', 'bottom'),
    ]
    render(
      <SyncConflictBanner
        conflicts={conflicts}
        remoteBundle={makeBundle()}
        pendingAt={Date.now()}
        onResolve={vi.fn()}
        onDismiss={vi.fn()}
      />,
    )
    expect(screen.getByText(/2 field conflicts/)).toBeTruthy()
  })

  it('collision: two rows with same field name flatten to one entry (later wins)', () => {
    const conflicts = [
      mkConflict('prefs', 'theme', 'dark', 'light'),
      mkConflict('layout', 'theme', 'compact', 'comfortable'),
    ]
    const onResolve = vi.fn()
    render(
      <SyncConflictBanner
        conflicts={conflicts}
        remoteBundle={makeBundle()}
        pendingAt={Date.now()}
        onResolve={onResolve}
        onDismiss={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /view details|查看詳情/i }))
    const radios = screen.getAllByRole('radio')
    fireEvent.click(radios[0])
    fireEvent.click(radios[3])
    fireEvent.click(screen.getByRole('button', { name: /apply|套用/i }))
    expect(onResolve).toHaveBeenCalledWith({ theme: 'remote' })
  })
})
