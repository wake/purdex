import { FloppyDisk, GitDiff } from '@phosphor-icons/react'
import type { FileSource } from '../../types/fs'

interface Props {
  source: FileSource
  filePath: string
  displayPath?: string
  isDirty: boolean
  canSave?: boolean
  showDiff?: boolean
  onSave: (anchorRect?: DOMRect) => void
  onDiff?: () => void
  onRenameStart?: (anchorRect: DOMRect) => void
}

export function EditorToolbar({ source, filePath, displayPath, isDirty, canSave, showDiff, onSave, onDiff, onRenameStart }: Props) {
  const pathForDisplay = displayPath ?? filePath
  const rawSegments = pathForDisplay.split('/').filter(Boolean)
  const showInAppPrefix = source.type === 'inapp'
  const segments = showInAppPrefix && rawSegments[0] === 'buffer' ? rawSegments.slice(1) : rawSegments
  const saveEnabled = canSave ?? isDirty

  return (
    <div className="flex items-start justify-between gap-2 px-3 py-1 border-b border-border-subtle bg-surface-secondary">
      <div className="min-w-0 flex items-center gap-2 text-xs text-text-secondary">
        <div className="min-w-0 flex items-center gap-0.5 overflow-hidden" title={pathForDisplay}>
            {showInAppPrefix ? (
              <>
                <span className="shrink-0 flex items-center gap-0.5 text-text-primary">
                  <img
                    src="/icons/logo-transparent.png"
                    alt=""
                    aria-hidden="true"
                    className="h-3.5 w-3.5 shrink-0 brightness-0 invert opacity-95"
                  />
                  <span className="shrink-0">Purdex</span>
                </span>
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
        {saveEnabled && <span className="text-accent-base" title="Unsaved changes">●</span>}
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
          onClick={(event) => onSave(event.currentTarget.getBoundingClientRect())}
          disabled={!saveEnabled}
          className="p-1 rounded hover:bg-surface-hover text-text-secondary disabled:opacity-30 transition-colors"
          title="Save (⌘S)"
        >
          <FloppyDisk size={14} />
        </button>
      </div>
    </div>
  )
}
