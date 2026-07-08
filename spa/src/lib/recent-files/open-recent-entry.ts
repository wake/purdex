import type { PaneContent } from '../../types/tab'
import type { RecentFileEntry } from '../../stores/useRecentFilesStore'
import { getFsBackend } from '../fs-backend'
import { createDaemonBackendForHost } from '../fs-backend-daemon'
import { useHostStore } from '../../stores/useHostStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { useI18nStore } from '../../stores/useI18nStore'

/**
 * Re-open a recent entry in place via the section's `onSelect`. Best-effort:
 * a daemon entry is host-guarded (avoids getDaemonBase's wrong-host fallback)
 * and stat-checked; failures raise a toast instead of opening.
 */
export async function openRecentEntry(
  entry: RecentFileEntry,
  onSelect: (content: PaneContent) => void,
): Promise<void> {
  const t = useI18nStore.getState().t
  const content = { kind: entry.kind, source: entry.source, filePath: entry.path } as PaneContent

  const fail = () =>
    useUndoToast.getState().show(t('editor.recent.open_failed', { name: entry.name }))

  try {
    if (entry.source.type === 'daemon') {
      const hostId = entry.source.hostId
      const host = useHostStore.getState().hosts[hostId]
      if (!host) {
        useUndoToast.getState().show(t('editor.recent.host_gone', { host: hostId }))
        return
      }
      const stat = await createDaemonBackendForHost(hostId).stat(entry.path)
      if (stat.isDirectory) { fail(); return }
      onSelect(content)
      return
    }

    // local / inapp: stat via the registered backend when available.
    const backend = getFsBackend(entry.source)
    if (!backend) {
      // No backend (e.g. local outside Electron) — best-effort open.
      onSelect(content)
      return
    }
    const stat = await backend.stat(entry.path)
    if (stat.isDirectory) { fail(); return }
    onSelect(content)
  } catch {
    fail()
  }
}
