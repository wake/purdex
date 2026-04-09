import { Columns, Rows, GridFour, Square } from '@phosphor-icons/react'

interface Props {
  title: string
}

export function TitleBar({ title }: Props) {
  return (
    <div
      className="shrink-0 flex items-center bg-surface-secondary border-b border-border-subtle px-2"
      style={{ height: 30, WebkitAppRegion: 'drag' } as React.CSSProperties}
    >
      {/* Traffic light safe zone */}
      <div className="shrink-0" style={{ width: 70 }} />

      {/* Title — centered */}
      <div className="flex-1 text-center text-xs text-text-muted truncate select-none">
        {title}
      </div>

      {/* Layout pattern buttons — placeholder, will be wired in Plan 3 */}
      <div
        data-testid="layout-buttons"
        className="shrink-0 flex items-center gap-0.5"
        style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      >
        <button disabled className="p-1 rounded text-text-muted disabled:opacity-40" title="Single pane">
          <Square size={14} />
        </button>
        <button disabled className="p-1 rounded text-text-muted disabled:opacity-40" title="Split horizontal">
          <Columns size={14} />
        </button>
        <button disabled className="p-1 rounded text-text-muted disabled:opacity-40" title="Split vertical">
          <Rows size={14} />
        </button>
        <button disabled className="p-1 rounded text-text-muted disabled:opacity-40" title="Grid">
          <GridFour size={14} />
        </button>
      </div>
    </div>
  )
}
