// spa/src/components/ToolCallBlock.tsx
import { useState } from 'react'
import { CaretRight, CaretDown, Wrench } from '@phosphor-icons/react'

interface Props {
  tool: string
  input: Record<string, unknown>
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
    case 'Glob':
      return (input.pattern as string) ?? ''
    case 'Agent':
      return (input.description as string) ?? ''
    default:
      return JSON.stringify(input).slice(0, 80)
  }
}

export default function ToolCallBlock({ tool, input }: Props) {
  const [expanded, setExpanded] = useState(false)
  const summary = getSummary(tool, input)

  return (
    <div className="rounded-lg border border-[#2a2a2a] bg-[#1e1e1e] text-sm my-1 overflow-hidden">
      <button
        data-testid="tool-header"
        className="w-full flex items-center gap-2 px-3 py-1.5 hover:bg-[#252525] cursor-pointer text-left"
        onClick={() => setExpanded(v => !v)}
      >
        {expanded ? (
          <CaretDown size={12} className="text-[#777] flex-shrink-0" />
        ) : (
          <CaretRight size={12} className="text-[#777] flex-shrink-0" />
        )}
        <Wrench size={16} data-testid="tool-icon-wrench" className="text-[#aaa] flex-shrink-0" />
        <span className="text-[#ddd] font-semibold">{tool}</span>
        {summary && (
          <span className="text-[#888] truncate flex-1 min-w-0">{summary}</span>
        )}
      </button>
      {expanded && (
        <div data-testid="tool-detail" className="border-t border-[#2a2a2a] px-3 py-2 bg-[#161616]">
          <pre className="text-xs text-[#aaa] whitespace-pre-wrap break-all overflow-auto max-h-60">
            {JSON.stringify(input, null, 2)}
          </pre>
        </div>
      )}
    </div>
  )
}
