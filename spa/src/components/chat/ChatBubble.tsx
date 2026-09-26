// spa/src/components/chat/ChatBubble.tsx — one bubble of the chat transcript
// (spec §5): the agent on the left, you on the right. Geometry borrowed from
// Aigora's AI bubble (8px radius, 7px/11px padding, fit-content width) for
// both sides. The cap is a share of the pane on a phone-width container and a
// reading measure on a wide one — the transcript root is the `@container`.
import type { ReactNode } from 'react'

interface Props {
  side: 'agent' | 'user'
  /** Extra classes on the bubble itself (a slash command's mono face, the pending line's dim). */
  className?: string
  children: ReactNode
}

const BASE = 'rounded-lg px-[11px] py-[7px] w-fit max-w-[85%] @md:max-w-[70ch] min-w-0 break-words'
const SIDE = {
  agent: 'bg-surface-secondary border border-border-subtle',
  user: 'bg-accent-muted text-text-primary',
} as const

export default function ChatBubble({ side, className, children }: Props) {
  return (
    <div className={`flex ${side === 'agent' ? 'justify-start' : 'justify-end'}`}>
      <div data-testid={`chat-bubble-${side}`} className={`${BASE} ${SIDE[side]}${className ? ` ${className}` : ''}`}>
        {children}
      </div>
    </div>
  )
}
