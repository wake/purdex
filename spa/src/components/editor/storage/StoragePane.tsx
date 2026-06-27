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
import { FilePlus, FolderPlus, PencilSimple, Stack, Trash, FolderOpen } from '@phosphor-icons/react'
import type { PaneRendererProps } from '../../../lib/module-registry'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../features/workspace/store'
import { useStorageTree } from '../../../hooks/useStorageTree'
import { findPane } from '../../../lib/pane-tree'
import { openInAppFile } from '../../../lib/open-in-app-file'
import { STORAGE_ROOT, basename, join, parentOf } from '../../../lib/storage-paths'
import { findNode, targetDirOf } from '../../../lib/storage-tree'
import { RenamePopover } from '../../RenamePopover'
import { StorageTree } from './StorageTree'
import {
  computeMoveFromDragEnd,
  createStorageFile,
  createStorageFolder,
  deleteStorageEntries,
  moveStorageEntry,
  renameStorageEntry,
} from './storage-actions'

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
  children,
}: {
  targetDir: string
  children: ReactNode
}) {
  const { setNodeRef, isOver } = useDroppable({ id: STORAGE_ROOT })
  return (
    <div
      ref={setNodeRef}
      className={'flex-1 overflow-y-auto' + (isOver ? ' bg-surface-hover/50' : '')}
      data-testid="storage-tree-region"
      data-target-dir={targetDir}
      data-root-over={isOver ? 'true' : 'false'}
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
 * Rename/delete operate on the selected node's FULL path.
 */
export function StoragePane({ pane }: PaneRendererProps) {
  const t = useI18nStore((s) => s.t)
  const { tree, loading, error: treeError, refresh, expanded, toggle } = useStorageTree()

  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [actionError, setActionError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [renameTarget, setRenameTarget] = useState<string | null>(null)
  const [renameError, setRenameError] = useState<string | null>(null)
  const [renameAnchorRect, setRenameAnchorRect] = useState<DOMRect | null>(null)
  const renameAnchorRef = useRef<HTMLButtonElement | null>(null)

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

  const handleSelect = useCallback((path: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })
  }, [])

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

  const handleDelete = useCallback(async () => {
    if (selectedArray.length === 0) return
    setBusy(true)
    setActionError(null)
    const res = await deleteStorageEntries(selectedArray, t)
    setBusy(false)
    if (res.status === 'deleted') {
      setSelected(new Set())
      refresh()
    } else if (res.status === 'refused' || res.status === 'error') {
      setActionError(res.message)
    }
  }, [selectedArray, t, refresh])

  const handleOpenSelected = useCallback(() => {
    if (singleSelected) handleOpen(singleSelected)
  }, [singleSelected, handleOpen])

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
  const canOpen = selectedArray.length === 1
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
          data-testid="toolbar-open"
          onClick={handleOpenSelected}
          disabled={!canOpen || toolbarBusy}
          className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover disabled:opacity-50"
          title={t('editor.buffers.open')}
        >
          <FolderOpen size={14} />
          {t('editor.buffers.open')}
        </button>
      </div>

      {/* Body: tree (left) + placeholder Backups sidebar (right). The tree is a
          DnD surface: rows are drag sources, folder rows + the root region are
          drop targets, and a drop calls `moveStorageEntry` via `handleDragEnd`. */}
      <div className="flex-1 flex overflow-hidden">
        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
          <StorageRegionDropZone targetDir={targetDir}>
            {error && <div className="p-4 text-xs text-red-400">{error}</div>}
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
              />
            )}
          </StorageRegionDropZone>
        </DndContext>
        <aside
          data-testid="storage-backups-placeholder"
          className="w-48 shrink-0 border-l border-border-subtle p-3 text-xs text-text-muted"
        >
          Backups (coming soon)
        </aside>
      </div>

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
