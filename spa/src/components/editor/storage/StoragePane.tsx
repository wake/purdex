import { useCallback, useMemo, useRef, useState, type ReactNode } from 'react'
import {
  DndContext,
  PointerSensor,
  closestCenter,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core'
import { Broom, DownloadSimple, FilePlus, FolderPlus, PencilSimple, Stack, Trash, FolderOpen, UploadSimple } from '@phosphor-icons/react'
import type { PaneRendererProps } from '../../../lib/module-registry'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useUndoToast } from '../../../stores/useUndoToast'
import { useWorkspaceStore } from '../../../features/workspace/store'
import { useStorageTree } from '../../../hooks/useStorageTree'
import { findPane } from '../../../lib/pane-tree'
import { openInAppFile } from '../../../lib/open-in-app-file'
import { STORAGE_ROOT, basename, join, parentOf } from '../../../lib/storage-paths'
import { findNode, targetDirOf } from '../../../lib/storage-tree'
import type { TreeNode } from '../../../lib/storage-tree'
import { RenamePopover } from '../../RenamePopover'
import { BackupStatusSidebar } from './BackupStatusSidebar'
import { StorageTree } from './StorageTree'
import {
  createStorageFile,
  createStorageFolder,
  deleteStorageEntries,
  downloadStorageFile,
  findEmptyFiles,
  moveStorageEntry,
  renameStorageEntry,
  uploadFiles,
  type DeleteOutcome,
} from './storage-actions'
import { computeMoveFromDragEnd } from './storage-dnd'

/**
 * Resolve the workspace that hosts this Storage pane, so opened files land in
 * the right workspace (`openInAppFile` → `computeClusterInsertTarget` /
 * `insertTab` are workspace-scoped). We walk the tab layouts to find which tab
 * owns `paneId` (same inline scan as `EditorPane.findTabIdForPane`), then map
 * tab → workspace. Returns `null` when the pane has no owning workspace — we do
 * NOT guess the active workspace (R2-2): `openInAppFile` refuses a null id
 * rather than land the tab in the wrong place.
 */
function resolveWorkspaceId(paneId: string): string | null {
  const wsState = useWorkspaceStore.getState()
  const { tabs } = useTabStore.getState()
  for (const [tabId, tab] of Object.entries(tabs)) {
    if (findPane(tab.layout, paneId)) {
      return wsState.findWorkspaceByTab(tabId)?.id ?? null
    }
  }
  return null
}

/**
 * The scrollable tree region, made a single `useDroppable` drop target keyed by
 * `STORAGE_ROOT` (drop here → move to the storage root). It must live in its own
 * component because `useDroppable` has to run *inside* the `DndContext` that
 * `StoragePane` renders. `closestCenter` collision detection lets a nested
 * folder droppable win when the pointer is over it, falling back to this root
 * zone for empty space / file rows.
 */
function StorageRegionDropZone({
  targetDir,
  onNativeDragOver,
  onNativeDrop,
  children,
}: {
  targetDir: string
  onNativeDragOver: (e: React.DragEvent<HTMLDivElement>) => void
  onNativeDrop: (e: React.DragEvent<HTMLDivElement>) => void
  children: ReactNode
}) {
  // Publish `STORAGE_ROOT` as this zone's authoritative drop target dir (codex
  // B1) so a drop on empty space / between rows resolves to the storage root via
  // the same `over.data.targetDir` channel the row droppables use.
  const { setNodeRef, isOver } = useDroppable({
    id: STORAGE_ROOT,
    data: { targetDir: STORAGE_ROOT },
  })
  return (
    <div
      ref={setNodeRef}
      className={'flex-1 overflow-y-auto' + (isOver ? ' bg-surface-hover/50' : '')}
      data-testid="storage-tree-region"
      data-target-dir={targetDir}
      data-root-over={isOver ? 'true' : 'false'}
      // Native OS-file drop (Phase 1c decision 1). HTML5 drag events carrying
      // `DataTransfer.files` are a SEPARATE event stream from dnd-kit's pointer
      // events, so these handlers only fire for an OS-file drag; an internal
      // node move (no files) is ignored and left entirely to dnd-kit.
      onDragOver={onNativeDragOver}
      onDrop={onNativeDrop}
    >
      {children}
    </div>
  )
}

