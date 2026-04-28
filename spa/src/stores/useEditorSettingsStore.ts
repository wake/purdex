import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'

/**
 * User-controlled Monaco code-editor preferences.
 *
 * Scope: Purdex global. Not per-host / per-workspace; same editor preferences
 * apply across every `/buffer/*` document and any other Monaco instance.
 * Not registered with `syncManager` — editor preferences are a device-local
 * choice (the small-screen laptop may want fontSize 11 while the big monitor
 * uses 14) rather than shared config.
 *
 * Tiptap integration is explicitly out of scope for this PR — see spec §2
 * non-goals. Only `MonacoWrapper` reads from this store.
 */
export type WordWrapOption = 'on' | 'off'
export type LineNumbersOption = 'on' | 'off'
export type TabSizeOption = 2 | 4 | 8

const FONT_SIZE_MIN = 10
const FONT_SIZE_MAX = 24

export interface EditorSettingsState {
  tabSize: TabSizeOption
  insertSpaces: boolean
  wordWrap: WordWrapOption
  lineNumbers: LineNumbersOption
  minimap: boolean
  fontSize: number

  /**
   * P5 file-open flow: when a click target file does not exist (stat returns
   * ENOENT/404 only — see open-file.ts `isNotFoundError` for the strict
   * classifier), show the FileNotFound popup instead of throwing. Auth /
   * network errors always bubble regardless of this flag.
   */
  popupOnMissingFile: boolean

  /**
   * P5 layer-1 auto search: when popup is enabled and the file is missing,
   * automatically attempt to resolve via path cache (P4 hint pipeline) before
   * falling through to the popup's expand UI. Off → popup always renders the
   * `ask-expand` mode.
   */
  autoSearchLayer1: boolean

  setTabSize: (value: TabSizeOption) => void
  setInsertSpaces: (value: boolean) => void
  setWordWrap: (value: WordWrapOption) => void
  setLineNumbers: (value: LineNumbersOption) => void
  setMinimap: (value: boolean) => void
  setFontSize: (value: number) => void
  setPopupOnMissingFile: (value: boolean) => void
  setAutoSearchLayer1: (value: boolean) => void
  reset: () => void
}

export const DEFAULT_EDITOR_SETTINGS = {
  tabSize: 2 as TabSizeOption,
  insertSpaces: true,
  wordWrap: 'on' as WordWrapOption,
  lineNumbers: 'on' as LineNumbersOption,
  minimap: true,
  fontSize: 13,
  popupOnMissingFile: true,
  autoSearchLayer1: true,
} as const

function isTabSize(v: unknown): v is TabSizeOption {
  return v === 2 || v === 4 || v === 8
}

function isWordWrap(v: unknown): v is WordWrapOption {
  return v === 'on' || v === 'off'
}

function isLineNumbers(v: unknown): v is LineNumbersOption {
  return v === 'on' || v === 'off'
}

function clampFontSize(value: number): number {
  if (!Number.isFinite(value)) return DEFAULT_EDITOR_SETTINGS.fontSize
  const rounded = Math.round(value)
  return Math.min(FONT_SIZE_MAX, Math.max(FONT_SIZE_MIN, rounded))
}

/**
 * Silently replaces malformed entries with defaults.  A corrupted
 * localStorage payload (user hand-edit, failed migration, cross-version
 * rehydrate) must not leave Monaco stranded with `tabSize: 99` /
 * `wordWrap: 'maybe'` — those would surface as silent rendering glitches.
 */
function sanitize(raw: unknown): Partial<Pick<
  EditorSettingsState,
  'tabSize' | 'insertSpaces' | 'wordWrap' | 'lineNumbers' | 'minimap' | 'fontSize' | 'popupOnMissingFile' | 'autoSearchLayer1'
>> {
  if (raw === null || typeof raw !== 'object') return {}
  const src = raw as Record<string, unknown>
  const out: Partial<EditorSettingsState> = {}

  if (isTabSize(src.tabSize)) out.tabSize = src.tabSize
  if (typeof src.insertSpaces === 'boolean') out.insertSpaces = src.insertSpaces
  if (isWordWrap(src.wordWrap)) out.wordWrap = src.wordWrap
  if (isLineNumbers(src.lineNumbers)) out.lineNumbers = src.lineNumbers
  if (typeof src.minimap === 'boolean') out.minimap = src.minimap
  if (typeof src.fontSize === 'number' && Number.isFinite(src.fontSize)) {
    out.fontSize = clampFontSize(src.fontSize)
  }
  if (typeof src.popupOnMissingFile === 'boolean') out.popupOnMissingFile = src.popupOnMissingFile
  if (typeof src.autoSearchLayer1 === 'boolean') out.autoSearchLayer1 = src.autoSearchLayer1

  return out
}

export const useEditorSettingsStore = create<EditorSettingsState>()(
  persist(
    (set) => ({
      ...DEFAULT_EDITOR_SETTINGS,

      setTabSize: (tabSize) => set({ tabSize }),
      setInsertSpaces: (insertSpaces) => set({ insertSpaces }),
      setWordWrap: (wordWrap) => set({ wordWrap }),
      setLineNumbers: (lineNumbers) => set({ lineNumbers }),
      setMinimap: (minimap) => set({ minimap }),
      setFontSize: (value) => set({ fontSize: clampFontSize(value) }),
      setPopupOnMissingFile: (popupOnMissingFile) => set({ popupOnMissingFile }),
      setAutoSearchLayer1: (autoSearchLayer1) => set({ autoSearchLayer1 }),
      reset: () => set({ ...DEFAULT_EDITOR_SETTINGS }),
    }),
    {
      name: STORAGE_KEYS.EDITOR_SETTINGS,
      storage: purdexStorage,
      version: 1,
      // partialize identity — every field is a user preference worth persisting.
      partialize: (state) => ({
        tabSize: state.tabSize,
        insertSpaces: state.insertSpaces,
        wordWrap: state.wordWrap,
        lineNumbers: state.lineNumbers,
        minimap: state.minimap,
        fontSize: state.fontSize,
        popupOnMissingFile: state.popupOnMissingFile,
        autoSearchLayer1: state.autoSearchLayer1,
      }),
      merge: (persisted, current) => {
        const clean = sanitize(persisted)
        return { ...current, ...DEFAULT_EDITOR_SETTINGS, ...clean }
      },
    },
  ),
)
