// spa/src/components/dock/useDockCards.ts — the dock's cards over time (U3 plan D8, spec §7). A card is bound to its approval:
// when the approval is no longer open the card locks at once (「題目已變更」), or says what the terminal answered
// (「已在終端機回答：X」), and goes away after a moment. A card whose answer THIS dock just sent goes away without the lock.
import { useCallback, useEffect, useRef, useState } from 'react'
import { ANSWERED_NOTE_MS, LOCK_CLOSE_MS, openAsks, terminalAnswerText, type CardPhase, type OpenAsk } from '../../lib/conversations/asks'
import { dockKey, forgetDockDraft } from '../../lib/conversations/dock-memory'
import type { ConversationApproval, ConversationItem } from '../../lib/conversations/types'

export interface DockCard {
  ask: OpenAsk
  phase: CardPhase
}

/** `hidden`: this dock's own answer is in flight and the approval has already closed — shown only if that send fails. */
interface Closing { ask: OpenAsk; until: number; hidden?: boolean }

export interface DockCards {
  /** Closing cards first (they leave in a moment), then the open ones in the order the daemon holds them. */
  cards: DockCard[]
  /** This dock is sending an answer for the approval: its close is ours, not a change under the card. */
  markAnswered: (approvalId: string) => void
  /**
   * The send has an answer. `ok`: the close (already seen or still to come) stays ours. Not `ok`: it was not — the card is
   * open again if the approval is, and if the approval closed meanwhile (another client won) the lock shows after all.
   */
  finishAnswered: (approvalId: string, ok: boolean) => void
}

export function useDockCards(paneKey: string, scope: string, approvals: readonly ConversationApproval[], items: readonly ConversationItem[]): DockCards {
  const open = openAsks(approvals)
  const seen = useRef(new Map<string, OpenAsk>())
  const answeredHere = useRef(new Set<string>())
  const [closing, setClosing] = useState<Closing[]>([])
  const itemsRef = useRef(items)
  useEffect(() => { itemsRef.current = items }, [items])

  // Another conversation (/clear, a relay, another host): the old one's approvals leaving are not closings of this one's.
  useEffect(() => {
    seen.current.clear()
    answeredHere.current.clear()
    setClosing([])
  }, [scope])

  // The approvals the dock saw open and now does not see: they closed.
  const openKey = open.map((a) => a.id).join('\0')
  useEffect(() => {
    const nowOpen = new Map(open.map((a) => [a.id, a] as const))
    const gone: Closing[] = []
    for (const [id, ask] of seen.current) {
      if (nowOpen.has(id)) continue
      seen.current.delete(id)
      forgetDockDraft(dockKey(paneKey, id))
      if (answeredHere.current.has(id)) {
        // Our own answer is in flight: its close is ours — unless the send turns out to have lost (finishAnswered).
        gone.push({ ask, until: Infinity, hidden: true })
        continue
      }
      const said = terminalAnswerText(itemsRef.current, ask.toolUseId) !== null
      gone.push({ ask, until: Date.now() + (said ? ANSWERED_NOTE_MS : LOCK_CLOSE_MS) })
    }
    for (const [id, ask] of nowOpen) seen.current.set(id, ask)
    if (gone.length > 0) setClosing((c) => [...c, ...gone])
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `open` is derived from `approvals`; its ids are the key
  }, [openKey, paneKey])

  // A closing card leaves when its time is up.
  useEffect(() => {
    if (closing.length === 0) return
    const next = Math.min(...closing.map((c) => c.until))
    if (!Number.isFinite(next)) return // only hidden entries: they wait for their send
    const timer = setTimeout(() => setClosing((c) => c.filter((x) => x.until > Date.now())), Math.max(0, next - Date.now()))
    return () => clearTimeout(timer)
  }, [closing])

  const markAnswered = useCallback((id: string) => { answeredHere.current.add(id) }, [])
  const finishAnswered = useCallback((id: string, ok: boolean) => {
    if (ok) {
      // the close stays ours: a hidden entry goes, and a close still to come finds the mark
      setClosing((c) => c.filter((x) => x.ask.id !== id))
      return
    }
    answeredHere.current.delete(id)
    setClosing((c) => c.map((x) => {
      if (x.ask.id !== id || !x.hidden) return x
      const said = terminalAnswerText(itemsRef.current, x.ask.toolUseId) !== null
      return { ask: x.ask, until: Date.now() + (said ? ANSWERED_NOTE_MS : LOCK_CLOSE_MS) }
    }))
  }, [])

  const cards: DockCard[] = [
    ...closing.filter((c) => !c.hidden).map((c): DockCard => {
      const text = terminalAnswerText(items, c.ask.toolUseId)
      return { ask: c.ask, phase: text !== null ? { kind: 'answered_in_terminal', text } : { kind: 'changed' } }
    }),
    ...open.map((ask): DockCard => ({ ask, phase: { kind: 'open' } })),
  ]
  return { cards, markAnswered, finishAnswered }
}
