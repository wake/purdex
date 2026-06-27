import { useEffect, useState } from 'react'
import { CaretRight, CaretDown } from '@phosphor-icons/react'
import { ICON_MAP } from '../../tab-icon-map'
import { fileIconForPath } from '../../../lib/file-icon'
import { getFsBackend } from '../../../lib/fs-backend'
import type { TreeNode } from '../../../lib/storage-tree'

/**
 * Extensions treated as binary — these rows show **size only**, never a word
 * count (we don't decode their bytes). Everything else is treated as text and
 * gets a word count (spec §4: "Text-file rows show word count; binary rows show
 * size only"). A denylist keeps unknown/extensionless files (e.g. `.log`,
 * `README`) on the text path, which is the friendlier default for a notes-style
 * In-App store.
 */
const BINARY_EXTS = new Set([
  // images
  'png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'ico', 'bmp', 'tiff', 'avif',
  // documents / pdf
  'pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx',
  // archives
  'zip', 'tar', 'gz', 'tgz', 'rar', '7z',
  // audio / video
  'mp3', 'wav', 'flac', 'ogg', 'm4a', 'mp4', 'mov', 'mkv', 'avi',
  // misc binary
  'wasm', 'bin', 'exe', 'dll', 'so', 'dylib',
])

function extensionOf(path: string): string {
  const base = path.split('/').pop() ?? path
  const dot = base.lastIndexOf('.')
  if (dot <= 0) return ''
  return base.slice(dot + 1).toLowerCase()
}

function isTextNode(node: TreeNode): boolean {
  if (node.isDir) return false
  return !BINARY_EXTS.has(extensionOf(node.path))
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
