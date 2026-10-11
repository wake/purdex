// spa/src/components/settings/DeckTypeSettings.tsx — the type size and line height of the deck and the chat (c9d9c7-60), under Appearance.
// Each control sits on the built-in number until the reader moves it; "Reset" goes back to the built-in look. Applies at once (the pane roots read the store).
import { useI18nStore } from '../../stores/useI18nStore'
import {
  DECK_FONT_DEFAULT, DECK_FONT_MAX, DECK_FONT_MIN, DECK_LINE_DEFAULT, DECK_LINE_MAX, DECK_LINE_MIN, DECK_LINE_STEP,
  useDeckTypeStore,
} from '../../stores/useDeckTypeStore'
import { SettingItem } from './SettingItem'

const resetClass = 'rounded px-2 py-0.5 text-xs text-accent hover:underline disabled:cursor-default disabled:text-text-muted disabled:no-underline'

export function DeckTypeSettings() {
  const t = useI18nStore((s) => s.t)
  const fontSize = useDeckTypeStore((s) => s.fontSize)
  const lineHeight = useDeckTypeStore((s) => s.lineHeight)
  const { setFontSize, setLineHeight, resetFontSize, resetLineHeight } = useDeckTypeStore.getState()
  return (
    <div data-testid="deck-type-settings">
      <h3 className="mt-6 text-sm font-medium text-text-primary">{t('settings.appearance.deck_type.title')}</h3>
      <SettingItem label={t('settings.appearance.deck_type.font')} description={t('settings.appearance.deck_type.font_desc')}>
        <div className="flex items-center gap-2">
          <input
            type="range" aria-label={t('settings.appearance.deck_type.font')}
            min={DECK_FONT_MIN} max={DECK_FONT_MAX} step={1}
            value={fontSize ?? DECK_FONT_DEFAULT}
            onChange={(e) => setFontSize(Number(e.target.value))}
          />
          <span data-testid="deck-type-font-value" className="w-12 text-right text-xs text-text-secondary">{fontSize ?? DECK_FONT_DEFAULT} px</span>
          <button type="button" data-testid="deck-type-font-reset" className={resetClass} disabled={fontSize === null} onClick={resetFontSize}>{t('settings.appearance.deck_type.reset')}</button>
        </div>
      </SettingItem>
      <SettingItem label={t('settings.appearance.deck_type.line')} description={t('settings.appearance.deck_type.line_desc')}>
        <div className="flex items-center gap-2">
          <input
            type="range" aria-label={t('settings.appearance.deck_type.line')}
            min={DECK_LINE_MIN} max={DECK_LINE_MAX} step={DECK_LINE_STEP}
            value={lineHeight ?? DECK_LINE_DEFAULT}
            onChange={(e) => setLineHeight(Number(e.target.value))}
          />
          <span data-testid="deck-type-line-value" className="w-12 text-right text-xs text-text-secondary">{(lineHeight ?? DECK_LINE_DEFAULT).toFixed(2)}</span>
          <button type="button" data-testid="deck-type-line-reset" className={resetClass} disabled={lineHeight === null} onClick={resetLineHeight}>{t('settings.appearance.deck_type.reset')}</button>
        </div>
      </SettingItem>
    </div>
  )
}
