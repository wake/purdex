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

import {
  DEFAULT_EDITOR_SETTINGS,
  useEditorSettingsStore,
} from './useEditorSettingsStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

function resetStore() {
  // merge-mode reset — do NOT pass `true` (replace mode wipes actions).
  useEditorSettingsStore.setState({ ...DEFAULT_EDITOR_SETTINGS })
}

beforeEach(() => {
  localStorage.clear()
  resetStore()
})

describe('useEditorSettingsStore', () => {
  it('S1-1: defaults match spec', () => {
    const s = useEditorSettingsStore.getState()
    expect(s.tabSize).toBe(2)
    expect(s.insertSpaces).toBe(true)
    expect(s.wordWrap).toBe('on')
    expect(s.lineNumbers).toBe('on')
    expect(s.minimap).toBe(true)
    expect(s.fontSize).toBe(13)
    // P5: open-behavior toggles default ON — popup + auto layer-1 search.
    expect(s.popupOnMissingFile).toBe(true)
    expect(s.autoSearchLayer1).toBe(true)
  })

  it('S1-1b: setPopupOnMissingFile / setAutoSearchLayer1 toggle the store', () => {
    const { setPopupOnMissingFile, setAutoSearchLayer1 } = useEditorSettingsStore.getState()
    setPopupOnMissingFile(false)
    setAutoSearchLayer1(false)
    const s = useEditorSettingsStore.getState()
    expect(s.popupOnMissingFile).toBe(false)
    expect(s.autoSearchLayer1).toBe(false)
  })

  it('S1-2: setFontSize(5) clamps to 10', () => {
    useEditorSettingsStore.getState().setFontSize(5)
    expect(useEditorSettingsStore.getState().fontSize).toBe(10)
  })

  it('S1-3: setFontSize(30) clamps to 24', () => {
    useEditorSettingsStore.getState().setFontSize(30)
    expect(useEditorSettingsStore.getState().fontSize).toBe(24)
  })

  it('S1-4: setFontSize(NaN) falls back to default 13', () => {
    useEditorSettingsStore.getState().setFontSize(NaN)
    expect(useEditorSettingsStore.getState().fontSize).toBe(13)
  })

  it('S1-5: reset restores defaults after mutating all fields', () => {
    const {
      setTabSize,
      setInsertSpaces,
      setWordWrap,
      setLineNumbers,
      setMinimap,
      setFontSize,
      reset,
    } = useEditorSettingsStore.getState()
    setTabSize(8)
    setInsertSpaces(false)
    setWordWrap('off')
    setLineNumbers('off')
    setMinimap(false)
    setFontSize(18)
    reset()
    const s = useEditorSettingsStore.getState()
    expect(s.tabSize).toBe(2)
    expect(s.insertSpaces).toBe(true)
    expect(s.wordWrap).toBe('on')
    expect(s.lineNumbers).toBe('on')
    expect(s.minimap).toBe(true)
    expect(s.fontSize).toBe(13)
  })

  it('S1-6: setWordWrap("off") persists to localStorage under EDITOR_SETTINGS', () => {
    useEditorSettingsStore.getState().setWordWrap('off')
    const raw = localStorage.getItem(STORAGE_KEYS.EDITOR_SETTINGS)
    expect(raw).toBeTruthy()
    const parsed = JSON.parse(raw!)
    expect(parsed.state.wordWrap).toBe('off')
  })

  it('S1-7: rehydrate with invalid fields replaces them with defaults', async () => {
    localStorage.setItem(
      STORAGE_KEYS.EDITOR_SETTINGS,
      JSON.stringify({
        state: { tabSize: 99, wordWrap: 'maybe', fontSize: 'big' },
        version: 1,
      }),
    )
    await useEditorSettingsStore.persist.rehydrate()
    const s = useEditorSettingsStore.getState()
    expect(s.tabSize).toBe(2)
    expect(s.wordWrap).toBe('on')
    expect(s.fontSize).toBe(13)
  })

  it('S1-8: rehydrate with null persisted state — no throw, all defaults', async () => {
    localStorage.setItem(
      STORAGE_KEYS.EDITOR_SETTINGS,
      JSON.stringify({ state: null, version: 1 }),
    )
    await expect(useEditorSettingsStore.persist.rehydrate()).resolves.not.toThrow()
    const s = useEditorSettingsStore.getState()
    expect(s.tabSize).toBe(2)
    expect(s.insertSpaces).toBe(true)
    expect(s.wordWrap).toBe('on')
    expect(s.lineNumbers).toBe('on')
    expect(s.minimap).toBe(true)
    expect(s.fontSize).toBe(13)
  })

  // S1-9: regression guard for R4 F1 (WITHDRAWN as false positive).
  // Zustand's persist middleware passes the UNWRAPPED state (not the
  // `{state, version}` envelope) to `merge`. A future refactor that
  // accidentally reintroduces envelope-unwrapping — the originally-
  // prescribed "fix" — would break persist entirely. This test locks
  // in the happy-path rehydrate behavior so such a regression fails
  // loudly. See spec §4.9.6 and commit 803d737e for full context.
  //
  // Note: we can't use the public setters to seed localStorage and then
  // rely on setState to "simulate fresh memory" — Zustand's persist
  // middleware wraps setState to re-write localStorage on every call
  // (node_modules/zustand/esm/middleware.mjs:363-367), so a merge-mode
  // reset clobbers the envelope with defaults. Instead, we write the
  // envelope directly (mirroring S1-7/S1-8) and rehydrate from the
  // default in-memory state. This still exercises the merge path the
  // R4 diagnosis would have broken — if a future refactor pulls
  // `.state` off `persisted` inside `merge`, `persisted.state` is
  // `undefined`, sanitize returns `{}`, and rehydrate yields
  // all-defaults, failing the assertions.
  it('S1-9: happy-path rehydrate restores non-default persisted values', async () => {
    // Confirm baseline in-memory state is defaults.
    const before = useEditorSettingsStore.getState()
    expect(before.wordWrap).toBe('on')
    expect(before.tabSize).toBe(2)
    expect(before.fontSize).toBe(13)

    // Write the persisted envelope directly — same shape Zustand would
    // produce via `setItem` (see the existing S1-6 for evidence).
    localStorage.setItem(
      STORAGE_KEYS.EDITOR_SETTINGS,
      JSON.stringify({
        state: {
          tabSize: 4,
          insertSpaces: true,
          wordWrap: 'off',
          lineNumbers: 'on',
          minimap: true,
          fontSize: 18,
        },
        version: 1,
      }),
    )

    // Rehydrate. Under correct Zustand behavior, the unwrapped state
    // flows into `merge(persisted, current)` → sanitize picks up the
    // non-defaults → store restores them.
    await useEditorSettingsStore.persist.rehydrate()

    const s = useEditorSettingsStore.getState()
    expect(s.wordWrap).toBe('off')
    expect(s.tabSize).toBe(4)
    expect(s.fontSize).toBe(18)
    // Other fields stay at defaults (unchanged in envelope).
    expect(s.insertSpaces).toBe(true)
    expect(s.lineNumbers).toBe('on')
    expect(s.minimap).toBe(true)
  })
})
