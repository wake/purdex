import { FloppyDisk, GitDiff } from '@phosphor-icons/react'

interface Props {
  filePath: string
  isDirty: boolean
  isMarkdown: boolean
  editorMode: 'raw' | 'wysiwyg'
  showDiff?: boolean
  isRenaming?: boolean
  renameValue?: string
  renameWarning?: string
  onSave: () => void
  onToggleMode?: () => void
  onDiff?: () => void
  onRenameStart?: () => void
  onRenameChange?: (value: string) => void
  onRenameSubmit?: () => void
  onRenameCancel?: () => void
}

export function EditorToolbar({ filePath, isDirty, isMarkdown, editorMode, showDiff, isRenaming = false, renameValue = '', renameWarning, onSave, onToggleMode, onDiff, onRenameStart, onRenameChange, onRenameSubmit, onRenameCancel }: Props) {
  const segments = filePath.split('/').filter(Boolean)

  return (
    <div className="flex items-start justify-between gap-3 px-3 py-1 border-b border-border-subtle bg-surface-secondary">
      <div className="min-w-0 flex flex-col gap-1 text-xs text-text-secondary">
        <div className="min-w-0 flex items-center gap-2">
          <div className="min-w-0 flex items-center gap-1 overflow-hidden" title={filePath}>
            {filePath.startsWith('/') && <span className="shrink-0 text-text-muted">/</span>}
            {segments.map((segment, index) => {
              const isLast = index === segments.length - 1
              return (
                <div key={`${segment}-${index}`} className="min-w-0 flex items-center gap-1">
                  {index > 0 && <span className="shrink-0 text-text-muted">/</span>}
                  {isLast && isRenaming ? (
                    <input
                      autoFocus
                      value={renameValue}
                      onChange={(event) => onRenameChange?.(event.target.value)}
                      onKeyDown={(event) => {
                        if (event.key === 'Enter') {
                          event.preventDefault()
                          onRenameSubmit?.()
                        }
                        if (event.key === 'Escape') {
                          event.preventDefault()
                          onRenameCancel?.()
                        }
                      }}
                      onBlur={() => onRenameCancel?.()}
                      aria-label="Rename file"
                      className="min-w-0 rounded border border-border-subtle bg-surface px-1.5 py-0.5 text-text-primary outline-none"
                    />
                  ) : isLast && onRenameStart ? (
                    <button
                      type="button"
                      onDoubleClick={onRenameStart}
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
        {renameWarning && <div role="alert" className="text-[10px] text-status-error">{renameWarning}</div>}
      </div>
      <div className="flex items-center gap-1 pt-0.5">
        {isMarkdown && onToggleMode && (
          <button
            onClick={onToggleMode}
            className="px-2 py-0.5 rounded text-[10px] border border-border-subtle hover:bg-surface-hover text-text-secondary transition-colors"
          >
            {editorMode === 'raw' ? 'WYSIWYG' : 'Raw'}
          </button>
        )}
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
