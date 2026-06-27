import { useEffect, useState } from 'react'
import { CaretRight, CaretDown } from '@phosphor-icons/react'
import { ICON_MAP } from '../../tab-icon-map'
import { fileIconForPath } from '../../../lib/file-icon'
import { getFsBackend } from '../../../lib/fs-backend'
import type { TreeNode } from '../../../lib/storage-tree'

/**
 * Explicit **allowlist** of extensions we treat as word-countable text (spec §4:
 * "Text-file rows show word count; binary rows show size only"). Only these
 * rows read + decode their bytes — an allowlist (vs the former binary denylist)
 * means unknown/binary extensions never trigger a `backend.read` and are never
 * decoded into a garbage word count (R2-3). `.log`, `.env`, `.gitignore` etc.
 * are included so notes-style entries still count.
 */
const TEXT_EXTS = new Set([
  'md', 'markdown', 'txt', 'text',
  'ts', 'tsx', 'js', 'jsx', 'mjs', 'cjs',
  'json', 'css', 'scss', 'html', 'xml',
  'yaml', 'yml', 'toml', 'csv',
  'sh', 'py', 'rs', 'go', 'c', 'h', 'cpp',
  'sql', 'log', 'ini', 'env', 'gitignore',
])

/**
 * Word count reads + decodes the whole file, so we cap it: rows above this size
 * show size only and never read (R2-3 — render cost was the sum of all visible
 * text file sizes).
 */
const WORD_COUNT_MAX_BYTES = 256 * 1024

function extensionOf(path: string): string {
  const base = path.split('/').pop() ?? path
  const dot = base.lastIndexOf('.')
  if (dot <= 0) return ''
  return base.slice(dot + 1).toLowerCase()
}

/**
 * A row gets a word count only when its extension is on the text allowlist AND
 * it is within the size cap; everything else (dirs, binaries, unknown exts,
 * oversized text) shows size only and is never read.
 */
function isTextNode(node: TreeNode): boolean {
  if (node.isDir) return false
  if (node.size > WORD_COUNT_MAX_BYTES) return false
  return TEXT_EXTS.has(extensionOf(node.path))
}

interface StorageRowProps {
  node: TreeNode
  depth: number
  selected: boolean
  expanded: boolean
  onToggle: (path: string) => void
  onSelect: (path: string) => void
  onOpen: (path: string) => void
}

/**
 * A single storage tree row: type icon (`fileIconForPath` → `ICON_MAP`) + name +
 * metadata. Folders show a caret and toggle expand on click; files select on
 * click and open on double-click. Text files additionally render a word count
 * (decoded from the backend bytes); binary files show size only.
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
        const str = new TextDecoder().decode(bytes)
        setWordCount(str.split(/\s+/).filter(Boolean).length)
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

  const handleClick = () => {
    if (node.isDir) onToggle(node.path)
    else onSelect(node.path)
  }
  const handleDoubleClick = () => {
    if (!node.isDir) onOpen(node.path)
  }

  return (
    <button
      data-testid="buffer-row"
      data-name={node.name}
      data-path={node.path}
      data-isdir={node.isDir ? 'true' : 'false'}
      data-icon={iconName}
      aria-selected={selected}
      onClick={handleClick}
      onDoubleClick={handleDoubleClick}
      style={{ paddingLeft: 8 + depth * 16 }}
      className={
        'w-full flex items-center gap-1.5 pr-3 py-1.5 text-left text-xs transition-colors ' +
        (selected
          ? 'bg-surface-selected text-text-primary'
          : 'text-text-secondary hover:bg-surface-hover')
      }
    >
      {node.isDir ? (
        expanded ? (
          <CaretDown size={12} className="shrink-0 text-text-muted" />
        ) : (
          <CaretRight size={12} className="shrink-0 text-text-muted" />
        )
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
    </button>
  )
}
