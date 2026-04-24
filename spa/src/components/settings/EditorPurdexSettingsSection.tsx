import { useId } from 'react'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'
import {
  useEditorSettingsStore,
  type TabSizeOption,
} from '../../stores/useEditorSettingsStore'
import { BufferListSection } from '../editor/BufferListSection'

interface Props {
  ctx: SettingsContextFor<'purdex'>
}

/**
 * Purdex-scope Editor settings section.
 *
 * Commit 1 transitional: also renders `<BufferListSection />` below the
 * preferences so users still have a way to manage `/buffer/*` entries while
 * the dedicated `editor-buffers` pane is being built (Commit 2). This
 * BufferListSection import disappears together with the file in Commit 2.
 *
 * Tiptap integration is deliberately out of scope for this PR — the note
 * below makes this explicit for users who expect the preferences to affect
 * rich-text editors too.
 */
export function EditorPurdexSettingsSection(_props: Props) {
  const tabSize = useEditorSettingsStore((s) => s.tabSize)
  const insertSpaces = useEditorSettingsStore((s) => s.insertSpaces)
  const wordWrap = useEditorSettingsStore((s) => s.wordWrap)
  const lineNumbers = useEditorSettingsStore((s) => s.lineNumbers)
  const minimap = useEditorSettingsStore((s) => s.minimap)
  const fontSize = useEditorSettingsStore((s) => s.fontSize)
  const setTabSize = useEditorSettingsStore((s) => s.setTabSize)
  const setInsertSpaces = useEditorSettingsStore((s) => s.setInsertSpaces)
  const setWordWrap = useEditorSettingsStore((s) => s.setWordWrap)
  const setLineNumbers = useEditorSettingsStore((s) => s.setLineNumbers)
  const setMinimap = useEditorSettingsStore((s) => s.setMinimap)
  const setFontSize = useEditorSettingsStore((s) => s.setFontSize)

  const tabSizeId = useId()
  const fontSizeId = useId()

  return (
    <div className="flex flex-col gap-6">
      <section className="flex flex-col gap-3">
        <h3 className="text-sm font-medium text-text-primary">Indentation</h3>

        <label className="flex items-center justify-between gap-4 text-sm text-text-secondary">
          <span>
            <span className="block">Tab size</span>
            <span className="block text-xs text-text-muted">
              Number of spaces a Tab character represents.
            </span>
          </span>
          <select
            id={tabSizeId}
            value={tabSize}
            onChange={(e) => setTabSize(Number(e.target.value) as TabSizeOption)}
            className="px-2 py-1 rounded-md bg-surface-muted text-text-primary border border-border-subtle focus:border-accent focus:outline-none text-sm"
          >
            <option value={2}>2</option>
            <option value={4}>4</option>
            <option value={8}>8</option>
          </select>
        </label>

        <label className="flex items-center justify-between gap-4 text-sm text-text-secondary">
          <span>
            <span className="block">Insert spaces</span>
            <span className="block text-xs text-text-muted">
              Insert spaces instead of the Tab character on key press.
            </span>
          </span>
          <input
            type="checkbox"
            checked={insertSpaces}
            onChange={(e) => setInsertSpaces(e.target.checked)}
            className="h-4 w-4"
          />
        </label>
      </section>

      <section className="flex flex-col gap-3">
        <h3 className="text-sm font-medium text-text-primary">Display</h3>

        <label className="flex items-center justify-between gap-4 text-sm text-text-secondary">
          <span>Word wrap</span>
          <input
            type="checkbox"
            checked={wordWrap === 'on'}
            onChange={(e) => setWordWrap(e.target.checked ? 'on' : 'off')}
            className="h-4 w-4"
          />
        </label>

        <label className="flex items-center justify-between gap-4 text-sm text-text-secondary">
          <span>Line numbers</span>
          <input
            type="checkbox"
            checked={lineNumbers === 'on'}
            onChange={(e) => setLineNumbers(e.target.checked ? 'on' : 'off')}
            className="h-4 w-4"
          />
        </label>

        <label className="flex items-center justify-between gap-4 text-sm text-text-secondary">
          <span>Minimap</span>
          <input
            type="checkbox"
            checked={minimap}
            onChange={(e) => setMinimap(e.target.checked)}
            className="h-4 w-4"
          />
        </label>

        <label className="flex items-center justify-between gap-4 text-sm text-text-secondary">
          <span>
            <span className="block">Font size</span>
            <span className="block text-xs text-text-muted">Clamped to 10–24.</span>
          </span>
          <input
            id={fontSizeId}
            type="number"
            min={10}
            max={24}
            step={1}
            value={fontSize}
            onChange={(e) => setFontSize(Number(e.target.value))}
            className="w-20 px-2 py-1 rounded-md bg-surface-muted text-text-primary border border-border-subtle focus:border-accent focus:outline-none text-sm"
          />
        </label>
      </section>

      <p className="text-xs text-text-muted">
        These settings apply to the Monaco code editor. Rich-text (Tiptap)
        integration arrives in a follow-up.
      </p>

      {/* Commit 1 transitional — removed together with BufferListSection in Commit 2. */}
      <section className="flex flex-col gap-2 pt-4 border-t border-border-subtle">
        <h3 className="text-sm font-medium text-text-primary">Buffers</h3>
        <BufferListSection />
      </section>
    </div>
  )
}
