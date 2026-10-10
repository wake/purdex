// spa/src/components/dock/QuestionDock.tsx — the dock (U3 spec §7, plan D8): the open question of the conversation as one card above
// the input, shared by the deck and the chat. Questions only (a `hook_ask`; permission prompts are P8b). Every card is bound to
// its approval: when it closes or changes the card locks at once, with no queued taps and no retry. What the reader has picked or
// typed lives in `dock-memory` (the dock unmounts with its tab; the question is still open when they return).
import { useState } from 'react'
import { ChatCircleDots, Check } from '@phosphor-icons/react'
import { answerAsk, buildAnswers, checkReply, replyToAsk, type AskDecideResult, type OpenAsk, type ReplyRefusal } from '../../lib/conversations/asks'
import { dockKey, readDockDraft, writeDockDraft, type DockDraft } from '../../lib/conversations/dock-memory'
import { selectAskChatV1, useNexHostStore } from '../../stores/useNexHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { DeckFooterContext } from '../deck/footer-context'
import { useDockCards, type DockCard } from './useDockCards'

const REPLY_REFUSAL_KEY: Record<ReplyRefusal, string> = {
  empty: 'deck.dock.reply_empty', too_long: 'deck.dock.reply_too_long', bad_characters: 'deck.dock.reply_bad',
}

export function QuestionDock({ ctx }: { ctx: DeckFooterContext }) {
  const { cards, markAnswered, unmarkAnswered } = useDockCards(ctx.paneKey, ctx.approvals, ctx.items)
  const askChat = useNexHostStore(selectAskChatV1(ctx.hostId))
  const t = useI18nStore((s) => s.t)
  const card = cards[0]
  if (!card) return null
  return (
    <div data-testid="question-dock" className="border-t border-border-subtle px-3 pt-2">
      {/* re-keyed by approval: a card for another request is another card, with its own draft and no state carried over */}
      <AskCard key={card.ask.id} card={card} ctx={ctx} askChat={askChat} markAnswered={markAnswered} unmarkAnswered={unmarkAnswered} />
      {cards.length > 1 && <div data-testid="dock-more" className="mt-1 text-xs text-text-muted">{t('deck.dock.more', { n: cards.length - 1 })}</div>}
    </div>
  )
}

interface CardProps {
  card: DockCard
  ctx: DeckFooterContext
  askChat: boolean
  markAnswered: (id: string) => void
  unmarkAnswered: (id: string) => void
}

type Failure = 'network' | 'failed' | ReplyRefusal

function AskCard({ card, ctx, askChat, markAnswered, unmarkAnswered }: CardProps) {
  const t = useI18nStore((s) => s.t)
  const { ask, phase } = card
  const key = dockKey(ctx.paneKey, ask.id)
  const [draft, setDraft] = useState<DockDraft>(() => readDockDraft(key, ask.questions.length))
  const [pending, setPending] = useState(false)
  const [failure, setFailure] = useState<Failure | null>(null)
  const [terminalOnly, setTerminalOnly] = useState(false)
  const update = (next: DockDraft) => { setDraft(next); writeDockDraft(key, next); setFailure(null) }

  if (phase.kind === 'changed') {
    return <Shell><div data-testid="dock-locked" role="status" className="py-2 text-sm text-text-muted">{t('deck.dock.changed')}</div></Shell>
  }
  if (phase.kind === 'answered_in_terminal') {
    return <Shell><div data-testid="dock-answered-terminal" role="status" className="py-2 text-sm text-text-muted">{t('deck.dock.answered_terminal', { text: phase.text })}</div></Shell>
  }

  const readOnly = ask.terminalOnly || terminalOnly
  const answers = buildAnswers(ask.questions, draft.picks)

  const settle = async (send: () => Promise<AskDecideResult>) => {
    setPending(true)
    markAnswered(ask.id) // before the send: the approval's close can outrun the answer, and it must read as ours
    const r = await send()
    setPending(false)
    if (r.ok) return
    unmarkAnswered(ask.id)
    if (r.reason === 'terminal_only') setTerminalOnly(true)
    else if (r.reason === 'network' || r.reason === 'failed') setFailure(r.reason)
    // 'changed': the approval closed under the card; the close lands as the lock, with no retry offered
  }
  const submit = () => { if (answers && !pending) void settle(() => answerAsk(ctx.hostId, ask.id, answers)) }
  const sendReply = () => {
    if (pending) return
    const r = checkReply(draft.reply)
    if (!r.ok) { setFailure(r.reason); return }
    void settle(() => replyToAsk(ctx.hostId, ask.id, r.text))
  }

  return (
    <Shell>
      <div className="mb-2 flex items-center gap-2 text-sm font-medium text-text-primary">
        <ChatCircleDots size={16} weight="fill" className="shrink-0 text-accent" />
        <span>{t('deck.dock.title')}</span>
      </div>
      {readOnly ? <ReadOnlyQuestions ask={ask} /> : draft.replying ? null : (
        <Questions ask={ask} draft={draft} disabled={pending} onChange={update} />
      )}
      {draft.replying && !readOnly && (
        <textarea
          data-testid="dock-reply" value={draft.reply} rows={3} disabled={pending} placeholder={t('deck.dock.reply_placeholder')}
          onChange={(e) => update({ ...draft, reply: e.target.value })}
          className="mb-2 w-full resize-none rounded border border-border-subtle bg-surface-primary px-2 py-1 text-sm text-text-primary"
        />
      )}
      {readOnly && <div data-testid="dock-terminal-only" className="mb-2 text-xs text-text-muted">{t('deck.dock.terminal_only')}</div>}
      {failure && <div data-testid="dock-failure" role="alert" className="mb-1 text-xs text-status-error">{t(failure === 'network' ? 'deck.dock.err_network' : failure === 'failed' ? 'deck.dock.err_failed' : REPLY_REFUSAL_KEY[failure])}</div>}
      <div className="flex flex-wrap items-center gap-2 pb-2">
        {readOnly ? (
          <button type="button" data-testid="dock-open-terminal" onClick={ctx.onSwitchToTerminal} className={BTN_PRIMARY}>{t('deck.dock.open_terminal')}</button>
        ) : draft.replying ? (
          <>
            <button type="button" data-testid="dock-send-reply" disabled={pending} onClick={sendReply} className={BTN_PRIMARY}>{t('deck.dock.reply_send')}</button>
            <button type="button" data-testid="dock-back" disabled={pending} onClick={() => update({ ...draft, replying: false })} className={BTN}>{t('deck.dock.back')}</button>
          </>
        ) : (
          <>
            <button type="button" data-testid="dock-submit" disabled={!answers || pending} onClick={submit} className={BTN_PRIMARY}>{t('deck.dock.submit')}</button>
            {askChat && <button type="button" data-testid="dock-chat" disabled={pending} onClick={() => update({ ...draft, replying: true })} className={BTN}>{t('deck.dock.chat')}</button>}
          </>
        )}
        {!readOnly && <button type="button" data-testid="dock-terminal" onClick={ctx.onSwitchToTerminal} className={BTN}>{t('deck.dock.terminal')}</button>}
      </div>
    </Shell>
  )
}

