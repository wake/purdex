import { useState } from 'react'
import { FloppyDisk, GitDiff } from '@phosphor-icons/react'
import type { FileSource } from '../../types/fs'
import { getFsBackend } from '../../lib/fs-backend'
import { STORAGE_ROOT, relativeToRoot } from '../../lib/storage-paths'
import { listTreeUnder, type TreeNode } from '../../lib/storage-tree'
import { BreadcrumbPopover } from './BreadcrumbPopover'

/**
 * Flatten a recursive `TreeNode[]` to its file leaves (directories are not
 * switch targets — they only contribute path prefixes to the labels). The
 * pre-order walk preserves the dirs-first/name order from `listTreeUnder`.
 */
function fileLeaves(nodes: TreeNode[], out: TreeNode[] = []): TreeNode[] {
  for (const node of nodes) {
    if (node.isDir) fileLeaves(node.children ?? [], out)
    else out.push(node)
  }
  return out
}

interface Props {
  source: FileSource
  filePath: string
  displayPath?: string
  isDirty: boolean
  canSave?: boolean
  showDiff?: boolean
  /**
   * Why this markdown file is in raw mode when the user never asked for it
   * (spec 2.3 — it carries content Live Mode would destroy). Absent whenever raw
   * is the user's own choice or simply what the language implies.
   */
  rawReason?: string
  onSave: (anchorRect?: DOMRect) => void
  /**
   * Handle on the Save button so the owner can anchor a popover to it even when
   * the save did NOT originate from a click. Monaco / Tiptap invoke `onSave()`
   * with no rect (they have no button to measure), and the first save of an
   * unnamed untitled document has to open the naming popover somewhere — this
   * is that "somewhere", identical to what a click would have produced.
   */
  saveButtonRef?: React.RefObject<HTMLButtonElement | null>
  onDiff?: () => void
  onRenameStart?: (anchorRect: DOMRect) => void
  onBufferSwitch?: (newKey: string) => void
  onManage?: () => void
  onNewBuffer?: () => void
}

