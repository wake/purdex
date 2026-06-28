/**
 * download-file — the single source of truth for triggering a browser "save as"
 * download from an in-memory `Blob`.
 *
 * Lifted verbatim out of `settings/SyncSection.tsx` (Phase 1c decision 6) so the
 * sync export, the Storage download/export action, and the binary
 * open-disposition all share one implementation. The anchor + object-URL dance
 * works inside the Electron WebContents too, so no daemon/IPC save dialog is
 * needed.
 */
export function triggerDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}
