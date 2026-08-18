import { BubbleMenu } from '@tiptap/react/menus'
import type { ChainedCommands, Editor } from '@tiptap/react'
import {
  ColumnsPlusLeft,
  ColumnsPlusRight,
  Columns,
  RowsPlusBottom,
  RowsPlusTop,
  Rows,
  Trash,
  type Icon,
} from '@phosphor-icons/react'

/**
 * A table renders in Live Mode but, without this, cannot be maintained there:
 * there is no way to add or drop a row or a column, which makes Live Mode worse
 * than raw for any document containing a table (spec 2.2). The menu appears only
 * while the selection sits inside a table and runs the TableKit commands.
 */
interface TableAction {
  /** Stable id — also the `data-testid` suffix. */
  id: string
  label: string
  Glyph: Icon
  run: (chain: ChainedCommands) => ChainedCommands
}

const ACTIONS: TableAction[] = [
  { id: 'add-row-before', label: 'Insert row above', Glyph: RowsPlusTop, run: (c) => c.addRowBefore() },
  { id: 'add-row-after', label: 'Insert row below', Glyph: RowsPlusBottom, run: (c) => c.addRowAfter() },
  { id: 'delete-row', label: 'Delete row', Glyph: Rows, run: (c) => c.deleteRow() },
  { id: 'add-column-before', label: 'Insert column left', Glyph: ColumnsPlusLeft, run: (c) => c.addColumnBefore() },
  { id: 'add-column-after', label: 'Insert column right', Glyph: ColumnsPlusRight, run: (c) => c.addColumnAfter() },
  { id: 'delete-column', label: 'Delete column', Glyph: Columns, run: (c) => c.deleteColumn() },
  { id: 'delete-table', label: 'Delete table', Glyph: Trash, run: (c) => c.deleteTable() },
]

export function TableBubbleMenu({ editor }: { editor: Editor | null }) {
  if (!editor) return null

  return (
    <BubbleMenu
      editor={editor}
      pluginKey="tableBubbleMenu"
      shouldShow={({ editor: active }) => !!active?.isActive('table')}
      role="toolbar"
      aria-label="Table actions"
      className="flex items-center gap-0.5 rounded-md border border-border-default bg-surface-elevated p-0.5 shadow-lg"
    >
      {ACTIONS.map(({ id, label, Glyph, run }) => (
        <button
          key={id}
          type="button"
          data-testid={`table-menu-${id}`}
          aria-label={label}
          title={label}
          // The menu lives outside the contenteditable, so an unprevented
          // mousedown blurs ProseMirror and collapses the cell selection the
          // command is about to act on. Focus stays in the document; the chain's
          // own `focus()` then returns the caret to the edited cell.
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => run(editor.chain().focus()).run()}
          className="rounded p-1 text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary focus-visible:bg-surface-hover focus-visible:text-text-primary"
        >
          <Glyph size={14} />
        </button>
      ))}
    </BubbleMenu>
  )
}
