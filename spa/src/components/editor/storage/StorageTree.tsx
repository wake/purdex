import { StorageRow } from './StorageRow'
import type { TreeNode } from '../../../lib/storage-tree'

interface StorageTreeProps {
  tree: TreeNode[]
  /** Full paths of currently expanded directories. */
  expanded: Set<string>
  /** Full paths of currently selected (leaf-file) nodes. */
  selected: Set<string>
  onToggle: (path: string) => void
  onSelect: (path: string) => void
  onOpen: (path: string) => void
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
          />
          {isExpanded && node.children && node.children.length > 0
            ? renderNodes(node.children, depth + 1)
            : null}
        </div>
      )
    })

  return <div>{renderNodes(tree, 0)}</div>
}
