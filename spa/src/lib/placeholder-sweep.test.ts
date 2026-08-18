// T5.2 — the automatic sweep for placeholder files that were opened and never
// touched. This is the one code path in the app that deletes a user file
// WITHOUT the user asking, so every guard gets its own test.
//
// The correctness hinge is ORDERING: the decision is taken on the state AFTER
// `closePane` has removed the closing pane's own `paneState`. Checking before
// would always find that pane and never fire (dead feature); deleting on the
// unmount alone would fire while another pane still holds the buffer (data
// loss). The two tests that pin this are "deletes when the LAST pane closes"
// (fails if the check runs pre-close) and the pane-move coverage in
// `EditorPane.placeholder-cleanup.test.tsx` (fails if there is no check).
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { closePaneAndSweepPlaceholder } from './placeholder-sweep'
import { useEditorStore } from '../stores/useEditorStore'
import { usePlaceholderFilesStore } from '../stores/usePlaceholderFilesStore'
import { clearFsBackendRegistry, registerFsBackend, type FsBackend } from './fs-backend'
import { bufferKey } from './editor-buffer-key'
import type { FileSource } from '../types/fs'

const INAPP: FileSource = { type: 'inapp' }
const DAEMON: FileSource = { type: 'daemon', hostId: 'h1' }
const LOCAL: FileSource = { type: 'local' }

const PLACEHOLDER = '/buffer/Untitled.md'

function fakeBackend(id: string): FsBackend & { delete: ReturnType<typeof vi.fn> } {
  return {
    id,
    label: id,
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

let inapp: ReturnType<typeof fakeBackend>
let daemon: ReturnType<typeof fakeBackend>
let local: ReturnType<typeof fakeBackend>

/** Seed a loaded buffer at `path` held by `paneIds`. */
function openOn(source: FileSource, path: string, paneIds: string[], body = ''): string {
  const key = bufferKey(source, path)
  useEditorStore.getState().openBuffer(key, body, { language: 'markdown' })
  for (const paneId of paneIds) useEditorStore.getState().attachPane(paneId, key)
  return key
}

beforeEach(() => {
  clearFsBackendRegistry()
  inapp = fakeBackend('inapp')
  daemon = fakeBackend('daemon')
  local = fakeBackend('local')
  registerFsBackend('inapp', inapp)
  registerFsBackend('daemon', daemon)
  registerFsBackend('local', local)
  useEditorStore.getState().clearAllBuffers()
  usePlaceholderFilesStore.setState({ paths: [] })
})

describe('T5.2 — an untouched placeholder is swept when its last pane closes', () => {
  it('deletes the file and drops the registry entry', async () => {
    usePlaceholderFilesStore.getState().register(INAPP, PLACEHOLDER)
    const key = openOn(INAPP, PLACEHOLDER, ['pane-1'])

    closePaneAndSweepPlaceholder('pane-1', key, INAPP, PLACEHOLDER)

    expect(inapp.delete).toHaveBeenCalledTimes(1)
    expect(inapp.delete).toHaveBeenCalledWith(PLACEHOLDER)
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])
    // The close itself still happened — the sweep wraps it, it does not replace it.
    expect(useEditorStore.getState().paneStates['pane-1']).toBeUndefined()
    expect(useEditorStore.getState().buffers[key]).toBeUndefined()
  })

  it('takes the decision on POST-close state: the closing pane is already gone when delete fires', () => {
    usePlaceholderFilesStore.getState().register(INAPP, PLACEHOLDER)
    const key = openOn(INAPP, PLACEHOLDER, ['pane-1'])

    const paneStatesAtDelete: string[][] = []
    inapp.delete.mockImplementation(async () => {
      paneStatesAtDelete.push(Object.keys(useEditorStore.getState().paneStates))
    })

    closePaneAndSweepPlaceholder('pane-1', key, INAPP, PLACEHOLDER)

    // Fires at all → the check did not see the closing pane's own paneState.
    expect(inapp.delete).toHaveBeenCalledTimes(1)
    // …and pins WHY: by the time the delete is issued, `pane-1` is already out
    // of the store. A pre-`closePane` check would have found it and bailed.
    expect(paneStatesAtDelete).toEqual([[]])
  })

  it('does nothing when no backend can be resolved', () => {
    clearFsBackendRegistry()
    usePlaceholderFilesStore.getState().register(INAPP, PLACEHOLDER)
    const key = openOn(INAPP, PLACEHOLDER, ['pane-1'])

    expect(() => closePaneAndSweepPlaceholder('pane-1', key, INAPP, PLACEHOLDER)).not.toThrow()
    expect(useEditorStore.getState().paneStates['pane-1']).toBeUndefined()
  })
})

describe('T5.2 — a file that changed hands is never swept', () => {
  it('a saved placeholder is left alone — including one saved EMPTY', () => {
    // The save flow (T5.1) deregistered it. Its buffer still looks exactly like
    // an untouched reservation (empty, clean, 0 B) — which is precisely why the
    // sweep must consult the registry and never re-infer from buffer shape.
    usePlaceholderFilesStore.getState().register(INAPP, PLACEHOLDER)
    usePlaceholderFilesStore.getState().unregister(INAPP, PLACEHOLDER)
    const key = openOn(INAPP, PLACEHOLDER, ['pane-1'], '')
    useEditorStore.getState().markSaved(key, { mtime: 1, size: 0 })

    closePaneAndSweepPlaceholder('pane-1', key, INAPP, PLACEHOLDER)

    expect(inapp.delete).not.toHaveBeenCalled()
  })

  it('a renamed placeholder is left alone under EITHER path', () => {
    usePlaceholderFilesStore.getState().register(INAPP, PLACEHOLDER)
    // The rename flow (T5.1) deregisters both the old and the new path.
    usePlaceholderFilesStore.getState().unregister(INAPP, PLACEHOLDER)
    usePlaceholderFilesStore.getState().unregister(INAPP, '/buffer/notes.md')

    const oldKey = openOn(INAPP, PLACEHOLDER, ['pane-old'])
    closePaneAndSweepPlaceholder('pane-old', oldKey, INAPP, PLACEHOLDER)

    const newKey = openOn(INAPP, '/buffer/notes.md', ['pane-new'])
    closePaneAndSweepPlaceholder('pane-new', newKey, INAPP, '/buffer/notes.md')

    expect(inapp.delete).not.toHaveBeenCalled()
  })

  it('an unregistered ordinary file is left alone', () => {
    const key = openOn(INAPP, '/buffer/notes.md', ['pane-1'], 'real content')

    closePaneAndSweepPlaceholder('pane-1', key, INAPP, '/buffer/notes.md')

    expect(inapp.delete).not.toHaveBeenCalled()
  })
})

