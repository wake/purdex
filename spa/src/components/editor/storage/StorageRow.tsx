import { useCallback, useEffect, useState } from 'react'
import { useDraggable, useDroppable } from '@dnd-kit/core'
import { CaretRight, CaretDown } from '@phosphor-icons/react'
import { ICON_MAP } from '../../tab-icon-map'
import { fileIconForPath } from '../../../lib/file-icon'
import { getFsBackend } from '../../../lib/fs-backend'
import { parentOf } from '../../../lib/storage-paths'
import { isWordCountable, wordCountFor } from '../../../lib/text-metrics'
import type { TreeNode } from '../../../lib/storage-tree'

/**
 * A row gets a word count only when its extension is on the text allowlist AND
 * it is within the size cap; everything else (dirs, binaries, unknown exts,
 * oversized text) shows size only and is never read. The allowlist + cap + count
 * now live in the shared `text-metrics` util (T2b-1) so the backup manifest
 * builder computes the identical metric (R1-P1c).
 */
function isTextNode(node: TreeNode): boolean {
  if (node.isDir) return false
  return isWordCountable(node.path, node.size)
}

interface StorageRowProps {
  node: TreeNode
  depth: number
  selected: boolean
  expanded: boolean
  onToggle: (path: string) => void
  /**
   * Select this row. `additive` (cmd/ctrl/shift held) toggles the row in a
   * multi-selection; a plain click (additive=false) replaces the selection with
   * just this row (codex B5).
   */
  onSelect: (path: string, additive: boolean) => void
  onOpen: (path: string) => void
}

/**
 * A single storage tree row: type icon (`fileIconForPath` → `ICON_MAP`) + name +
 * metadata.
 *
 * Selection vs expand are separate gestures (T1b-0): a **single click anywhere
 * on the row selects** the node (file or folder), so a folder can be the target
 * of nesting-aware actions. The **caret** is its own button that toggles
 * expand/collapse and stops propagation, so expanding a folder never changes the
 * selection (and selecting never expands). **Double-click** opens a file or
 * toggles a folder's expansion. Text files additionally render a word count
 * (decoded from the backend bytes); binary files show size only.
 *
 * Drag-and-drop move (T1b-6b): every row is a `useDraggable` source keyed by its
 * full path; **folder** rows are additionally `useDroppable` drop targets (files
 * pass `disabled` so they never accept a drop — the root region in `StoragePane`
 * is the other target). The `PointerSensor` activation distance (5px, set in
 * `StoragePane`) keeps a stationary click/double-click from starting a drag, so
 * select/open/toggle coexist with dragging. `StoragePane.onDragEnd` resolves the
 * active path + drop target into a `moveStorageEntry` call.
 */
