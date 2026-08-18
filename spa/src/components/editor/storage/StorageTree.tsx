import { StorageRow } from './StorageRow'
import type { TreeNode } from '../../../lib/storage-tree'

interface StorageTreeProps {
  tree: TreeNode[]
  /** Full paths of currently expanded directories. */
  expanded: Set<string>
  /** Full paths of currently selected nodes (files OR folders — T1b-0). */
  selected: Set<string>
  onToggle: (path: string) => void
  /** Select a row; `additive` (modifier held) toggles into a multi-selection. */
  onSelect: (path: string, additive: boolean) => void
  onOpen: (path: string) => void
  /** Rename a specific row's entry; the rect anchors the rename popover (T4.1). */
  onRename: (path: string, anchorRect: DOMRect | null) => void
  /** Delete a specific row's entry, independent of the selection (T4.1). */
  onDelete: (path: string) => void
  /** Toggle a row in the multi-selection via its checkbox (T4.3). */
  onToggleSelect: (path: string) => void
}

/**
 * Renders the nested In-App storage tree from `useStorageTree`'s `TreeNode[]`.
 * Each node is identified by its FULL path (selection / expansion key), and
 * children of an expanded directory are rendered recursively with an increasing
 * `depth` for indentation. Files open via `onOpen`; folders toggle via
 * `onToggle`.
 */
export function StorageTree({
  tree,
  expanded,
  selected,
  onToggle,
  onSelect,
  onOpen,
  onRename,
  onDelete,
  onToggleSelect,
}: StorageTreeProps) {
  const renderNodes = (nodes: TreeNode[], depth: number) =>
    nodes.map((node) => {
      const isExpanded = node.isDir && expanded.has(node.path)
      return (
        <div key={node.path}>
          <StorageRow
            node={node}
            depth={depth}
            selected={selected.has(node.path)}
            expanded={isExpanded}
            onToggle={onToggle}
            onSelect={onSelect}
            onOpen={onOpen}
            onRename={onRename}
            onDelete={onDelete}
            onToggleSelect={onToggleSelect}
          />
          {isExpanded && node.children && node.children.length > 0
            ? renderNodes(node.children, depth + 1)
            : null}
        </div>
      )
    })

  return <div>{renderNodes(tree, 0)}</div>
}
