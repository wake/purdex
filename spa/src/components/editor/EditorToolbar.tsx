import { FloppyDisk, GitDiff } from '@phosphor-icons/react'

interface Props {
  filePath: string
  isDirty: boolean
  showDiff?: boolean
  onSave: () => void
  onDiff?: () => void
  onRenameStart?: (anchorRect: DOMRect) => void
}

export function EditorToolbar({ filePath, isDirty, showDiff, onSave, onDiff, onRenameStart }: Props) {
  const segments = filePath.split('/').filter(Boolean)

  return (
    <div className="flex items-start justify-between gap-3 px-3 py-1 border-b border-border-subtle bg-surface-secondary">
      <div className="min-w-0 flex items-center gap-2 text-xs text-text-secondary">
        <div className="min-w-0 flex items-center gap-1 overflow-hidden" title={filePath}>
            {filePath.startsWith('/') && <span className="shrink-0 text-text-muted">/</span>}
            {segments.map((segment, index) => {
              const isLast = index === segments.length - 1
              return (
                <div key={`${segment}-${index}`} className="min-w-0 flex items-center gap-1">
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
        {isDirty && <span className="text-accent-base" title="Unsaved changes">●</span>}
      </div>
      <div className="flex items-center gap-1 pt-0.5">
        {isDirty && onDiff && (
          <button
            onClick={onDiff}
            className={`p-1 rounded hover:bg-surface-hover transition-colors ${showDiff ? 'text-accent-base' : 'text-text-secondary'}`}
            title={showDiff ? 'Close diff' : 'Diff against saved'}
          >
            <GitDiff size={14} />
          </button>
        )}
        <button
          onClick={onSave}
          disabled={!isDirty}
          className="p-1 rounded hover:bg-surface-hover text-text-secondary disabled:opacity-30 transition-colors"
          title="Save (⌘S)"
        >
          <FloppyDisk size={14} />
        </button>
      </div>
    </div>
  )
}
