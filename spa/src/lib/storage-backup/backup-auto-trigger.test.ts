import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createBackupAutoTrigger } from './backup-auto-trigger'
import type { SupportsMutationEvents } from '../fs-backend'

function fakeBackend() {
  const listeners = new Set<() => void>()
  const backend: SupportsMutationEvents = {
    onMutation: (cb) => {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
  }
  return { backend, emit: () => listeners.forEach((cb) => cb()), size: () => listeners.size }
}

function fakeHostSub() {
  const listeners = new Set<() => void>()
  return {
    subscribe: (cb: () => void) => {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
    fire: () => listeners.forEach((cb) => cb()),
    size: () => listeners.size,
  }
}

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('createBackupAutoTrigger (T2b-6)', () => {
  it('debounces rapid mutations into one backupNow with the resolved host', () => {
    const fb = fakeBackend()
    const host = fakeHostSub()
    const backupNow = vi.fn()
    createBackupAutoTrigger({
      backend: fb.backend,
      resolveHostId: () => 'A',
      subscribeHost: host.subscribe,
      backupNow,
      delayMs: 2000,
    })

    fb.emit(); fb.emit(); fb.emit()
    vi.advanceTimersByTime(1999)
    expect(backupNow).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(backupNow).toHaveBeenCalledTimes(1)
    expect(backupNow).toHaveBeenCalledWith('A')
  })

  it('captures the mutation-time host (active-host switch cancels, never retargets)', () => {
    const fb = fakeBackend()
    const host = fakeHostSub()
    const backupNow = vi.fn()
    let active = 'A'
    createBackupAutoTrigger({
      backend: fb.backend,
      resolveHostId: () => active,
      subscribeHost: host.subscribe,
      backupNow,
      delayMs: 2000,
    })

    fb.emit() // schedules for A
    active = 'B' // active host switched
    host.fire() // host store changed → cancel the stale pending backup
    vi.advanceTimersByTime(2000)
    expect(backupNow).not.toHaveBeenCalled() // never backed up to A or B

    // A fresh mutation then schedules anew for the new host.
    fb.emit()
    vi.advanceTimersByTime(2000)
    expect(backupNow).toHaveBeenCalledTimes(1)
    expect(backupNow).toHaveBeenCalledWith('B')
  })

  it('cancels a pending backup when the hostOrder[0] fallback changes', () => {
    const fb = fakeBackend()
    const host = fakeHostSub()
    const backupNow = vi.fn()
    let fallback = 'A' // activeHostId null → resolves to hostOrder[0]
    createBackupAutoTrigger({
      backend: fb.backend,
      resolveHostId: () => fallback,
      subscribeHost: host.subscribe,
      backupNow,
      delayMs: 2000,
    })

    fb.emit() // schedules for A
    fallback = 'B' // hostOrder[0] changed
    host.fire()
    vi.advanceTimersByTime(2000)
    expect(backupNow).not.toHaveBeenCalled()
  })

  it('a host change that leaves the resolved host unchanged does NOT cancel', () => {
    const fb = fakeBackend()
    const host = fakeHostSub()
    const backupNow = vi.fn()
    createBackupAutoTrigger({
      backend: fb.backend,
      resolveHostId: () => 'A',
      subscribeHost: host.subscribe,
      backupNow,
      delayMs: 2000,
    })
    fb.emit()
    host.fire() // some unrelated host-store change; resolved host still 'A'
    vi.advanceTimersByTime(2000)
    expect(backupNow).toHaveBeenCalledTimes(1)
    expect(backupNow).toHaveBeenCalledWith('A')
  })

  it('does not schedule when no host resolves', () => {
    const fb = fakeBackend()
    const host = fakeHostSub()
    const backupNow = vi.fn()
    createBackupAutoTrigger({
      backend: fb.backend,
      resolveHostId: () => undefined,
      subscribeHost: host.subscribe,
      backupNow,
      delayMs: 2000,
    })
    fb.emit()
    vi.advanceTimersByTime(2000)
    expect(backupNow).not.toHaveBeenCalled()
  })

  it('dispose clears a pending timer and unsubscribes (no late backupNow)', () => {
    const fb = fakeBackend()
    const host = fakeHostSub()
    const backupNow = vi.fn()
    const trigger = createBackupAutoTrigger({
      backend: fb.backend,
      resolveHostId: () => 'A',
      subscribeHost: host.subscribe,
      backupNow,
      delayMs: 2000,
    })
    fb.emit()
    trigger.dispose()
    vi.advanceTimersByTime(2000)
    expect(backupNow).not.toHaveBeenCalled()
    expect(fb.size()).toBe(0) // mutation listener removed
    expect(host.size()).toBe(0) // host listener removed
  })
})
