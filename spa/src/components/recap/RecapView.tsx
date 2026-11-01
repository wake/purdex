// spa/src/components/recap/RecapView.tsx — one session's recap history, in the panel area (plan T14b; look and states from docs/pages/recap-view-mock.html).
//
// Header: back, 「{name} 的 recap」, the regenerate button. Under it one status line (generating / ended / no turn summaries yet / newer turns waiting / up to date).
// Body, newest first: the turns after the newest recap (decision 2 = B), then the recaps — the newest open, each one a time, its turn span and its trigger, the headline,
// and open the fields 完成／目前／已定／等你／下一步 and that span's turn summaries.
// Opening the view reads the recaps, and asks for one ("open") when the daemon can make one and turn summaries are waiting; a `dialogue.recap` event reloads (the store does).
import { useEffect, useState } from 'react'
import { ArrowsClockwise, CaretDown, CaretLeft, CaretRight, ClockCounterClockwise } from '@phosphor-icons/react'
import { selectDialogueEntry, summariesInRange, useDialogueStore } from '../../stores/useDialogueStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { keepFocus } from '../../lib/keep-focus'
import { TURN_SUMMARIES_LIMIT, type Recap, type TurnSummary } from '../../lib/dialogue/types'
import { clockOf } from './recap-text'

interface Props {
  hostId: string
  sessionId: string
  /** Who the recap is of (the heading's name). */
  label: string
  backLabel: string
  onBack: () => void
}

