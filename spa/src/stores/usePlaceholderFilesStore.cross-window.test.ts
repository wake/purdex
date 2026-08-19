// The placeholder registry across TWO tabs, driven end to end.
//
// Why this suite exists at all: an entry in the registry is a standing
// authorization to DELETE a real file. The entry records that the path WAS an
// untouched placeholder at some past moment — so every event that ends that
// status has to reach every tab, or the tab that missed it keeps an
// authorization for a file that is now the user's and sweeps it on close.
//
// A single-process assertion ("register was called on syncManager") cannot show
// that; it only shows the wiring exists. So each test here builds two INDEPENDENT
// module graphs over one shared `localStorage` and one shared BroadcastChannel
// bus — the closest thing to two browser tabs a jsdom test can have — and lets
// window B run its real sweep against whatever state window A left behind.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { FileSource } from '../types/fs'
import type { FsBackend } from '../lib/fs-backend'

const INAPP: FileSource = { type: 'inapp' }
const PLACEHOLDER = '/buffer/Untitled.md'

/**
 * The bus the two windows share. The real `BroadcastChannel` delivers to every
 * OTHER channel of the same name and never to the sender, which is exactly the
 * asymmetry the sync manager relies on — a self-delivered message would make a
 * window rehydrate over its own fresh write.
 */
class FakeBroadcastChannel {
  static bus = new Set<FakeBroadcastChannel>()
  name: string
  onmessage: ((event: MessageEvent) => void) | null = null
  constructor(name: string) {
    this.name = name
    FakeBroadcastChannel.bus.add(this)
  }
  postMessage(data: unknown): void {
    for (const peer of FakeBroadcastChannel.bus) {
      if (peer === this || peer.name !== this.name) continue
      peer.onmessage?.({ data } as MessageEvent)
    }
  }
  close(): void {
    FakeBroadcastChannel.bus.delete(this)
  }
}

type Win = {
  store: typeof import('./usePlaceholderFilesStore').usePlaceholderFilesStore
  editor: typeof import('./useEditorStore').useEditorStore
  closePaneAndSweepPlaceholder: typeof import('../lib/placeholder-sweep').closePaneAndSweepPlaceholder
  bufferKey: typeof import('../lib/editor-buffer-key').bufferKey
  registerFsBackend: typeof import('../lib/fs-backend').registerFsBackend
  clearFsBackendRegistry: typeof import('../lib/fs-backend').clearFsBackendRegistry
}

/**
 * A fresh module registry = a fresh set of module singletons (its own store, its
 * own `syncManager`, its own channel) over the SAME globals. That is what makes
 * these two objects behave like two tabs rather than two references to one tab.
 */
async function openWindow(): Promise<Win> {
  vi.resetModules()
  const [placeholder, editor, sweep, key, fsBackend] = await Promise.all([
    import('./usePlaceholderFilesStore'),
    import('./useEditorStore'),
    import('../lib/placeholder-sweep'),
    import('../lib/editor-buffer-key'),
    import('../lib/fs-backend'),
  ])
  await placeholder.usePlaceholderFilesStore.persist.rehydrate()
  return {
    store: placeholder.usePlaceholderFilesStore,
    editor: editor.useEditorStore,
    closePaneAndSweepPlaceholder: sweep.closePaneAndSweepPlaceholder,
    bufferKey: key.bufferKey,
    registerFsBackend: fsBackend.registerFsBackend,
    clearFsBackendRegistry: fsBackend.clearFsBackendRegistry,
  }
}

/** Let the broadcast-driven `persist.rehydrate()` settle. */
async function flush(): Promise<void> {
  await Promise.resolve()
  await Promise.resolve()
}

function fakeBackend(): FsBackend & { delete: ReturnType<typeof vi.fn> } {
  return {
    id: 'inapp',
    label: 'inapp',
    available: () => true,
    read: vi.fn(),
    write: vi.fn(),
    stat: vi.fn(),
    list: vi.fn(),
    mkdir: vi.fn(),
    delete: vi.fn(async () => {}),
    rename: vi.fn(),
  } as unknown as FsBackend & { delete: ReturnType<typeof vi.fn> }
}

