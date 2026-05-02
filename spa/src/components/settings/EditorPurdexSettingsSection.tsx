import type { SettingsContextFor } from '../../lib/settings-contribution-types'
import { useI18nStore } from '../../stores/useI18nStore'
import {
  useEditorSettingsStore,
  type TabSizeOption,
} from '../../stores/useEditorSettingsStore'
import { SettingItem } from './SettingItem'
import { EditorOpenBehaviorSection } from './editor/EditorOpenBehaviorSection'
import { EditorLinkDetectionSection } from './editor/EditorLinkDetectionSection'

interface Props {
  ctx: SettingsContextFor<'purdex'>
}

/**
 * Purdex-scope Editor settings section.
 *
 * Renders Monaco preference controls using the canonical
 * `<h2>` + `<SettingItem>` layout shared with Appearance, Terminal,
 * Sync, etc. Spec §4.2 (PR-2) collapses the previously separate
 * `open-behavior` / `link-detect` sidebar entries into inline segments
 * within this page — `EditorOpenBehaviorSection` /
 * `EditorLinkDetectionSection` are reused verbatim, just mounted here
 * instead of registered as standalone contributions. Buffer CRUD lives
 * in the dedicated `EditorBuffersPane` (spec §4.5). Tiptap wiring is
 * deferred; see the trailing note.
 */
export function EditorPurdexSettingsSection(_props: Props) {
  const t = useI18nStore((s) => s.t)
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

  return (
    <div>
      <h2 className="text-lg text-text-primary">{t('settings.editor.title')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('settings.editor.desc')}</p>

      <SettingItem
        label={t('settings.editor.tab_size.label')}
        description={t('settings.editor.tab_size.desc')}
      >
        <select
          aria-label={t('settings.editor.tab_size.aria')}
          value={tabSize}
          onChange={(e) => setTabSize(Number(e.target.value) as TabSizeOption)}
          className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-20 hover:border-text-muted focus:border-border-active focus:outline-none"
        >
          <option value={2}>2</option>
          <option value={4}>4</option>
          <option value={8}>8</option>
        </select>
      </SettingItem>

      <SettingItem
        label={t('settings.editor.insert_spaces.label')}
        description={t('settings.editor.insert_spaces.desc')}
      >
        <input
          type="checkbox"
          checked={insertSpaces}
          onChange={(e) => setInsertSpaces(e.target.checked)}
          className="h-4 w-4"
        />
      </SettingItem>

      <SettingItem
        label={t('settings.editor.word_wrap.label')}
        description={t('settings.editor.word_wrap.desc')}
      >
        <input
          type="checkbox"
          checked={wordWrap === 'on'}
          onChange={(e) => setWordWrap(e.target.checked ? 'on' : 'off')}
          className="h-4 w-4"
        />
      </SettingItem>

      <SettingItem
        label={t('settings.editor.line_numbers.label')}
        description={t('settings.editor.line_numbers.desc')}
      >
        <input
          type="checkbox"
          checked={lineNumbers === 'on'}
          onChange={(e) => setLineNumbers(e.target.checked ? 'on' : 'off')}
          className="h-4 w-4"
        />
      </SettingItem>

      <SettingItem
        label={t('settings.editor.minimap.label')}
        description={t('settings.editor.minimap.desc')}
      >
        <input
          type="checkbox"
          checked={minimap}
          onChange={(e) => setMinimap(e.target.checked)}
          className="h-4 w-4"
        />
      </SettingItem>

      <SettingItem
        label={t('settings.editor.font_size.label')}
        description={t('settings.editor.font_size.desc')}
      >
        <input
          aria-label={t('settings.editor.font_size.aria')}
          type="number"
          min={10}
          max={24}
          step={1}
          value={fontSize}
          onChange={(e) => setFontSize(Number(e.target.value))}
          className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-20 hover:border-text-muted focus:border-border-active focus:outline-none"
        />
      </SettingItem>

      {/* PR-2 (spec §4.2): inlined segments. The two child components
          provide their own <h3> + description; no extra wrapping needed. */}
      <EditorOpenBehaviorSection />
      <EditorLinkDetectionSection />

      <p className="text-xs text-text-muted mt-4">
        {t('settings.editor.tiptap_note')}
      </p>
    </div>
  )
}