export function RecapView({ hostId, sessionId, label, backLabel, onBack }: Props) {
  const t = useI18nStore((s) => s.t)
  const entry = useDialogueStore((s) => selectDialogueEntry(s, hostId, sessionId))
  const [notice, setNotice] = useState(false)
  const [afterOpen, setAfterOpen] = useState(true)
  const [expanded, setExpanded] = useState<Record<number, boolean>>({})
  const [baseline, setBaseline] = useState<number | null>(null)

  // Open: read the recaps, then ask for a new one when the daemon can make it and turn summaries are waiting.
  useEffect(() => {
    let alive = true
    void (async () => {
      const dlg = useDialogueStore.getState()
      await dlg.loadRecaps(hostId, sessionId)
      if (!alive) return
      const e = selectDialogueEntry(useDialogueStore.getState(), hostId, sessionId)
      const r = e?.recaps
      if (r && r.can_generate && r.newest_turn_no > r.last_recap_to_no && !e.generating) await dlg.regenerate(hostId, sessionId, 'open')
    })()
    return () => { alive = false }
  }, [hostId, sessionId])

  const recaps = entry?.recaps ?? null
  const lastTo = recaps?.last_recap_to_no ?? 0
  const newest = recaps?.newest_turn_no ?? 0
  // The turns after the newest recap: read whenever the recaps answer changes what they are.
  useEffect(() => {
    if (recaps === null || recaps.newest_turn_no <= recaps.last_recap_to_no) return
    void useDialogueStore.getState().loadSummariesAfter(hostId, sessionId, recaps.last_recap_to_no)
  }, [hostId, sessionId, recaps?.last_recap_to_no, recaps?.newest_turn_no]) // eslint-disable-line react-hooks/exhaustive-deps

  if (recaps !== null && baseline === null) setBaseline(recaps.recaps[0]?.id ?? 0)
  const pending = newest > lastTo ? summariesInRange(entry, lastTo + 1, Number.MAX_SAFE_INTEGER) : []
  const generating = entry?.generating === true
  const canGenerate = recaps?.can_generate === true
  const failed = recaps === null && entry !== undefined && !entry.loading
  // The number of turns not in a recap is a difference of turn numbers: the list of summaries is capped (TURN_SUMMARIES_LIMIT).
  const newCount = Math.max(0, newest - lastTo)

  let status: React.ReactNode
  let tip: string
  let tone: 'ok' | 'acc' | 'mut' = 'mut'
  let can = false
  if (generating) { status = <><ArrowsClockwise size={11} className="animate-spin mt-0.5 flex-shrink-0" /><span>{t('recap.status.generating')}</span></>; tip = t('recap.regen.tip.generating') }
  else if (entry?.loadFailed === true) { status = <span>{t('recap.status.load_failed')}</span>; tip = '' }
  else if (recaps === null) { status = <span>{failed ? t('recap.status.load_failed') : t('recap.status.loading')}</span>; tip = '' }
  else if (!canGenerate) { status = <span>{t('recap.status.ended')}</span>; tip = t('recap.regen.tip.ended') }
  else if (newest === 0) { status = <span>{t('recap.status.no_turns')}</span>; tip = t('recap.regen.tip.no_turns') }
  else if (newest > lastTo) { status = <span>{t('recap.status.newer', { n: newCount })}</span>; tone = 'acc'; can = true; tip = t('recap.regen.tip.newer') }
  else { status = <span>{t('recap.status.latest', { to: lastTo })}</span>; tone = 'ok'; tip = t('recap.regen.tip.latest') }

  const regenerate = async () => {
    setNotice(false)
    const outcome = await useDialogueStore.getState().regenerate(hostId, sessionId, 'manual')
    if (outcome === 'error') setNotice(true)
  }

  const pendingLabel = !canGenerate ? t('recap.after.ended', { n: newCount }) : generating ? t('recap.after.generating', { n: newCount }) : t('recap.after.live', { n: newCount })
  const dot = tone === 'ok' ? 'bg-status-success' : tone === 'acc' ? 'bg-accent' : 'bg-text-muted'

  return (
    <div data-testid="recap-view" className="text-xs text-text-primary flex flex-col flex-1 min-h-0">
      <div className="flex items-center gap-1.5 px-2 h-8 border-b border-border-subtle flex-shrink-0">
        <button
          type="button"
          data-testid="recap-back"
          onMouseDown={keepFocus}
          onClick={onBack}
          title={backLabel}
          aria-label={backLabel}
          className="px-1 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer flex-shrink-0"
        >
          <CaretLeft size={12} />
        </button>
        <span data-testid="recap-title" className="truncate min-w-0 flex-1 font-medium">{t('recap.view.title', { name: label })}</span>
        <button
          type="button"
          data-testid="recap-regen"
          disabled={!can || generating}
          onMouseDown={keepFocus}
          onClick={() => { void regenerate() }}
          title={tip}
          className="flex items-center gap-1 px-1.5 py-0.5 rounded text-[11px] text-text-secondary flex-shrink-0 whitespace-nowrap enabled:hover:text-text-primary enabled:hover:bg-surface-hover enabled:cursor-pointer disabled:opacity-50"
        >
          <ArrowsClockwise size={12} className={generating ? 'animate-spin' : ''} />
          <span>{generating ? t('recap.regen.busy') : t('recap.regen')}</span>
        </button>
      </div>
      <div data-testid="recap-status" className={`flex-shrink-0 flex items-start gap-1.5 px-3 py-1.5 text-[11px] leading-4 border-b border-border-subtle ${tone === 'acc' ? 'text-text-secondary' : 'text-text-muted'}`}>
        {!generating && <i aria-hidden="true" className={`w-1.5 h-1.5 rounded-full flex-shrink-0 mt-[5px] ${dot}`} />}
        {status}
      </div>
      {notice && <div data-testid="recap-notice" role="alert" className="flex-shrink-0 px-3 py-1 text-[11px] text-status-error">{t('recap.regen.error')}</div>}
      <div data-testid="recap-body" className="flex-1 min-h-0 overflow-y-auto pt-0.5 pb-2.5">
        {generating && (
          <div data-testid="recap-generating" className="mx-3 mt-2 mb-1 px-2.5 py-2 border border-dashed border-border-default rounded-md text-text-secondary">
            <div className="flex items-center gap-1.5 text-[11.5px]"><ArrowsClockwise size={12} className="animate-spin" />{t('recap.generating.title')}</div>
            {['88%', '64%', '76%'].map((w) => <div key={w} className="h-[7px] rounded bg-surface-hover mt-[7px] animate-pulse" style={{ width: w }} />)}
          </div>
        )}
        {pending.length > 0 && (
          <div data-testid="recap-after" className="mx-3 mt-2 mb-1 border border-border-subtle rounded-md bg-surface-hover/50">
            <button
              type="button"
              data-testid="recap-after-toggle"
              aria-expanded={afterOpen}
              onMouseDown={keepFocus}
              onClick={() => setAfterOpen((v) => !v)}
              className="flex items-center gap-1.5 w-full px-2 py-1.5 text-[11px] text-text-secondary hover:text-text-primary text-left cursor-pointer"
            >
              {afterOpen ? <CaretDown size={10} /> : <CaretRight size={10} />}
              {pendingLabel}
            </button>
            {afterOpen && (
              <div className="px-2 pb-1.5">
                <TurnRows list={pending} />
                {pending.length >= TURN_SUMMARIES_LIMIT && <p data-testid="recap-after-capped" className="mt-1 text-[10.5px] text-text-muted">{t('recap.after.capped', { n: TURN_SUMMARIES_LIMIT })}</p>}
              </div>
            )}
          </div>
        )}
        {recaps !== null && recaps.recaps.length === 0 && !generating && (
          <div data-testid="recap-empty" className="px-5 py-6 text-center text-text-muted text-[11.5px] leading-[18px]">
            <ClockCounterClockwise size={22} className="mx-auto mb-2" />
            <b className="block text-text-primary text-[13px] font-medium mb-1">{t('recap.empty.title')}</b>
            {!canGenerate ? t('recap.empty.ended') : newest === 0 ? t('recap.empty.no_turns') : t('recap.empty.press')}
          </div>
        )}
        {recaps?.recaps.map((r, i) => (
          <RecapItem
            key={r.id}
            hostId={hostId}
            sessionId={sessionId}
            recap={r}
            open={expanded[r.id] ?? i === 0}
            onToggle={() => setExpanded((m) => ({ ...m, [r.id]: !(m[r.id] ?? i === 0) }))}
            fresh={baseline !== null && r.id > baseline}
          />
        ))}
      </div>
    </div>
  )
}

