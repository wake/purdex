// spa/src/components/ThinkingBlock.tsx
import { useState } from 'react'
import { Brain, CaretRight, CaretDown } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  content: string
}

export default function ThinkingBlock({ content }: Props) {
  const t = useI18nStore((s) => s.t)
  const [expanded, setExpanded] = useState(false)

  return (
    <div className="border-l-2 border-border-default my-1">
      <button
        data-testid="thinking-header"
        aria-expanded={expanded}
        className="flex items-center gap-2 px-2.5 py-1.5 text-xs text-text-muted hover:text-text-secondary cursor-pointer w-full text-left"
        onClick={() => setExpanded(v => !v)}
      >
        <Brain size={14} />
        <span>{t('stream.thinking')}</span>
        <span className="ml-auto">
          {expanded ? <CaretDown size={10} /> : <CaretRight size={10} />}
        </span>
      </button>
      {expanded && (
        <div
          data-testid="thinking-content"
          className="px-2.5 pb-2 text-xs text-text-secondary leading-relaxed whitespace-pre-wrap font-mono"
        >
          {content}
        </div>
      )}
    </div>
  )
}
