// spa/src/components/ToolResultBlock.tsx
// TODO: theme tokens for tool-result error/success color variants
import { useState } from 'react'
import { CheckCircle, XCircle, Prohibit, CaretRight, CaretDown } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { toolResultFacts, type ToolResultFacts } from '../lib/nex/tool-result-facts'

interface Props {
  content: string
  isError: boolean
  /**
   * N2 overlay for the header (P-B3.2 spec §4.4 R3 / R4): `status: 'denied'`
   * overrides `isError` (a denial is not a failure — neutral colours, the
   * Prohibit icon and a `denied` badge); `file` / `diff` / `output` feed the
   * facts span. Absent → today's DOM byte-for-byte (baseline snapshots).
   */
  facts?: ToolResultFacts
}

export default function ToolResultBlock({ content, isError, facts }: Props) {
  const t = useI18nStore((s) => s.t)
  const [expanded, setExpanded] = useState(false)
  const summary = content.slice(0, 80) + (content.length > 80 ? '...' : '')
  const denied = facts?.status === 'denied'
  // R3: the raw frame flags a denial as an error too; N2's status wins.
  const errorTone = isError && !denied
  const segs = toolResultFacts(facts, t)

  return (
    <div
      data-testid="tool-result-block"
      className={`rounded-lg border my-1 overflow-hidden ${
        errorTone ? 'border-[#302a2a] bg-[#1f1b1b]' : 'border-[#2a302a] bg-[#1b1f1b]'
      }`} /* TODO: theme token */
    >
      <button
        data-testid="tool-result-header"
        aria-expanded={expanded}
        className={`w-full flex items-center gap-2 px-3 py-1.5 cursor-pointer text-left text-xs ${
          errorTone ? 'text-[#c77] hover:bg-[#251f1f]' : 'text-[#8bc] hover:bg-[#1f251f]'
        }`} /* TODO: theme token */
        onClick={() => setExpanded(v => !v)}
      >
        {expanded ? <CaretDown size={10} /> : <CaretRight size={10} />}
        {denied ? <Prohibit size={14} /> : isError ? <XCircle size={14} /> : <CheckCircle size={14} />}
        <span className="truncate flex-1">{summary}</span>
        {segs.length > 0 && (
          <span data-testid="tool-result-facts" className="text-text-muted tabular-nums flex-shrink-0">{segs.join(' · ')}</span>
        )}
        {denied && (
          <span data-testid="tool-result-denied" className="text-status-warning flex-shrink-0">{t('execution.tool.denied')}</span>
        )}
      </button>
      {expanded && (
        <div
          data-testid="tool-result-content"
          className={`border-t px-3 py-2 text-xs font-mono whitespace-pre-wrap max-h-60 overflow-auto ${
            errorTone ? 'border-[#302a2a] text-[#c99]' : 'border-[#2a302a] text-[#9b9]'
          }`} /* TODO: theme token */
        >
          {content}
        </div>
      )}
    </div>
  )
}