describe('T5.2 — a placeholder still open elsewhere is never swept', () => {
  it('closing the first of two panes deletes nothing; closing the last deletes', () => {
    usePlaceholderFilesStore.getState().register(INAPP, PLACEHOLDER)
    const key = openOn(INAPP, PLACEHOLDER, ['pane-1', 'pane-2'])

    closePaneAndSweepPlaceholder('pane-1', key, INAPP, PLACEHOLDER)

    expect(inapp.delete).not.toHaveBeenCalled()
    expect(usePlaceholderFilesStore.getState().paths).toEqual([PLACEHOLDER])
    expect(useEditorStore.getState().buffers[key]).toBeDefined()

    closePaneAndSweepPlaceholder('pane-2', key, INAPP, PLACEHOLDER)

    expect(inapp.delete).toHaveBeenCalledTimes(1)
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])
  })

  it('a pane that already rebound to another buffer does not take the placeholder with it', () => {
    usePlaceholderFilesStore.getState().register(INAPP, PLACEHOLDER)
    const key = openOn(INAPP, PLACEHOLDER, ['pane-1', 'pane-2'])
    // `pane-1` switched to a different file; `pane-2` still shows the placeholder.
    openOn(INAPP, '/buffer/other.md', ['pane-1'], 'other')

    // The stale EditorPane instance for the placeholder unmounts afterwards.
    closePaneAndSweepPlaceholder('pane-1', key, INAPP, PLACEHOLDER)

    expect(inapp.delete).not.toHaveBeenCalled()
    expect(useEditorStore.getState().buffers[key]).toBeDefined()
  })
})

describe('T5.2 — remote and local files are never swept', () => {
  it.each([
    ['daemon', DAEMON, '/home/wake/Untitled.md'],
    ['local', LOCAL, '/Users/wake/Untitled.md'],
  ] as const)('%s: identical shape, still untouchable', (_name, source, path) => {
    // Force the path into the registry directly — `register` refuses non-in-app
    // sources, so this models a registry that somehow carries the path anyway.
    // Even then the sweep must refuse: we never create these files eagerly and
    // must never delete them.
    usePlaceholderFilesStore.setState({ paths: [path] })
    const key = openOn(source, path, ['pane-1'])

    closePaneAndSweepPlaceholder('pane-1', key, source, path)

    expect(daemon.delete).not.toHaveBeenCalled()
    expect(local.delete).not.toHaveBeenCalled()
    expect(inapp.delete).not.toHaveBeenCalled()
    expect(usePlaceholderFilesStore.getState().paths).toEqual([path])
  })
})

describe('T5.2 — the sweep is best-effort housekeeping', () => {
  it('attaches a rejection handler to the delete promise', () => {
    // Not merely "does not throw": an unhandled rejection escaping an unmount
    // cleanup is exactly the failure mode this must not have. The probe proves
    // the caller took ownership of the promise's rejection path.
    usePlaceholderFilesStore.getState().register(INAPP, PLACEHOLDER)
    const key = openOn(INAPP, PLACEHOLDER, ['pane-1'])
    const attachedCatch = vi.fn()
    inapp.delete.mockReturnValue({
      catch: (handler: (reason: unknown) => void) => {
        attachedCatch()
        handler(new Error('storage on fire'))
        return Promise.resolve()
      },
    })

    expect(() => closePaneAndSweepPlaceholder('pane-1', key, INAPP, PLACEHOLDER)).not.toThrow()
    expect(attachedCatch).toHaveBeenCalledTimes(1)
  })

  it('a genuinely rejected delete leaves the teardown intact', async () => {
    usePlaceholderFilesStore.getState().register(INAPP, PLACEHOLDER)
    const key = openOn(INAPP, PLACEHOLDER, ['pane-1'])
    inapp.delete.mockRejectedValue(new Error('storage on fire'))

    expect(() => closePaneAndSweepPlaceholder('pane-1', key, INAPP, PLACEHOLDER)).not.toThrow()
    await Promise.resolve()

    // The pane still closed — housekeeping failure never blocks the teardown.
    expect(useEditorStore.getState().paneStates['pane-1']).toBeUndefined()
    // The entry is dropped either way: one automatic attempt, then the file is
    // ordinary and only the manual T4.2 sweep can reach it.
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])
  })

  it('swallows a delete that throws synchronously', () => {
    usePlaceholderFilesStore.getState().register(INAPP, PLACEHOLDER)
    const key = openOn(INAPP, PLACEHOLDER, ['pane-1'])
    inapp.delete.mockImplementation(() => {
      throw new Error('sync boom')
    })

    expect(() => closePaneAndSweepPlaceholder('pane-1', key, INAPP, PLACEHOLDER)).not.toThrow()
    expect(useEditorStore.getState().paneStates['pane-1']).toBeUndefined()
  })
})