export function StorageRow({
  node,
  depth,
  selected,
  expanded,
  onToggle,
  onSelect,
  onOpen,
}: StorageRowProps) {
  const text = isTextNode(node)
  const [wordCount, setWordCount] = useState<number | null>(null)

  // The directory a drop onto THIS row targets (codex B1): a folder accepts
  // children into itself; a file resolves to its parent dir (dropping onto a
  // file means "into the folder it lives in"). Published on the droppable's
  // `data.targetDir` so the drop resolver reads an authoritative value instead
  // of inferring it from the over-id — which previously let a drop onto a file
  // row (no droppable then) fall through to the root zone and move to root.
  const targetDir = node.isDir ? node.path : parentOf(node.path)

  // Drag source (every row) + drop target (EVERY row now — files route a drop
  // to their parent dir via `targetDir`). One DOM node carries both refs via the
  // merge callback below.
  const { attributes, listeners, setNodeRef: setDragRef, transform, isDragging } = useDraggable({
    id: node.path,
  })
  const { setNodeRef: setDropRef, isOver } = useDroppable({
    id: node.path,
    data: { targetDir },
  })
  const setRowRef = useCallback(
    (el: HTMLDivElement | null) => {
      setDragRef(el)
      setDropRef(el)
    },
    [setDragRef, setDropRef],
  )
  const dropActive = node.isDir && isOver

  useEffect(() => {
    // Rows are keyed by full path in `StorageTree`, so `text` is constant for a
    // given instance — a binary row simply never reads bytes and keeps the
    // initial `null` word count (no synchronous reset needed).
    if (!text) return
    let cancelled = false
    const backend = getFsBackend({ type: 'inapp' })
    if (!backend) return
    backend
      .read(node.path)
      .then((bytes) => {
        if (cancelled) return
        setWordCount(wordCountFor(node.path, bytes))
      })
      .catch(() => {
        if (!cancelled) setWordCount(null)
      })
    return () => {
      cancelled = true
    }
  }, [node.path, text])

  const iconName = fileIconForPath(node.path, { isDir: node.isDir, expanded })
  const Icon = ICON_MAP[iconName] ?? ICON_MAP.File

  // Single click selects (file or folder); double-click opens a file or toggles
  // a folder's expansion. A plain click selects ONLY this row (replacing the
  // selection); a modifier click (cmd/ctrl/shift) toggles it into a
  // multi-selection (codex B5) — so the click→click→double-click sequence no
  // longer toggles the row off and leaves a stale/empty action target. The
  // caret has its own handler below.
  const isAdditive = (e: { metaKey: boolean; ctrlKey: boolean; shiftKey: boolean }) =>
    e.metaKey || e.ctrlKey || e.shiftKey
  const handleClick = (e: React.MouseEvent) => onSelect(node.path, isAdditive(e))
  const handleDoubleClick = () => {
    if (node.isDir) onToggle(node.path)
    else onOpen(node.path)
  }
  const handleCaretClick = (e: React.MouseEvent) => {
    // Stop the row's select handler so expand/collapse is independent of
    // selection (and vice versa).
    e.stopPropagation()
    onToggle(node.path)
  }
  // Keyboard parity for the `role="button"` row (codex B6): Enter mirrors the
  // double-click (file → open, folder → toggle); Space selects (additive with a
  // modifier). preventDefault on Space stops the page from scrolling.
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      if (node.isDir) onToggle(node.path)
      else onOpen(node.path)
    } else if (e.key === ' ') {
      e.preventDefault()
      onSelect(node.path, isAdditive(e))
    }
  }

  return (
    <div
      ref={setRowRef}
      {...attributes}
      {...listeners}
      data-testid="buffer-row"
      data-name={node.name}
      data-path={node.path}
      data-isdir={node.isDir ? 'true' : 'false'}
      data-icon={iconName}
      data-drop-over={dropActive ? 'true' : 'false'}
      data-target-dir={targetDir}
      role="button"
      tabIndex={0}
      aria-selected={selected}
      onClick={handleClick}
      onDoubleClick={handleDoubleClick}
      onKeyDown={handleKeyDown}
      style={{
        paddingLeft: 8 + depth * 16,
        ...(transform ? { transform: `translate3d(${transform.x}px, ${transform.y}px, 0)` } : {}),
      }}
      className={
        'w-full flex items-center gap-1.5 pr-3 py-1.5 text-left text-xs transition-colors cursor-pointer ' +
        (selected
          ? 'bg-surface-selected text-text-primary'
          : 'text-text-secondary hover:bg-surface-hover') +
        (dropActive ? ' ring-1 ring-inset ring-accent bg-surface-hover' : '') +
        (isDragging ? ' opacity-50' : '')
      }
    >
      {node.isDir ? (
        <button
          type="button"
          data-testid="buffer-caret"
          onClick={handleCaretClick}
          onPointerDown={(e) => e.stopPropagation()}
          aria-label={expanded ? 'Collapse' : 'Expand'}
          className="shrink-0 flex items-center justify-center text-text-muted hover:text-text-primary"
        >
          {expanded ? <CaretDown size={12} /> : <CaretRight size={12} />}
        </button>
      ) : (
        <span className="w-3 shrink-0" />
      )}
      <Icon size={14} className="shrink-0 text-text-muted" />
      <span className="truncate flex-1">{node.name}</span>
      {!node.isDir && (
        <span className="shrink-0 text-text-muted tabular-nums">
          {node.size} B
          {text && wordCount !== null ? ` · ${wordCount} words` : ''}
        </span>
      )}
    </div>
  )
}
