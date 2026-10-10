// spa/src/components/SessionViewPlaceholder.tsx — what the deck / chat view of a session pane shows until the real views
// land (U3-1c / U3-3). It exists so the swap (U3 plan D2) is complete and testable now: it is the absolute sibling over
// the terminal, and it takes focus when the pane switches to it, as the real view's input will.
import { useRef } from 'react'
import { useActivationFocus } from '../hooks/useActivationFocus'
import { useI18nStore } from '../stores/useI18nStore'
import type { ConversationView } from '../stores/useSessionViewStore'

export function SessionViewPlaceholder({ view, isActive, isFocusTarget }: { view: ConversationView; isActive: boolean; isFocusTarget: boolean }) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)
  useActivationFocus(isActive, isFocusTarget, () => ref.current?.focus(), { raf: true })
  return (
    <div
      ref={ref}
      tabIndex={-1}
      data-testid={`session-view-${view}`}
      className="absolute inset-0 flex items-center justify-center bg-surface-primary text-sm text-text-secondary outline-none"
    >
      {t(view === 'deck' ? 'session.view.deck_soon' : 'session.view.chat_soon')}
    </div>
  )
}
