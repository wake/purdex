// spa/src/components/LayoutKeepPicker.tsx — the dialogs of a title-bar layout change (shell cleanup spec §10, rule
// D.1a). `LayoutKeepPicker` is case 3: the user ticks exactly k of the content panes to keep, and the picker is the
// confirmation. `LayoutClosingList` names the panes that will close; the picker and the case-2 confirm both show it.
// Presentational: the caller applies the change.
import { useState } from 'react'
import { File } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { getPaneIcon } from '../lib/pane-labels'
import { paneDisplayLabelNow } from '../lib/pane-display-label'
import { ICON_MAP } from './tab-icon-map'
import { ConfirmDialog } from './ConfirmDialog'
import type { Pane } from '../types/tab'

function PaneLabel({ pane }: { pane: Pane }) {
  const t = useI18nStore((s) => s.t)
  const Icon = ICON_MAP[getPaneIcon(pane.content)] ?? File
  return (
    <>
      <Icon size={12} className="shrink-0 text-text-muted" />
      <span className="truncate">{paneDisplayLabelNow(pane.content, t)}</span>
    </>
  )
}

/**
 * "Will close:" and the panes, by display label and kind icon (`${testIdPrefix}-closing`). While an editor is among
 * them, a note that its unsaved changes are lost (`${testIdPrefix}-editor-note`).
 */
export function LayoutClosingList({ testIdPrefix, panes }: { testIdPrefix: string; panes: readonly Pane[] }) {
  const t = useI18nStore((s) => s.t)
  return (
    <div className="mt-2">
      <p className="text-xs text-text-secondary">{t('pane.layout_closing')}</p>
      <ul data-testid={`${testIdPrefix}-closing`} className="mt-1 space-y-0.5">
        {panes.map((p) => (
          <li key={p.id} className="flex items-center gap-1.5 text-xs text-text-primary min-w-0">
            <PaneLabel pane={p} />
          </li>
        ))}
      </ul>
      {panes.some((p) => p.content.kind === 'editor') && (
        <p data-testid={`${testIdPrefix}-editor-note`} className="mt-1 text-xs text-status-warning">
          {t('pane.layout_confirm_editor')}
        </p>
      )}
    </div>
  )
}

interface PickerProps {
  /** How many panes the new layout holds. */
  k: number
  /** Every content pane of the tab, in layout order. */
  candidates: readonly Pane[]
  /** Ticked when the picker opens. */
  preselected: readonly string[]
  onCancel: () => void
  /** The ticked pane ids; called only with exactly `k`. */
  onConfirm: (keepIds: string[]) => void
}

export function LayoutKeepPicker({ k, candidates, preselected, onCancel, onConfirm }: PickerProps) {
  const t = useI18nStore((s) => s.t)
  const [ticked, setTicked] = useState<readonly string[]>(preselected)
  const full = ticked.length >= k

  const toggle = (id: string) => {
    setTicked((cur) => {
      if (cur.includes(id)) return cur.filter((x) => x !== id)
      if (cur.length < k) return [...cur, id]
      // At the cap: with one slot the tick moves (a radio); with more, the extra tick is refused.
      return k === 1 ? [id] : cur
    })
  }

  return (
    <ConfirmDialog
      testIdPrefix="layout-keep"
      title={t('pane.layout_keep_title')}
      body={t('pane.layout_keep_body', { count: k })}
      confirmLabel={t('pane.layout_apply')}
      confirmDisabled={ticked.length !== k}
      onCancel={onCancel}
      onConfirm={() => { if (ticked.length === k) onConfirm([...ticked]) }}
    >
      <ul data-testid="layout-keep-options" className="mt-2 space-y-1">
        {candidates.map((p) => {
          const checked = ticked.includes(p.id)
          // With two or more slots a full set refuses another tick; with one, any box moves the tick.
          const refused = !checked && full && k > 1
          return (
            <li key={p.id}>
              <label className={`flex items-center gap-1.5 text-xs text-text-primary min-w-0 ${refused ? 'opacity-50' : 'cursor-pointer'}`}>
                <input
                  type="checkbox"
                  data-testid={`layout-keep-option-${p.id}`}
                  checked={checked}
                  disabled={refused}
                  onChange={() => toggle(p.id)}
                  className="accent-accent shrink-0"
                />
                <PaneLabel pane={p} />
              </label>
            </li>
          )
        })}
      </ul>
      <LayoutClosingList testIdPrefix="layout-keep" panes={candidates.filter((p) => !ticked.includes(p.id))} />
    </ConfirmDialog>
  )
}