const FIELD_ROW = 'relative pl-2.5 text-text-secondary leading-[18px] before:content-[""] before:absolute before:left-px before:top-2 before:w-[3px] before:h-[3px] before:rounded-full before:bg-text-muted'

function Section({ id, label, value, wait = false }: { id: string; label: string; value: string | string[]; wait?: boolean }) {
  const list = Array.isArray(value) ? value : null
  if (list ? list.length === 0 : value === '') return null
  return (
    <div
      data-testid={`recap-sec-${id}`}
      className={wait ? '-ml-2.5 pl-2 pr-2 pt-[5px] pb-1.5 border-l-2 border-status-warning-text/50 bg-status-warning/15 rounded-r-md' : ''}
    >
      <div className={`text-[10px] mb-px ${wait ? 'text-status-warning-text font-semibold' : 'text-text-muted'}`}>{label}</div>
      {list ? (
        <ul>{list.map((x, i) => <li key={i} className={wait ? `${FIELD_ROW} !text-text-primary before:!bg-status-warning-text` : FIELD_ROW}>{x}</li>)}</ul>
      ) : (
        <p className="text-text-secondary leading-[18px]">{value}</p>
      )}
    </div>
  )
}

function RecapItem({ hostId, sessionId, recap: r, open, onToggle, fresh }: { hostId: string; sessionId: string; recap: Recap; open: boolean; onToggle: () => void; fresh: boolean }) {
  const t = useI18nStore((s) => s.t)
  const entry = useDialogueStore((s) => selectDialogueEntry(s, hostId, sessionId))
  const [turnsOpen, setTurnsOpen] = useState(false)
  // The span's turn summaries are read once the recap is open (the count in the toggle needs them).
  useEffect(() => {
    if (open) void useDialogueStore.getState().loadTurnSummaries(hostId, sessionId, { from: r.from_no, to: r.to_no })
  }, [open, hostId, sessionId, r.from_no, r.to_no])
  const turns = summariesInRange(entry, r.from_no, r.to_no)
  const trigger = ['auto', 'open', 'manual'].includes(r.trigger) ? t(`recap.trigger.${r.trigger}`) : r.trigger
  return (
    <div data-testid="recap-item" className="px-3 pt-2 pb-[9px] border-b border-border-subtle last:border-b-0">
      <button
        type="button"
        data-testid="recap-meta"
        aria-expanded={open}
        onMouseDown={keepFocus}
        onClick={onToggle}
        className="flex items-center gap-1.5 w-full text-[10.5px] text-text-muted hover:text-text-secondary text-left tabular-nums cursor-pointer"
      >
        <span className="w-2.5 grid place-items-center">{open ? <CaretDown size={10} /> : <CaretRight size={10} />}</span>
        <span>{clockOf(r.at)}</span>
        <span>· {t('recap.item.range', { from: r.from_no, to: r.to_no })}</span>
        <span>· {trigger}</span>
        {fresh && <span data-testid="recap-new" className="text-[9.5px] leading-[15px] px-[5px] rounded bg-accent-muted text-text-primary">{t('recap.item.new')}</span>}
      </button>
      <button
        type="button"
        data-testid="recap-headline"
        onMouseDown={keepFocus}
        onClick={onToggle}
        className={`block w-full text-left mt-0.5 pl-4 text-[12.5px] leading-[18px] cursor-pointer ${open ? 'font-medium text-text-primary' : 'font-normal text-text-secondary line-clamp-2'}`}
      >
        {r.headline}
      </button>
      {open && (
        <div className="pl-4 mt-[7px] flex flex-col gap-2">
          <Section id="done" label={t('recap.sec.done')} value={r.done} />
          <Section id="now" label={t('recap.sec.now')} value={r.now} />
          <Section id="decided" label={t('recap.sec.decided')} value={r.decided} />
          <Section id="waiting" label={`${t('recap.sec.waiting')} ${r.waiting.length}`} value={r.waiting} wait />
          <Section id="next" label={t('recap.sec.next')} value={r.next} />
          <div>
            <button
              type="button"
              data-testid="recap-turns-toggle"
              aria-expanded={turnsOpen}
              onMouseDown={keepFocus}
              onClick={() => setTurnsOpen((v) => !v)}
              className="flex items-center gap-1 text-[11px] text-text-secondary hover:text-text-primary py-px cursor-pointer"
            >
              {turnsOpen ? <CaretDown size={10} /> : <CaretRight size={10} />}
              {turns.length > 0 ? t('recap.turns.count', { n: turns.length }) : t('recap.turns.toggle')}
            </button>
            {turnsOpen && <div className="mt-0.5"><TurnRows list={turns} /></div>}
          </div>
        </div>
      )}
    </div>
  )
}

/** Turn summaries, newest first: number, time, title, summary. */
function TurnRows({ list }: { list: readonly TurnSummary[] }) {
  return (
    <div className="flex flex-col">
      {list.slice().reverse().map((x) => (
        <div key={x.turn_no} data-testid="recap-turn-row" className="grid grid-cols-[22px_34px_minmax(0,1fr)] gap-x-1.5 pt-[5px] pb-1 border-t border-dashed border-border-subtle first:border-t-0">
          <span data-testid="recap-turn-no" className="text-right text-text-muted text-[10.5px] leading-[18px] tabular-nums">{x.turn_no}</span>
          <span className="text-text-muted text-[10.5px] leading-[18px] tabular-nums">{clockOf(x.at)}</span>
          <div>
            <b className="block font-medium text-text-primary text-xs leading-[18px]">{x.title}</b>
            <p className="mt-px text-[11px] leading-4 text-text-secondary">{x.summary}</p>
          </div>
        </div>
      ))}
    </div>
  )
}
