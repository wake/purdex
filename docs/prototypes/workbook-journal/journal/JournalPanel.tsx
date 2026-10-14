// spa/src/components/journal/JournalPanel.tsx — the workbook's journal: a report plus an event stream, newest at the bottom.
// Opens scrolled to the bottom and follows new events unless the user scrolled up; then a pill counts what is below.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { ArrowDown, ChatCircleDots, CheckCircle, CheckSquare, Gear, PlusSquare, Question, X } from '@phosphor-icons/react'
import { FILTERS, KIND_STYLE, matchesFilter, type JournalEvent, type JournalFilter, type JournalKind } from './journal-types'

const ICONS: Record<JournalKind, typeof Gear> = {
  exec: Gear, done: CheckCircle, task: PlusSquare, taskdone: CheckSquare, comm: ChatCircleDots, req: Question,
}
const NEAR_BOTTOM = 24

interface Props {
  session: string
  roleLine: string
  live: boolean
  events: JournalEvent[]
  onClose?: () => void
}

export function JournalPanel({ session, roleLine, live, events, onClose }: Props) {
  const [filter, setFilter] = useState<JournalFilter>('all')
  const shown = useMemo(() => events.filter((e) => matchesFilter(e, filter)), [events, filter])
  const waiting = useMemo(() => events.filter((e) => e.pending).length, [events])
  const scroller = useRef<HTMLDivElement>(null)
  const pinned = useRef(true) // the user is at the bottom: follow
  const seen = useRef(shown.length)
  const [below, setBelow] = useState(0)

  const toBottom = useCallback((smooth = false) => {
    const el = scroller.current
    if (!el) return
    el.scrollTo({ top: el.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
    pinned.current = true
    seen.current = shown.length
    setBelow(0)
  }, [shown.length])

  // On open and on a filter change: straight to the bottom.
  useLayoutEffect(() => { toBottom() }, [filter]) // eslint-disable-line react-hooks/exhaustive-deps
  // New events: follow when pinned, else count them for the pill.
  useEffect(() => {
    if (pinned.current) toBottom(true)
    else setBelow(Math.max(0, shown.length - seen.current))
  }, [shown.length]) // eslint-disable-line react-hooks/exhaustive-deps

  const onScroll = () => {
    const el = scroller.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight <= NEAR_BOTTOM
    pinned.current = atBottom
    if (atBottom) { seen.current = shown.length; setBelow(0) }
  }

  const rows: React.ReactNode[] = []
  let day = ''
  shown.forEach((e, i) => {
    if (e.date !== day) {
      day = e.date
      rows.push(<DaySep key={`d-${day}`} date={day} />)
    }
    rows.push(<EventRow key={`${e.date}-${e.time}-${i}`} e={e} />)
  })

  return (
    <div data-testid="journal-panel" className="h-full flex flex-col text-xs text-text-primary bg-surface-elevated">
      <div className="px-3 pt-2.5 pb-2 border-b border-border-subtle flex-shrink-0">
        <div className="flex items-center gap-2">
          <span data-testid="journal-live" title={live ? '連線中，事件即時更新' : '離線'} className={`w-2 h-2 rounded-full flex-shrink-0 ${live ? 'bg-status-success animate-pulse' : 'bg-text-muted'}`} />
          <span className="text-sm font-medium truncate flex-1">{session}</span>
          {onClose && <button type="button" onClick={onClose} aria-label="關閉日誌" className="p-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover"><X size={12} /></button>}
        </div>
        <div className="mt-0.5 pl-4 text-[11px] text-text-muted truncate">{roleLine}</div>
        <div className="mt-2 flex flex-wrap gap-1" role="tablist">
          {FILTERS.map((f) => {
            const on = filter === f.id
            return (
              <button
                key={f.id}
                type="button"
                role="tab"
                aria-selected={on}
                onClick={() => setFilter(f.id)}
                className={`px-2 py-0.5 rounded-full border text-[11px] ${on ? 'border-accent text-text-primary bg-accent-muted' : 'border-border-default text-text-secondary hover:text-text-primary hover:bg-surface-hover'}`}
              >
                {f.label}{f.id === 'wait' ? ` ${waiting}` : ''}
              </button>
            )
          })}
        </div>
      </div>
      <div className="relative flex-1 min-h-0">
        <div ref={scroller} onScroll={onScroll} data-testid="journal-stream" className="absolute inset-0 overflow-y-auto px-3 py-2">
          {rows.length === 0 ? <div className="text-text-muted py-6 text-center">沒有符合的事件</div> : rows}
        </div>
        {below > 0 && (
          <button
            type="button"
            data-testid="journal-pill"
            onClick={() => toBottom(true)}
            className="absolute left-1/2 -translate-x-1/2 bottom-3 flex items-center gap-1 px-3 py-1 rounded-full bg-accent text-text-inverse shadow-lg text-[11px]"
          >
            <ArrowDown size={11} /> 往下有 {below} 則新事件
          </button>
        )}
      </div>
    </div>
  )
}

function DaySep({ date }: { date: string }) {
  return (
    <div className="flex items-center gap-2 my-2 text-[10.5px] text-text-muted select-none">
      <span className="flex-1 h-px bg-border-subtle" />{date}<span className="flex-1 h-px bg-border-subtle" />
    </div>
  )
}

function EventRow({ e }: { e: JournalEvent }) {
  const k = KIND_STYLE[e.kind]
  const Icon = ICONS[e.kind]
  return (
    <div data-testid="journal-event" data-kind={e.kind} className="flex gap-2 py-1.5 border-b border-border-subtle/50 last:border-b-0">
      <span className="w-9 flex-shrink-0 tabular-nums text-[10.5px] text-text-muted pt-0.5">{e.time}</span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5 flex-wrap">
          <span className="inline-flex items-center gap-1 px-1.5 rounded text-[10px] font-medium" style={{ color: k.color, background: `${k.color}22`, border: `1px solid ${k.color}66` }}>
            <Icon size={10} weight="bold" />{e.kindLabel}
          </span>
          <span className="text-[10.5px] text-text-secondary break-words min-w-0">{e.actor}</span>
          {e.pending && <span className="text-[10px] px-1 rounded bg-status-warning/20 text-status-warning-text">等我回覆</span>}
        </div>
        <div className="mt-0.5 leading-[17px] text-text-primary break-words">{e.title}</div>
        {e.body !== '' && <div className="mt-0.5 text-[11px] leading-[16px] text-text-muted break-words">{e.body}</div>}
        {e.refs.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-1">
            {e.refs.map((r) => <span key={r} className="px-1 rounded bg-surface-hover text-[10px] text-text-secondary font-mono">{r}</span>)}
          </div>
        )}
      </div>
    </div>
  )
}
