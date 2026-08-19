// T5.1 — the placeholder registry: an EXPLICIT record of "we created this file
// eagerly and the user has never touched it".
//
// It exists because the fact cannot be inferred. The rejected predicate was
// `savedContent === '' && !isDirty && lastStat.size === 0`, which `markSaved`
// makes true for a file that HAD content, was deliberately emptied, and saved —
// the user's file, indistinguishable from an untouched reservation. So we record
// the fact at reservation time instead of guessing it later.
import { describe, it, expect, beforeEach, vi } from 'vitest'

const registerSpy = vi.hoisted(() => vi.fn())
const notifySpy = vi.hoisted(() => vi.fn())

vi.mock('../lib/storage/sync', () => ({
  syncManager: {
    register: registerSpy,
    notify: notifySpy,
    destroy: vi.fn(),
  },
  createSyncManager: vi.fn(),
}))

import { usePlaceholderFilesStore } from './usePlaceholderFilesStore'
import { STORAGE_KEYS } from '../lib/storage'
import type { FileSource } from '../types/fs'

const INAPP: FileSource = { type: 'inapp' }
const LOCAL: FileSource = { type: 'local' }
const DAEMON: FileSource = { type: 'daemon', hostId: 'host-A' }

beforeEach(() => {
  usePlaceholderFilesStore.setState({ paths: [] })
  localStorage.clear()
})

describe('usePlaceholderFilesStore — registration', () => {
  it('records an in-app reservation and reports it as a placeholder', () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')
    expect(usePlaceholderFilesStore.getState().isPlaceholder(INAPP, '/buffer/Untitled.md')).toBe(true)
    expect(usePlaceholderFilesStore.getState().paths).toEqual(['/buffer/Untitled.md'])
  })

  it('is idempotent — registering the same path twice keeps ONE entry', () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')
    expect(usePlaceholderFilesStore.getState().paths).toEqual(['/buffer/Untitled.md'])
  })

  it('NEVER records a remote (daemon) or local path — those files are not ours to delete', () => {
    usePlaceholderFilesStore.getState().register(DAEMON, '/home/wake/Untitled.md')
    usePlaceholderFilesStore.getState().register(LOCAL, '/Users/wake/Untitled.md')
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])
    expect(usePlaceholderFilesStore.getState().isPlaceholder(DAEMON, '/home/wake/Untitled.md')).toBe(false)
    expect(usePlaceholderFilesStore.getState().isPlaceholder(LOCAL, '/Users/wake/Untitled.md')).toBe(false)
  })

  it('never reports an in-app path as a placeholder for a non-in-app source', () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')
    expect(usePlaceholderFilesStore.getState().isPlaceholder(DAEMON, '/buffer/Untitled.md')).toBe(false)
    expect(usePlaceholderFilesStore.getState().isPlaceholder(LOCAL, '/buffer/Untitled.md')).toBe(false)
  })
})

describe('usePlaceholderFilesStore — deregistration is permanent', () => {
  it('drops the exact path', () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/a.md')
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/b.md')
    usePlaceholderFilesStore.getState().unregister(INAPP, '/buffer/a.md')
    expect(usePlaceholderFilesStore.getState().paths).toEqual(['/buffer/b.md'])
  })

  it('drops every descendant of a folder path (a folder rename / delete sweeps its contents)', () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/dir/x.md')
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/dir/sub/y.md')
    // Sibling prefix: `/buffer/dirty.md` must survive a `/buffer/dir` sweep.
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/dirty.md')
    usePlaceholderFilesStore.getState().unregister(INAPP, '/buffer/dir')
    expect(usePlaceholderFilesStore.getState().paths).toEqual(['/buffer/dirty.md'])
  })

  it('unregistering an unknown path is a no-op that keeps the same array reference', () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/a.md')
    const before = usePlaceholderFilesStore.getState().paths
    usePlaceholderFilesStore.getState().unregister(INAPP, '/buffer/zzz.md')
    expect(usePlaceholderFilesStore.getState().paths).toBe(before)
  })

  it('a non-in-app unregister never touches the in-app registry', () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/a.md')
    usePlaceholderFilesStore.getState().unregister(DAEMON, '/buffer/a.md')
    usePlaceholderFilesStore.getState().unregister(LOCAL, '/buffer/a.md')
    expect(usePlaceholderFilesStore.getState().paths).toEqual(['/buffer/a.md'])
  })

  it('clear() empties the whole registry (the restore invalidation)', () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/a.md')
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/b.md')
    usePlaceholderFilesStore.getState().clear()
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])
  })
})

describe('usePlaceholderFilesStore — cross-tab sync', () => {
  // The registry is an authorization to DELETE a file. A second tab that never
  // learns the entry was dropped keeps the stale authorization and sweeps a file
  // that has since become the user's — so registration here is not a nicety, it
  // is what stops a save in tab A from being undone by a close in tab B.
  it('registers itself with syncManager so another tab rehydrates on every write', () => {
    expect(registerSpy).toHaveBeenCalledWith(STORAGE_KEYS.PLACEHOLDER_FILES, usePlaceholderFilesStore)
  })
})

describe('usePlaceholderFilesStore — persistence', () => {
  it('writes the registry to localStorage under its own key', () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')
    const raw = localStorage.getItem(STORAGE_KEYS.PLACEHOLDER_FILES)
    expect(raw).toBeTruthy()
    expect(JSON.parse(raw!).state.paths).toEqual(['/buffer/Untitled.md'])
  })

  it('survives a rehydrate — a reservation is still a placeholder after a reload', async () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')
    const persisted = localStorage.getItem(STORAGE_KEYS.PLACEHOLDER_FILES)!
    // Simulate the reload: in-memory state gone, only the persisted bytes left.
    // (Resetting the state re-persists it, so put the pre-reload bytes back.)
    usePlaceholderFilesStore.setState({ paths: [] })
    localStorage.setItem(STORAGE_KEYS.PLACEHOLDER_FILES, persisted)
    await usePlaceholderFilesStore.persist.rehydrate()
    expect(usePlaceholderFilesStore.getState().isPlaceholder(INAPP, '/buffer/Untitled.md')).toBe(true)
  })
})
