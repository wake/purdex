import type { ViewProps } from '../lib/module-registry'

/**
 * Session-scoped file tree — uses active terminal's cwd as root.
 * Deferred: requires daemon API endpoint GET /api/sessions/:code/cwd
 */
export function FileTreeSessionView({ isActive }: ViewProps) {
  void isActive
  return (
    <div className="flex-1 flex items-center justify-center p-4 text-xs text-text-muted text-center">
      Session file tree 尚未實作（需 daemon cwd API）
    </div>
  )
}