/**
 * StoragePane — management UI for In-App `/buffer/*` entries (spec §4.5),
 * restructured into a nested tree (`StorageTree`/`StorageRow`) + extracted CRUD
 * (`storage-actions`). Two-region shell: the tree on the left, a placeholder
 * Backups sidebar on the right (subsystem 2 fills it later).
 *
 * Open routes through `openInAppFile` (registry-resolved kind: md→editor,
 * png→image-preview, pdf→pdf-preview; open-or-focus, no cross-tab hijack).
 * Rename/delete operate on the selected node's FULL path; each row also carries
 * its own Open/Rename/Delete cluster (T4.1) that targets that row regardless of
 * the selection, routed through the same `renameStorageEntry` /
 * `deleteStorageEntries` actions and the same rename popover.
 */
export function StoragePane({ pane }: PaneRendererProps) {
  const t = useI18nStore((s) => s.t)
  const { tree, loading, error: treeError, refresh, expanded, toggle } = useStorageTree()

  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [actionError, setActionError] = useState<string | null>(null)
  // Upload-only soft warning (over the size cap) — rendered amber, below errors.
  // Kept separate from `actionError` (red) so a too-large rejection reads as a
  // recoverable warning, not a hard failure (T1c-4).
  const [actionWarning, setActionWarning] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // The 0 B files a cleanup scan found, held while the confirmation dialog is
  // up (`null` = no dialog). T4.2.
  const [cleanupCandidates, setCleanupCandidates] = useState<string[] | null>(null)
  const [renameTarget, setRenameTarget] = useState<string | null>(null)
  const [renameError, setRenameError] = useState<string | null>(null)
  const [renameAnchorRect, setRenameAnchorRect] = useState<DOMRect | null>(null)
  const renameAnchorRef = useRef<HTMLButtonElement | null>(null)
  const uploadInputRef = useRef<HTMLInputElement | null>(null)

  const error = actionError ?? treeError

  const selectedArray = useMemo(() => Array.from(selected), [selected])
  const singleSelected = selectedArray.length === 1 ? selectedArray[0] : null

  // Resolve the single selection back to its TreeNode so we know whether it is a
  // folder, then derive the directory that nesting-aware actions (new file / new
  // folder / drop) should target (T1b-0): folder → itself, file → parent, no
  // single selection → storage root. Exposed on the tree region below so later
  // 1b tasks (and tests) can consume it.
  const selectedNode = useMemo(
    () => (singleSelected ? findNode(tree, singleSelected) : null),
    [singleSelected, tree],
  )
  const targetDir = useMemo(() => targetDirOf(selectedNode), [selectedNode])

  // --- Selection / open ---

  // Plain click → select ONLY this row (replace selection). Modifier click
  // (cmd/ctrl/shift, surfaced as `additive`) → toggle into a multi-selection
  // (codex B5). A plain click never deselects, so the click→click→double-click
  // sequence leaves the row selected instead of toggling it off and stranding a
  // stale rename/new/move target.
  const handleSelect = useCallback((path: string, additive: boolean) => {
    setSelected((prev) => {
      if (!additive) return new Set([path])
      const next = new Set(prev)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })
  }, [])

  // --- Visible batch selection (T4.3) ---
  //
  // The checkbox column, the header select-all and the action bar are pure UI
  // over the SAME `selected` set the modifier-click path already wrote to — no
  // second selection model, so the two gestures compose.

  /** Every row currently rendered: top level plus the children of expanded dirs. */
  const visiblePaths = useMemo(() => {
    const out: string[] = []
    const walk = (nodes: TreeNode[]) => {
      for (const node of nodes) {
        out.push(node.path)
        if (node.isDir && expanded.has(node.path) && node.children) walk(node.children)
      }
    }
    walk(tree)
    return out
  }, [tree, expanded])

  const allVisibleSelected = visiblePaths.length > 0 && visiblePaths.every((p) => selected.has(p))
  const someVisibleSelected = visiblePaths.some((p) => selected.has(p))

  // A row checkbox is exactly an additive select — same reducer, same set.
  const handleToggleRowSelect = useCallback(
    (path: string) => handleSelect(path, true),
    [handleSelect],
  )

  const handleToggleSelectAll = useCallback(() => {
    setSelected((prev) => {
      const everySelected = visiblePaths.length > 0 && visiblePaths.every((p) => prev.has(p))
      return everySelected ? new Set<string>() : new Set(visiblePaths)
    })
  }, [visiblePaths])

  const handleClearSelection = useCallback(() => setSelected(new Set()), [])

  // `indeterminate` is a DOM property with no JSX attribute, so it is written
  // through a callback ref that re-runs whenever the derived state changes.
  const selectAllRef = useCallback(
    (el: HTMLInputElement | null) => {
      if (el) el.indeterminate = someVisibleSelected && !allVisibleSelected
    },
    [someVisibleSelected, allVisibleSelected],
  )

  const handleOpen = useCallback(
    async (path: string) => {
      // Pass the possibly-null workspace id straight through (R2-2). When the
      // open is aborted — refused, or stat-gated because the entry is stale
      // (R2-1) — refresh the tree so the missing row disappears.
      const tabId = await openInAppFile(path, resolveWorkspaceId(pane.id))
      if (!tabId) refresh()
    },
    [pane.id, refresh],
  )

  // --- Actions ---

  const handleNew = useCallback(async () => {
    setBusy(true)
    setActionError(null)
    setActionWarning(null)
    // T1b-0 wiring: the new file lands in the selected folder (or the parent of
    // a selected file, else the storage root).
    const res = await createStorageFile(targetDir)
    setBusy(false)
    if (res.error) setActionError(res.error)
    else refresh()
  }, [refresh, targetDir])

  const handleNewFolder = useCallback(async () => {
    setBusy(true)
    setActionError(null)
    setActionWarning(null)
    const res = await createStorageFolder(targetDir)
    setBusy(false)
    if ('error' in res) {
      setActionError(res.error)
      return
    }
    // Auto-expand the (empty) new folder and select it so a follow-up New File
    // immediately targets it, then refresh to materialize the row.
    if (!expanded.has(res.path)) toggle(res.path)
    setSelected(new Set([res.path]))
    refresh()
  }, [refresh, targetDir, expanded, toggle])

  const handleOpenRename = useCallback(() => {
    if (!singleSelected) return
    // Capture the anchor rect here (event time) rather than reading the ref
    // during render — the popover positions itself off this rect.
    setRenameAnchorRect(renameAnchorRef.current?.getBoundingClientRect() ?? null)
    setRenameError(null)
    setRenameTarget(singleSelected)
  }, [singleSelected])

  const handleRenameConfirm = useCallback(
    async (newName: string) => {
      if (!renameTarget) return
      const res = await renameStorageEntry(renameTarget, newName)
      if ('ok' in res) {
        // Re-select by the new full path (file or folder) so a follow-up action
        // keeps targeting the renamed entry once the tree rebuilds.
        const newPath = join(parentOf(renameTarget), newName)
        setRenameTarget(null)
        setRenameError(null)
        setSelected(new Set([newPath]))
        refresh()
      } else if (res.kind === 'exists') {
        setRenameError(t('editor.buffers.rename_exists_error'))
      } else {
        setActionError(res.message)
      }
    },
    [renameTarget, refresh, t],
  )

  const validateRename = useCallback(
    (trimmed: string, current: string): string | undefined => {
      if (!trimmed) return undefined
      if (trimmed === current) return undefined
      // Subfolders are not supported until Phase 1b's mkdir / move.
      if (trimmed.includes('/')) return t('editor.buffers.rename_slash_error')
      return undefined
    },
    [t],
  )

  /**
   * The one delete path (T4.1): the toolbar hands it the whole selection, a row
   * action hands it just that row. On success the deleted paths are dropped
   * from the selection — for the toolbar that empties it, for a row action it
   * leaves the rest of the selection alone (deleting a hovered row must not
   * clear an unrelated selection).
   *
   * Returns the outcome so a caller that needs to report on it (the empty-file
   * cleanup below) can, without duplicating the delete call or the banner.
   */
  const deletePaths = useCallback(
    async (paths: string[]): Promise<DeleteOutcome | null> => {
      if (paths.length === 0) return null
      setBusy(true)
      setActionError(null)
      setActionWarning(null)
      const res = await deleteStorageEntries(paths, t)
      setBusy(false)
      if (res.status === 'deleted') {
        setSelected((prev) => {
          const next = new Set(prev)
          for (const p of paths) next.delete(p)
          return next
        })
        refresh()
      } else if (res.status === 'refused' || res.status === 'error') {
        setActionError(res.message)
      }
      return res
    },
    [t, refresh],
  )

  const handleDelete = useCallback(() => deletePaths(selectedArray), [deletePaths, selectedArray])

  // --- Manual empty-file cleanup (T4.2) ---
  //
  // Eager reservation (#854) writes a real 0 B file the instant "New File" is
  // pressed, so every new tab that was never typed into leaves one behind. This
  // is the broom: scan the ALREADY-LOADED tree (pure, no backend read), show
  // exactly what would go, and delete the confirmed set through the same
  // `deleteStorageEntries` everything else uses — guards included. Those guards
  // raise their own native confirm (locked-tab refusal / dirty-buffer warning),
  // which is deliberately NOT bypassed: a 0 B file can still be open with
  // unsaved edits, and that warning is the only thing standing between this
  // housekeeping sweep and losing them.

  const handleCleanEmpty = useCallback(() => {
    const candidates = findEmptyFiles(tree)
    if (candidates.length === 0) {
      // Nothing to do is an OUTCOME, not a silent no-op — and an empty
      // confirmation dialog would be worse than no dialog at all.
      useUndoToast.getState().show(t('editor.buffers.clean_empty_none'))
      return
    }
    setCleanupCandidates(candidates)
  }, [tree, t])

  const handleCleanEmptyConfirm = useCallback(async () => {
    const paths = cleanupCandidates
    setCleanupCandidates(null)
    if (!paths || paths.length === 0) return
    const res = await deletePaths(paths)
    if (res?.status === 'deleted') {
      useUndoToast.getState().show(t('editor.buffers.clean_empty_done', { count: paths.length }))
    } else if (res?.status === 'error') {
      // `deleteStorageEntries` is NOT atomic — it deletes path by path with no
      // transaction, so a mid-way failure leaves the earlier paths gone. The
      // banner (already set by `deletePaths`) reports the failure; refreshing is
      // what stops the tree from still listing what IS deleted.
      refresh()
    }
  }, [cleanupCandidates, deletePaths, refresh, t])

  // --- Row-scoped actions (T4.1) ---

  // Rename from a row's own button: anchor the shared popover to THAT button's
  // rect and target THAT path, whatever happens to be selected.
  const handleRowRename = useCallback((path: string, anchorRect: DOMRect | null) => {
    setRenameAnchorRect(anchorRect)
    setRenameError(null)
    setRenameTarget(path)
  }, [])

  const handleRowDelete = useCallback((path: string) => deletePaths([path]), [deletePaths])

  const handleOpenSelected = useCallback(() => {
    // Only files open (codex B3): a folder is not openable, so guard here as
    // well as disabling the toolbar button — a folder must never be handed to
    // openInAppFile.
    if (singleSelected && !selectedNode?.isDir) handleOpen(singleSelected)
  }, [singleSelected, selectedNode, handleOpen])

  // --- Upload (file picker + native OS-file drop, T1c-1) ---

  // Ingest OS files into the current `targetDir`. Shared by the picker and the
  // native drop. Reflects partial success in the inline banner (codex R1 F5) and
  // distinguishes the T1c-4 guard outcomes by typed reason:
  //   - a quota failure (store full) → red error banner (takes precedence);
  //   - else if EVERY failure is too-large → amber warning naming the first file
  //     + the cap in MB (a soft, recoverable rejection, not a hard error);
  //   - else → the generic partial message (count + first failed file).
  // All-success clears both banners.
  const ingestFiles = useCallback(
    async (files: File[]) => {
      if (files.length === 0) return
      setBusy(true)
      setActionError(null)
      setActionWarning(null)
      const summary = await uploadFiles(targetDir, files)
      setBusy(false)
      const { failed } = summary
      if (failed.length > 0) {
        const quota = failed.find((f) => f.kind === 'quota')
        const tooLarge = failed.filter((f) => f.kind === 'too-large')
        if (quota) {
          setActionError(t('editor.buffers.upload_quota', { name: quota.name }))
        } else if (tooLarge.length === failed.length) {
          const capMb = Math.round((tooLarge[0].cap ?? 0) / (1024 * 1024))
          setActionWarning(
            t('editor.buffers.upload_too_large', { name: tooLarge[0].name, cap: capMb }),
          )
        } else {
          setActionError(
            t('editor.buffers.upload_partial', {
              uploaded: summary.uploaded.length,
              failed: failed.length,
              name: failed[0].name,
            }),
          )
        }
      }
      refresh()
    },
    [targetDir, refresh, t],
  )

  const handleUploadClick = useCallback(() => {
    uploadInputRef.current?.click()
  }, [])

  const handleUploadChange = useCallback(
    async (e: React.ChangeEvent<HTMLInputElement>) => {
      const files = e.target.files ? Array.from(e.target.files) : []
      // Clear the value so re-selecting the SAME file fires change again.
      e.target.value = ''
      await ingestFiles(files)
    },
    [ingestFiles],
  )

  // Native OS-file drag (decision 1). `preventDefault` on dragover is what makes
  // the region a valid drop target; only act when the drag actually carries
  // files so an internal dnd-kit node move passes straight through.
  const handleNativeDragOver = useCallback((e: React.DragEvent<HTMLDivElement>) => {
    if (e.dataTransfer?.types?.includes('Files')) e.preventDefault()
  }, [])

  const handleNativeDrop = useCallback(
    (e: React.DragEvent<HTMLDivElement>) => {
      const dt = e.dataTransfer
      // Not an OS-file drag (no `Files` type) → this is a dnd-kit internal node
      // move; do NOT preventDefault, do NOT ingest — let dnd-kit's pointer-event
      // flow own it.
      if (!dt?.types?.includes('Files')) return
      // It IS an OS-file drag — claim it so the browser never falls back to its
      // default drop (navigating to / opening the dropped item). This covers
      // dropping an OS FOLDER, which reports `types: ['Files']` but an EMPTY
      // `files` list (codex R2 C3): without preventDefault the folder drop would
      // leak to the browser default.
      e.preventDefault()
      const files = dt.files
      if (!files || files.length === 0) return
      void ingestFiles(Array.from(files))
    },
    [ingestFiles],
  )

  const handleDownload = useCallback(async () => {
    // Single-file only (T1c-2): a folder is not downloadable, so guard here as
    // well as disabling the toolbar button. The byte read + OS download happen
    // in downloadStorageFile; surface any failure in the inline banner.
    if (!singleSelected || selectedNode?.isDir) return
    setActionError(null)
    setActionWarning(null)
    const res = await downloadStorageFile(singleSelected)
    if ('error' in res) setActionError(res.error)
  }, [singleSelected, selectedNode])

  // --- Drag-and-drop move (T1b-6b) ---

  // Mirror RegionManager: a 5px activation distance so a stationary
  // click/double-click never starts a drag — select/open/toggle coexist with
  // dragging.
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  const handleDragEnd = useCallback(
    async (event: DragEndEvent) => {
      const move = computeMoveFromDragEnd(event.active, event.over)
      if (!move) return
      setActionError(null)
      setActionWarning(null)
      const res = await moveStorageEntry(move.from, move.targetDir)
      if ('ok' in res) {
        // Keep the moved entry selected at its new home so a follow-up action
        // still targets it once the tree rebuilds.
        setSelected(new Set([join(move.targetDir, basename(move.from))]))
        refresh()
      } else if (res.kind === 'exists') {
        setActionError(t('editor.buffers.rename_exists_error'))
      } else if (res.kind === 'error') {
        setActionError(res.message)
      }
      // 'noop' (inert / self / own-descendant): silent.
    },
    [refresh, t],
  )

  // --- Render ---

  const hasAny = tree.length > 0
  const canRename = selectedArray.length === 1
  const canDelete = selectedArray.length >= 1
  // Open is only valid for a single FILE selection (codex B3): a folder is not
  // openable, so the toolbar Open button disables when a folder is selected.
  const canOpen = singleSelected !== null && !selectedNode?.isDir
  // Download is single-file only (T1c-2): a folder is not downloadable, same
  // guard as Open.
  const canDownload = canOpen
  const toolbarBusy = busy || loading

  return (
    <div className="flex flex-col h-full overflow-hidden">
      {/* Toolbar */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-border-subtle">
        <Stack size={16} className="text-text-muted" />
        <h2 className="text-sm font-medium text-text-primary">{t('editor.buffers.tab_title')}</h2>
        <div className="flex-1" />
        <button
          data-testid="toolbar-new"
          onClick={handleNew}
          disabled={toolbarBusy}
          className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover disabled:opacity-50"
          title={t('editor.buffers.new')}
        >
          <FilePlus size={14} />
          {t('editor.buffers.new')}
        </button>
        <button
          data-testid="toolbar-new-folder"
          onClick={handleNewFolder}
          disabled={toolbarBusy}
          className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover disabled:opacity-50"
          title={t('editor.buffers.new_folder')}
        >
          <FolderPlus size={14} />
          {t('editor.buffers.new_folder')}
        </button>
        <button
          data-testid="toolbar-upload"
          onClick={handleUploadClick}
          disabled={toolbarBusy}
          className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover disabled:opacity-50"
          title={t('editor.buffers.upload')}
        >
          <UploadSimple size={14} />
          {t('editor.buffers.upload')}
        </button>
        <input
          ref={uploadInputRef}
          type="file"
          multiple
          data-testid="upload-input"
          className="hidden"
          onChange={handleUploadChange}
        />
        <button
          data-testid="toolbar-rename"
          ref={renameAnchorRef}
          onClick={handleOpenRename}
          disabled={!canRename || toolbarBusy}
          className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover disabled:opacity-50"
          title={t('editor.buffers.rename')}
        >
          <PencilSimple size={14} />
          {t('editor.buffers.rename')}
        </button>
        <button
          data-testid="toolbar-delete"
          onClick={handleDelete}
          disabled={!canDelete || toolbarBusy}
          className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover disabled:opacity-50"
          title={t('editor.buffers.delete')}
        >
          <Trash size={14} />
          {t('editor.buffers.delete')}
        </button>
        <button
          data-testid="toolbar-clean-empty"
          onClick={handleCleanEmpty}
          disabled={toolbarBusy}
          className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover disabled:opacity-50"
          title={t('editor.buffers.clean_empty')}
        >
          <Broom size={14} />
          {t('editor.buffers.clean_empty')}
        </button>
        <button
          data-testid="toolbar-open"
          onClick={handleOpenSelected}
          disabled={!canOpen || toolbarBusy}
          className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover disabled:opacity-50"
          title={t('editor.buffers.open')}
        >
          <FolderOpen size={14} />
          {t('editor.buffers.open')}
        </button>
        <button
          data-testid="toolbar-download"
          onClick={handleDownload}
          disabled={!canDownload || toolbarBusy}
          className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover disabled:opacity-50"
          title={t('editor.buffers.download')}
        >
          <DownloadSimple size={14} />
          {t('editor.buffers.download')}
        </button>
      </div>

      {/* Body: tree (left) + placeholder Backups sidebar (right). The tree is a
          DnD surface: rows are drag sources, folder rows + the root region are
          drop targets, and a drop calls `moveStorageEntry` via `handleDragEnd`. */}
      <div className="flex-1 flex overflow-hidden">
        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
          <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
          {/* Selection action bar (T4.3) — only while something is selected. It
              sits OUTSIDE the scrollable region so a long tree never scrolls the
              batch actions away. */}
          {selected.size > 0 && (
            <div
              data-testid="selection-action-bar"
              className="flex items-center gap-2 px-3 py-1.5 border-b border-border-subtle bg-surface-secondary"
            >
              <span data-testid="selection-count" className="text-xs text-text-primary">
                {t('editor.buffers.selected_count', { count: selected.size })}
              </span>
              <div className="flex-1" />
              <button
                data-testid="selection-delete"
                onClick={handleDelete}
                disabled={toolbarBusy}
                className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover hover:text-status-error disabled:opacity-50"
                title={t('editor.buffers.delete')}
              >
                <Trash size={14} />
                {t('editor.buffers.delete')}
              </button>
              <button
                data-testid="selection-clear"
                onClick={handleClearSelection}
                className="px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover"
                title={t('editor.buffers.clear_selection')}
              >
                {t('editor.buffers.clear_selection')}
              </button>
            </div>
          )}
          <StorageRegionDropZone
            targetDir={targetDir}
            onNativeDragOver={handleNativeDragOver}
            onNativeDrop={handleNativeDrop}
          >
            {/* Select-all header (T4.3): checked when every VISIBLE row is
                selected, indeterminate on a partial selection. Sticky so it
                survives scrolling the tree. */}
            {!error && hasAny && (
              <div
                data-testid="storage-list-header"
                className="sticky top-0 z-10 flex items-center gap-1.5 pl-2 pr-3 py-1 bg-surface-primary border-b border-border-subtle"
              >
                <input
                  type="checkbox"
                  data-testid="select-all-checkbox"
                  ref={selectAllRef}
                  checked={allVisibleSelected}
                  onChange={handleToggleSelectAll}
                  aria-label={t('editor.buffers.select_all')}
                  className="shrink-0 accent-accent cursor-pointer"
                />
                <span className="text-xs text-text-muted">{t('editor.buffers.select_all')}</span>
              </div>
            )}
            {error && <div className="p-4 text-xs text-red-400">{error}</div>}
            {!error && actionWarning && (
              <div data-testid="storage-warning" className="p-4 text-xs text-amber-400">
                {actionWarning}
              </div>
            )}
            {!error && !hasAny && (
              <div className="p-8 flex flex-col items-center justify-center text-text-muted">
                <Stack size={32} className="mb-2 opacity-50" />
                <p className="text-sm">{t('editor.buffers.empty')}</p>
              </div>
            )}
            {!error && hasAny && (
              <StorageTree
                tree={tree}
                expanded={expanded}
                selected={selected}
                onToggle={toggle}
                onSelect={handleSelect}
                onOpen={handleOpen}
                onRename={handleRowRename}
                onDelete={handleRowDelete}
                onToggleSelect={handleToggleRowSelect}
              />
            )}
          </StorageRegionDropZone>
          </div>
        </DndContext>
        <BackupStatusSidebar />
      </div>

      {/* Empty-file cleanup confirmation (T4.2) — the full candidate list is
          shown BEFORE anything is deleted, because this action deletes files
          the user never explicitly selected. */}
      {cleanupCandidates && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
          data-testid="empty-cleanup-dialog"
        >
          <div className="flex max-h-[80vh] w-[420px] flex-col rounded-lg border border-border-default bg-surface-primary shadow-lg">
            <div className="border-b border-border-subtle px-4 py-3">
              <h3 className="text-sm font-medium text-text-primary">
                {t('editor.buffers.clean_empty')}
              </h3>
              <p className="mt-1 text-xs text-text-muted">
                {t('editor.buffers.clean_empty_confirm', { count: cleanupCandidates.length })}
              </p>
            </div>
            <ul className="flex-1 overflow-y-auto px-4 py-3 text-xs text-text-secondary">
              {cleanupCandidates.map((path) => (
                <li key={path} data-testid="empty-cleanup-item" className="truncate py-0.5">
                  {path}
                </li>
              ))}
            </ul>
            <div className="flex justify-end gap-2 border-t border-border-subtle px-4 py-3">
              <button
                data-testid="empty-cleanup-cancel"
                onClick={() => setCleanupCandidates(null)}
                className="px-3 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover"
              >
                {t('common.cancel')}
              </button>
              <button
                data-testid="empty-cleanup-confirm"
                onClick={handleCleanEmptyConfirm}
                className="px-3 py-1 rounded-md text-xs text-text-primary bg-surface-secondary hover:bg-surface-hover hover:text-status-error"
              >
                {t('editor.buffers.delete')}
              </button>
            </div>
          </div>
        </div>
      )}

      {renameTarget && (
        <RenamePopover
          anchorRect={
            renameAnchorRect ??
            ({
              left: 0,
              top: 0,
              right: 0,
              bottom: 0,
              width: 0,
              height: 0,
              x: 0,
              y: 0,
              toJSON: () => ({}),
            } as DOMRect)
          }
          currentName={basename(renameTarget)}
          onConfirm={handleRenameConfirm}
          onCancel={() => {
            setRenameTarget(null)
            setRenameError(null)
          }}
          error={renameError ?? undefined}
          onClearError={() => setRenameError(null)}
          validateName={validateRename}
        />
      )}
    </div>
  )
}
