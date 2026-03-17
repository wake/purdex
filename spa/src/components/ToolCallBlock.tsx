// spa/src/components/ToolCallBlock.tsx
import { useState } from 'react'
import {
  CaretRight,
  CaretDown,
  Terminal,
  File,
  PencilSimple,
  Globe,
  MagnifyingGlass,
} from '@phosphor-icons/react'

interface Props {
  tool: string
  input: Record<string, unknown>
}

function getToolIcon(tool: string) {
  switch (tool) {
    case 'Bash':
      return <Terminal size={14} className="text-yellow-400" />
    case 'Read':
    case 'Write':
      return <File size={14} className="text-blue-400" />
    case 'Edit':
      return <PencilSimple size={14} className="text-green-400" />
    case 'WebFetch':
      return <Globe size={14} className="text-purple-400" />
    case 'Grep':
    case 'Glob':
      return <MagnifyingGlass size={14} className="text-gray-400" />
    default:
      return <Terminal size={14} className="text-gray-400" />
  }
}

function getSummary(tool: string, input: Record<string, unknown>): string {
  switch (tool) {
    case 'Bash':
      return (input.command as string) ?? ''
    case 'Read':
    case 'Write':
    case 'Edit':
      return (input.file_path as string) ?? ''
    case 'WebFetch':
      return (input.url as string) ?? ''
    case 'Grep':
      return (input.pattern as string) ?? ''
    case 'Glob':
      return (input.pattern as string) ?? ''
    default:
      return JSON.stringify(input).slice(0, 80)
  }
}

export default function ToolCallBlock({ tool, input }: Props) {
  const [expanded, setExpanded] = useState(false)
  const summary = getSummary(tool, input)

  return (
    <div className="rounded-lg border border-gray-700 bg-gray-900 text-sm my-1 overflow-hidden">
      {/* Header */}
      <button
        data-testid="tool-header"
        className="w-full flex items-center gap-2 px-3 py-2 hover:bg-gray-800 cursor-pointer text-left"
        onClick={() => setExpanded(v => !v)}
      >
        {expanded ? (
          <CaretDown size={12} className="text-gray-500 flex-shrink-0" />
        ) : (
          <CaretRight size={12} className="text-gray-500 flex-shrink-0" />
        )}
        {getToolIcon(tool)}
        <span className="text-gray-300 font-medium">{tool}</span>
        {summary && (
          <span className="text-gray-500 truncate flex-1 min-w-0">{summary}</span>
        )}
      </button>

      {/* Detail */}
      {expanded && (
        <div
          data-testid="tool-detail"
          className="border-t border-gray-700 px-3 py-2 bg-gray-950"
        >
          <pre className="text-xs text-gray-300 whitespace-pre-wrap break-all overflow-auto max-h-60">
            {JSON.stringify(input, null, 2)}
          </pre>
        </div>
      )}
    </div>
  )
}