/** Open `path` in window `win` on a single pane, and return that pane's key. */
function openOn(win: Win, path: string, paneId: string): string {
  const key = win.bufferKey(INAPP, path)
  win.editor.getState().openBuffer(key, '', { language: 'markdown' })
  win.editor.getState().attachPane(paneId, key)
  return key
}

beforeEach(() => {
  FakeBroadcastChannel.bus.clear()
  localStorage.clear()
  vi.stubGlobal('BroadcastChannel', FakeBroadcastChannel)
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.resetModules()
})

describe('the registry reaches every tab', () => {
  it('a register in tab A shows up in tab B', async () => {
    const a = await openWindow()
    const b = await openWindow()

    a.store.getState().register(INAPP, PLACEHOLDER)
    await flush()

    expect(b.store.getState().paths).toEqual([PLACEHOLDER])
  })

  it('an unregister in tab A (the save) clears the entry in tab B', async () => {
    const a = await openWindow()
    const b = await openWindow()
    a.store.getState().register(INAPP, PLACEHOLDER)
    await flush()
    expect(b.store.getState().isPlaceholder(INAPP, PLACEHOLDER)).toBe(true)

    a.store.getState().unregister(INAPP, PLACEHOLDER)
    await flush()

    expect(b.store.getState().isPlaceholder(INAPP, PLACEHOLDER)).toBe(false)
    expect(b.store.getState().paths).toEqual([])
  })

  it('a clear() in tab A (the restore) empties the registry in tab B', async () => {
    const a = await openWindow()
    const b = await openWindow()
    a.store.getState().register(INAPP, PLACEHOLDER)
    await flush()

    a.store.getState().clear()
    await flush()

    expect(b.store.getState().paths).toEqual([])
  })
})

describe('a file that changed hands in tab A is never swept by tab B', () => {
  let backend: ReturnType<typeof fakeBackend>

  /**
   * The starting position these tests measure from: BOTH tabs hold the entry.
   * B's copy is seeded directly rather than inherited from A's broadcast — the
   * describe above already pins that direction, and depending on it here would
   * make these tests pass vacuously on a build with no sync at all (B would
   * simply never know the path and never sweep it).
   */
  async function twoTabsHoldingAPlaceholder(): Promise<{ a: Win; b: Win }> {
    const a = await openWindow()
    const b = await openWindow()
    a.store.getState().register(INAPP, PLACEHOLDER)
    b.store.setState({ paths: [PLACEHOLDER] })
    await flush()
    expect(a.store.getState().paths).toEqual([PLACEHOLDER])
    expect(b.store.getState().paths).toEqual([PLACEHOLDER])
    backend = fakeBackend()
    b.clearFsBackendRegistry()
    b.registerFsBackend('inapp', backend)
    return { a, b }
  }

  it('SAVED in A → closing the last pane in B leaves the file alone', async () => {
    const { a, b } = await twoTabsHoldingAPlaceholder()
    const key = openOn(b, PLACEHOLDER, 'pane-b')

    // Tab A saves: the file now holds the user's content and is theirs.
    a.store.getState().unregister(INAPP, PLACEHOLDER)
    await flush()

    b.closePaneAndSweepPlaceholder('pane-b', key, INAPP, PLACEHOLDER)

    expect(backend.delete).not.toHaveBeenCalled()
    expect(b.store.getState().paths).toEqual([])
  })

  it('RESTORED in A → closing the last pane in B leaves the file alone', async () => {
    const { a, b } = await twoTabsHoldingAPlaceholder()
    const key = openOn(b, PLACEHOLDER, 'pane-b')

    // A storage restore swapped the whole tree; every entry is invalid.
    a.store.getState().clear()
    await flush()

    b.closePaneAndSweepPlaceholder('pane-b', key, INAPP, PLACEHOLDER)

    expect(backend.delete).not.toHaveBeenCalled()
  })

  it('still sweeps when NOTHING ended the placeholder — the guard is the sync, not a blanket refusal', async () => {
    // The control for the two tests above: same setup, no deregistering event.
    // Without it they would also pass on a build where B simply never deletes.
    const { b } = await twoTabsHoldingAPlaceholder()
    const key = openOn(b, PLACEHOLDER, 'pane-b')

    b.closePaneAndSweepPlaceholder('pane-b', key, INAPP, PLACEHOLDER)

    expect(backend.delete).toHaveBeenCalledWith(PLACEHOLDER)
  })
})