export function EditorToolbar({
  source,
  filePath,
  displayPath,
  isDirty,
  canSave,
  showDiff,
  rawReason,
  onSave,
  saveButtonRef,
  onDiff,
  onRenameStart,
  onBufferSwitch,
  onManage,
  onNewBuffer,
}: Props) {
  const pathForDisplay = displayPath ?? filePath
  const rawSegments = pathForDisplay.split('/').filter(Boolean)
  const showInAppPrefix = source.type === 'inapp'
  const segments = showInAppPrefix && rawSegments[0] === 'buffer' ? rawSegments.slice(1) : rawSegments
  const saveEnabled = canSave ?? isDirty

  const [popoverAnchorRect, setPopoverAnchorRect] = useState<DOMRect | null>(null)
  const [popoverBuffers, setPopoverBuffers] = useState<string[]>([])

  const handleChipClick = async (event: React.MouseEvent<HTMLButtonElement>) => {
    const rect = event.currentTarget.getBoundingClientRect()
    const backend = getFsBackend({ type: 'inapp' })
    let names: string[] = []
    if (backend) {
      try {
        // Recursive enumeration (not the flat single-level `list`) so nested
        // files are switchable; each leaf is labeled by its path relative to
        // STORAGE_ROOT (e.g. `dir/sub/c.md`). BreadcrumbPopover re-joins the
        // label onto STORAGE_ROOT to recover the full switch target.
        const tree = await listTreeUnder(backend, STORAGE_ROOT)
        names = fileLeaves(tree)
          .map((node) => relativeToRoot(node.path))
          .sort((a, b) => a.localeCompare(b))
      } catch {
        names = []
      }
    }
    setPopoverBuffers(names)
    setPopoverAnchorRect(rect)
  }

  const dismissPopover = () => {
    setPopoverAnchorRect(null)
  }

  return (
    <div className="flex items-center justify-between gap-2 px-3 py-1 border-b border-border-subtle bg-surface-secondary">
      <div className="min-w-0 flex items-center gap-2 text-xs text-text-secondary">
        <div className="min-w-0 flex items-center gap-0.5 overflow-hidden" title={pathForDisplay}>
            {showInAppPrefix ? (
              <>
                <button
                  type="button"
                  onClick={handleChipClick}
                  className="shrink-0 flex items-center gap-0.5 text-text-primary rounded hover:bg-surface-hover px-0.5 -mx-0.5"
                  title="Switch buffer"
                >
                  <img
                    src="/icons/logo-transparent.png"
                    alt=""
                    aria-hidden="true"
                    className="h-3.5 w-3.5 shrink-0 brightness-0 invert opacity-95"
                  />
                  <span className="shrink-0">Purdex</span>
                </button>
                <span className="shrink-0 text-text-muted">/</span>
              </>
            ) : filePath.startsWith('/') ? <span className="shrink-0 text-text-muted">/</span> : null}
            {segments.map((segment, index) => {
              const isLast = index === segments.length - 1
              return (
                <div key={`${segment}-${index}`} className="min-w-0 flex items-center gap-0.5">
                  {index > 0 && <span className="shrink-0 text-text-muted">/</span>}
                  {isLast && onRenameStart ? (
                    <button
                      type="button"
                      onDoubleClick={(event) => onRenameStart(event.currentTarget.getBoundingClientRect())}
                      className="truncate text-text-primary text-left"
                    >
                      {segment}
                    </button>
                  ) : (
                    <span className={isLast ? 'truncate text-text-primary' : 'shrink-0'}>{segment}</span>
                  )}
                </div>
              )
            })}
        </div>
        {/* Spec 1.3: the dot means "there are unsaved changes", so it binds to
            `isDirty` only. Binding it to `saveEnabled` made every never-saved
            buffer (and, before the canSave fix, every file that failed to stat)
            claim to be modified. */}
        {isDirty && <span className="text-accent" title="Unsaved changes">●</span>}
        {rawReason && (
          <span
            data-testid="editor-raw-reason"
            className="shrink-0 truncate rounded bg-surface-hover px-1.5 py-0.5 text-[10px] text-text-muted"
            title={rawReason}
          >
            {rawReason}
          </span>
        )}
      </div>
      <div className="flex items-center gap-1">
        {isDirty && onDiff && (
          <button
            onClick={onDiff}
            /* `text-accent-base` had no Tailwind 4 token behind it (0 hits in
               the built CSS), so the active state never actually painted. Use
               the same `text-accent` the Save button uses. */
            className={`p-1 rounded hover:bg-surface-hover transition-colors ${showDiff ? 'text-accent' : 'text-text-secondary'}`}
            title={showDiff ? 'Close diff' : 'Diff against saved'}
          >
            <GitDiff size={14} />
          </button>
        )}
        {/* Spec 1.3: a 14 px floppy at two near-identical greys made "savable"
            impossible to read at a glance. Enabled now carries the theme accent
            (`--color-accent`); disabled keeps the muted secondary + opacity-30. */}
        <button
          ref={saveButtonRef}
          onClick={(event) => onSave(event.currentTarget.getBoundingClientRect())}
          disabled={!saveEnabled}
          className={`p-1 rounded hover:bg-surface-hover disabled:opacity-30 transition-colors ${saveEnabled ? 'text-accent hover:text-accent-hover' : 'text-text-secondary'}`}
          title="Save (⌘S)"
        >
          <FloppyDisk size={14} />
        </button>
      </div>
      {showInAppPrefix && popoverAnchorRect && (
        <BreadcrumbPopover
          buffers={popoverBuffers}
          currentBufferKey={filePath}
          onSwitch={(newKey) => {
            onBufferSwitch?.(newKey)
          }}
          onManage={() => {
            onManage?.()
          }}
          onDismiss={dismissPopover}
          anchorRect={popoverAnchorRect}
          onNewBuffer={onNewBuffer ? () => {
            onNewBuffer()
          } : undefined}
        />
      )}
    </div>
  )
}
