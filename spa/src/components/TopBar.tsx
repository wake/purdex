// spa/src/components/TopBar.tsx
import { Terminal, Lightning, Stop } from '@phosphor-icons/react'

interface Props {
  sessionName: string
  mode: string
  onModeSwitch: () => void
  onInterrupt: () => void
}

export default function TopBar({ sessionName, mode, onModeSwitch, onInterrupt }: Props) {
  return (
    <div className="h-10 bg-gray-900 border-b border-gray-800 flex items-center px-3 gap-3">
      <span className="text-sm text-gray-200 font-medium truncate">{sessionName}</span>

      <div className="flex-1" />

      {/* Mode switch */}
      <button
        data-testid="mode-switch"
        onClick={onModeSwitch}
        className="flex items-center gap-1.5 px-2 py-1 rounded text-xs cursor-pointer hover:bg-gray-800"
      >
        {mode === 'stream' ? (
          <Lightning size={14} weight="fill" className="text-blue-400" />
        ) : (
          <Terminal size={14} className="text-gray-400" />
        )}
        <span className="text-gray-400">{mode}</span>
      </button>

      {/* Interrupt — stream mode only */}
      {mode === 'stream' && (
        <button
          data-testid="interrupt-btn"
          onClick={onInterrupt}
          className="flex items-center gap-1 px-2 py-1 rounded text-xs cursor-pointer text-red-400 hover:bg-gray-800"
        >
          <Stop size={14} weight="fill" />
          <span>Stop</span>
        </button>
      )}
    </div>
  )
}
