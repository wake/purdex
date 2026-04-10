import { useState, useRef, useEffect } from 'react'
import { Lightning, CaretDown } from '@phosphor-icons/react'
import { useCommands, type ResolvedCommand } from '../hooks/useCommands'

interface Props {
  hostId: string
  workspaceId?: string | null
  onExecute: (cmd: ResolvedCommand) => void
  disabled?: boolean
}

export function QuickCommandMenu({ hostId, workspaceId, onExecute, disabled }: Props) {
  const commands = useCommands({ hostId, workspaceId })
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const handle = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handle)
    return () => document.removeEventListener('mousedown', handle)
  }, [open])

  if (commands.length === 0) return null

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen(!open)}
        disabled={disabled}
        className="flex items-center gap-1 px-2 py-1 rounded text-xs text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer disabled:opacity-50"
        title="Quick Commands"
      >
        <Lightning size={14} />
        <CaretDown size={10} />
      </button>
      {open && (
        <div className="absolute top-full left-0 mt-1 w-56 bg-surface-secondary border border-border-default rounded-lg shadow-lg z-50 py-1">
          {commands.map((cmd) => (
            <button
              key={`${cmd.source}-${cmd.id}`}
              onClick={() => { onExecute(cmd); setOpen(false) }}
              className="w-full text-left px-3 py-1.5 text-xs text-text-primary hover:bg-surface-tertiary cursor-pointer flex items-center gap-2"
            >
              <span className="flex-1 truncate">{cmd.name}</span>
              {cmd.category && (
                <span className="text-[10px] text-text-muted bg-surface-primary px-1.5 py-0.5 rounded">{cmd.category}</span>
              )}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