const BTN = 'rounded border border-border-subtle px-3 py-1 text-xs text-text-secondary hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-50'
const BTN_PRIMARY = 'rounded bg-accent px-3 py-1 text-xs text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50'

function Shell({ children }: { children: React.ReactNode }) {
  return <div data-testid="dock-card" className="rounded-lg border border-accent/40 bg-surface-secondary px-3 pt-2">{children}</div>
}

function ReadOnlyQuestions({ ask }: { ask: OpenAsk }) {
  return (
    <div className="mb-2 space-y-2">
      {ask.questions.map((q, i) => (
        <div key={i} data-testid="dock-question">
          {q.header && <div className="text-xs text-text-muted">{q.header}</div>}
          <div className="text-sm text-text-primary">{q.question}</div>
          <ul className="text-xs text-text-secondary">{q.options.map((o) => <li key={o.label}>· {o.label}</li>)}</ul>
        </div>
      ))}
    </div>
  )
}

function Questions({ ask, draft, disabled, onChange }: { ask: OpenAsk; draft: DockDraft; disabled: boolean; onChange: (d: DockDraft) => void }) {
  const t = useI18nStore((s) => s.t)
  const setPick = (i: number, pick: DockDraft['picks'][number]) => onChange({ ...draft, picks: draft.picks.map((p, j) => (j === i ? pick : p)) })
  return (
    <div className="mb-2 space-y-3">
      {ask.questions.map((q, i) => {
        const pick = draft.picks[i]
        const toggle = (label: string) => {
          const on = pick.chosen.includes(label)
          const chosen = q.multiple ? (on ? pick.chosen.filter((c) => c !== label) : [...pick.chosen, label]) : (on ? [] : [label])
          // a single-select answers with one thing: picking an option drops the free text, typing free text drops the pick
          setPick(i, { chosen, other: q.multiple ? pick.other : '' })
        }
        return (
          <div key={i} data-testid="dock-question">
            {q.header && <div className="text-xs text-text-muted">{q.header}</div>}
            <div className="text-sm text-text-primary">{q.question}{q.multiple && <span className="ml-1 text-xs text-text-muted">{t('deck.dock.multi')}</span>}</div>
            <div className="mt-1 flex flex-col gap-1">
              {q.options.map((o) => {
                const on = pick.chosen.includes(o.label)
                return (
                  <button
                    key={o.label} type="button" data-testid="dock-option" data-chosen={on} aria-pressed={on} disabled={disabled} onClick={() => toggle(o.label)}
                    className={`flex items-center gap-2 rounded border px-2 py-1 text-left text-sm ${on ? 'border-accent bg-accent/10 text-text-primary' : 'border-border-subtle text-text-secondary hover:text-text-primary'}`}
                  >
                    <span className="flex h-4 w-4 shrink-0 items-center justify-center rounded border border-border-subtle">{on && <Check size={10} weight="bold" />}</span>
                    <span>{o.label}{o.description && <span className="ml-1 text-xs text-text-muted">— {o.description}</span>}</span>
                  </button>
                )
              })}
              <input
                type="text" data-testid="dock-other" value={pick.other} disabled={disabled} placeholder={t('deck.dock.other')} aria-label={t('deck.dock.other')}
                onChange={(e) => setPick(i, { chosen: q.multiple ? pick.chosen : [], other: e.target.value })}
                className="rounded border border-border-subtle bg-surface-primary px-2 py-1 text-sm text-text-primary"
              />
            </div>
          </div>
        )
      })}
    </div>
  )
}
