import type { ViewProps } from '../lib/module-registry'
import { useI18nStore } from '../stores/useI18nStore'

/**
 * Session-scoped file tree — uses active terminal's cwd as root.
 * Deferred: requires daemon API endpoint GET /api/sessions/:code/cwd
 */
export function FileTreeSessionView({ isActive }: ViewProps) {
  void isActive
  const t = useI18nStore((s) => s.t)
  return (
    <div className="flex-1 flex items-center justify-center p-4 text-xs text-text-muted text-center">
      {t('file_tree.session_not_implemented')}
    </div>
  )
}
