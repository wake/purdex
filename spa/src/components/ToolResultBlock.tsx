// spa/src/components/ToolResultBlock.tsx
import { useState } from 'react'
import { CheckCircle, XCircle, CaretRight, CaretDown } from '@phosphor-icons/react'

interface Props {
  content: string
  isError: boolean
}

export default function ToolResultBlock({ content, isError }: Props) {
  const [expanded, setExpanded] = useState(false)
  const summary = content.slice(0, 80) + (content.length > 80 ? '...' : '')

  return (
    <div
      data-testid="tool-result-block"
      className={`rounded-lg border my-1 overflow-hidden ${
        isError ? 'border-[#302a2a] bg-[#1f1b1b]' : 'border-[#2a302a] bg-[#1b1f1b]'
      }`}
    >
      <button
        data-testid="tool-result-header"
        className={`w-full flex items-center gap-2 px-3 py-1.5 cursor-pointer text-left text-xs ${
          isError ? 'text-[#c77] hover:bg-[#251f1f]' : 'text-[#8bc] hover:bg-[#1f251f]'
        }`}
        onClick={() => setExpanded(v => !v)}
      >
        {expanded ? <CaretDown size={10} /> : <CaretRight size={10} />}
        {isError ? <XCircle size={14} /> : <CheckCircle size={14} />}
        <span className="truncate flex-1">{summary}</span>
      </button>
      {expanded && (
        <div
          data-testid="tool-result-content"
          className={`border-t px-3 py-2 text-xs font-mono whitespace-pre-wrap max-h-60 overflow-auto ${
            isError ? 'border-[#302a2a] text-[#c99]' : 'border-[#2a302a] text-[#9b9]'
          }`}
        >
          {content}
        </div>
      )}
    </div>
  )
}
