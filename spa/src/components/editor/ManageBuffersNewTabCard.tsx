import { Stack } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { PaneContent } from '../../types/tab'

interface Props {
  onSelect: (content: PaneContent) => void
}

/**
 * NewTab card — transforms the current NewTab pane into an `editor-buffers`
 * pane (spec §4.7).  Does NOT call `openSingletonTab` directly; that
 * singleton entry point is reserved for the breadcrumb popover's
 * `Manage buffers...` link in Commit 3.
 */
export function ManageBuffersNewTabCard({ onSelect }: Props) {
  const t = useI18nStore((s) => s.t)

  return (
    <div className="flex gap-2">
      <button
        data-testid="manage-buffers-card"
        className="flex items-center gap-2 px-4 py-2 rounded-lg border border-border-subtle bg-surface-secondary hover:bg-surface-hover text-text-primary text-sm transition-colors"
        onClick={() => onSelect({ kind: 'editor-buffers' })}
      >
        <Stack size={16} />
        {t('newTab.editor.buffers.label')}
      </button>
    </div>
  )
}
