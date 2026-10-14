// Prototype of the v4 journal card: header (+ roadmap/tasks toggles), pinned band, filters, stream.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowDown, ChatCircleDots, CheckCircle, CheckSquare, Gear, MapTrifold, PlusSquare, PushPin, Question, SquaresFour, X, Warning,
  Square, CheckSquare as CheckedBox,
} from '@phosphor-icons/react'
import { FILTERS, KIND_COLOR, kindLabel, matchesFilter, openItems, isResolved, type Entry, type Filter, type Kind } from './types'

const ICONS: Record<Kind, typeof Gear> = {
  exec: Gear, exec_done: CheckCircle, task_new: PlusSquare, task_done: CheckSquare, comm: ChatCircleDots, request: Question, confirm: Warning,
}
const NEAR_BOTTOM = 24

interface Props {
  name: string
  roleLine: string
  live: boolean
  entries: Entry[]
  hoverTask: string | null
  onHoverTask: (id: string | null) => void
  onTogglePin: (id: string) => void
  selectedConfirm: string | null
  onOpenConfirm: (id: string) => void
  roadmapOpen: boolean
  tasksOpen: boolean
  onToggleRoadmap: () => void
  onToggleTasks: () => void
  onClose: () => void
}

export function JournalCardV4(p: Props) {
  const { entries } = p
  const [filter, setFilter] = useState<Filter>('all')
  const shown = useMemo(() => entries.filter((e) => matchesFilter(e, filter)), [entries, filter])
  const pinnedList = useMemo(() => entries.filter((e) => e.pinned).sort((a, b) => (`${b.date} ${b.time}` > `${a.date} ${a.time}` ? 1 : -1)), [entries])
  const openCount = useMemo(() => entries.reduce((n, e) => n + (e.kind === 'confirm' ? openItems(e) : 0), 0), [entries])
  const scroller = useRef<HTMLDivElement>(null)
  const atBottom = useRef(true)
  const seen = useRef(shown.length)
  const [below, setBelow] = useState(0)

  const toBottom = useCallback((smooth = false) => {
    const el = scroller.current
    if (!el) return
    el.scrollTo({ top: el.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
    atBottom.current = true
    seen.current = shown.length
    setBelow(0)
  }, [shown.length])

  useLayoutEffect(() => { toBottom() }, [filter]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (atBottom.current) toBottom(true)
    else setBelow(Math.max(0, shown.length - seen.current))
  }, [shown.length]) // eslint-disable-line react-hooks/exhaustive-deps

  const onScroll = () => {
    const el = scroller.current
    if (!el) return
    const at = el.scrollHeight - el.scrollTop - el.clientHeight <= NEAR_BOTTOM
    atBottom.current = at
    if (at) { seen.current = shown.length; setBelow(0) }
  }

  const rows: React.ReactNode[] = []
  let day = ''
  shown.forEach((e) => {
    if (e.date !== day) {
      day = e.date
      rows.push(<DaySep key={`d-${day}`} date={day} />)
    }
    rows.push(<Row key={e.id} e={e} {...p} />)
  })

  const tog = (on: boolean) => `p-1 rounded border ${on ? 'border-accent bg-accent-muted text-text-primary' : 'border-border-default text-text-secondary hover:text-text-primary hover:bg-surface-hover'}`

  return (
    <div data-testid="journal-card" className="h-full flex flex-col text-xs text-text-primary bg-surface-elevated">
      <div className="px-3 pt-2.5 pb-2 border-b border-border-subtle flex-shrink-0">
        <div className="flex items-center gap-2">
          <span title={p.live ? '連線中，事件即時更新' : '離線'} className={`w-2 h-2 rounded-full flex-shrink-0 ${p.live ? 'bg-status-success animate-pulse' : 'bg-text-muted'}`} />
          <span className="text-sm font-medium truncate flex-1">{p.name}</span>
          <button type="button" data-testid="toggle-roadmap" aria-pressed={p.roadmapOpen} onClick={p.onToggleRoadmap} title="路線圖" className={tog(p.roadmapOpen)}><MapTrifold size={13} /></button>
          <button type="button" data-testid="toggle-tasks" aria-pressed={p.tasksOpen} onClick={p.onToggleTasks} title="任務" className={tog(p.tasksOpen)}><SquaresFour size={13} /></button>
          <button type="button" onClick={p.onClose} aria-label="關閉日誌" className="p-1 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover"><X size={12} /></button>
        </div>
        <div className="mt-0.5 pl-4 text-[11px] text-text-muted truncate">{p.roleLine}</div>
        <div className="mt-2 flex flex-wrap gap-1" role="tablist">
          {FILTERS.map((f) => {
            const on = filter === f.id
            return (
              <button key={f.id} type="button" role="tab" aria-selected={on} onClick={() => setFilter(on && f.id !== 'all' ? 'all' : f.id)}
                className={`px-2 py-0.5 rounded-full border text-[11px] ${on ? 'border-accent text-text-primary bg-accent-muted' : 'border-border-default text-text-secondary hover:text-text-primary hover:bg-surface-hover'}`}>
                {f.label}
                {f.id === 'confirm' && openCount > 0 && <span className="ml-1 px-1 rounded-full text-[10px] font-medium" style={{ background: KIND_COLOR.confirm, color: '#3a2f00' }}>{openCount}</span>}
              </button>
            )
          })}
        </div>
      </div>

      {pinnedList.length > 0 && (
        <div data-testid="pinned-band" className="flex-shrink-0 max-h-[34%] overflow-y-auto px-3 py-1.5 border-b border-border-default bg-surface-secondary/60">
          <div className="flex items-center gap-1 text-[10.5px] text-text-muted mb-0.5"><PushPin size={10} weight="fill" />釘選（{pinnedList.length}）</div>
          {pinnedList.map((e) => <Row key={e.id} e={e} {...p} compact />)}
        </div>
      )}

      <div className="relative flex-1 min-h-0">
        <div ref={scroller} onScroll={onScroll} data-testid="journal-stream" className="absolute inset-0 overflow-y-auto px-3 py-2">
          {rows.length === 0 ? <div className="text-text-muted py-6 text-center">沒有符合的事件</div> : rows}
        </div>
        {below > 0 && (
          <button type="button" data-testid="journal-pill" onClick={() => toBottom(true)}
            className="absolute left-1/2 -translate-x-1/2 bottom-3 flex items-center gap-1 px-3 py-1 rounded-full bg-accent text-text-inverse shadow-lg text-[11px]">
            <ArrowDown size={11} /> ↓ {below} 則新事件
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

export function KindChip({ e }: { e: Entry }) {
  const c = KIND_COLOR[e.kind]
  const Icon = ICONS[e.kind]
  return (
    <span className="inline-flex items-center gap-1 px-1.5 rounded text-[10px] font-medium flex-shrink-0" style={{ color: c, background: `${c}22`, border: `1px solid ${c}66` }}>
      <Icon size={10} weight="bold" />{kindLabel(e)}
    </span>
  )
}

function Row({ e, compact, hoverTask, onHoverTask, onTogglePin, selectedConfirm, onOpenConfirm }: Props & { e: Entry; compact?: boolean }) {
  const isConfirm = e.kind === 'confirm'
  const resolved = isResolved(e)
  const linked = e.taskId !== undefined && e.taskId === hoverTask
  const selected = selectedConfirm === e.id
  const yellow = KIND_COLOR.confirm
  return (
    <div
      data-testid="journal-entry" data-kind={e.kind} data-linked={linked || undefined}
      onMouseEnter={() => e.taskId && onHoverTask(e.taskId)} onMouseLeave={() => e.taskId && onHoverTask(null)}
      onClick={isConfirm ? () => onOpenConfirm(e.id) : undefined}
      className={`group relative flex gap-2 py-1.5 px-1 -mx-1 rounded border-b border-border-subtle/50 last:border-b-0 ${isConfirm ? 'cursor-pointer hover:bg-surface-hover' : ''} ${linked ? 'bg-accent-muted ring-1 ring-accent' : ''} ${selected ? 'ring-1' : ''}`}
    >
      <span className="w-9 flex-shrink-0 tabular-nums text-[10.5px] text-text-muted pt-0.5">{e.time}</span>
      <div className="min-w-0 flex-1 pr-5">
        <div className="flex items-center gap-1.5 flex-wrap">
          <KindChip e={e} />
          <span className="text-[10.5px] text-text-secondary break-words min-w-0">{e.actor}</span>
          {isConfirm && resolved && <span className="text-[10px] px-1 rounded bg-surface-hover text-text-muted">已答完</span>}
        </div>
        <div className={`mt-0.5 leading-[17px] break-words ${compact ? 'line-clamp-2' : ''} ${resolved ? 'text-text-secondary' : 'text-text-primary'}`}>{e.text}</div>
        {isConfirm && e.items && compact && (
          <div className="mt-0.5 text-[11px]" style={{ color: KIND_COLOR.confirm }}>已答 {e.items.filter((i) => i.done).length} / {e.items.length} 項</div>
        )}
        {isConfirm && e.items && !compact && (
          <ul className="mt-1 space-y-0.5">
            {e.items.map((it) => (
              <li key={it.t} className="flex items-start gap-1.5 leading-[16px]">
                {it.done ? <CheckedBox size={13} weight="fill" className="flex-shrink-0 mt-px text-status-success" /> : <Square size={13} className="flex-shrink-0 mt-px" style={{ color: yellow }} />}
                <span className={it.done ? 'text-text-muted' : 'text-text-primary'}>{it.t}</span>
              </li>
            ))}
          </ul>
        )}
        {!compact && e.refs.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-1">
            {e.refs.map((r) => <span key={r} className="px-1 rounded bg-surface-hover text-[10px] text-accent font-mono underline decoration-dotted underline-offset-2 cursor-pointer">{r}</span>)}
          </div>
        )}
      </div>
      <button
        type="button" data-testid="pin-toggle" title={e.pinned ? '取消釘選' : '釘選'}
        onClick={(ev) => { ev.stopPropagation(); onTogglePin(e.id) }}
        className={`absolute top-1 right-0.5 p-0.5 rounded hover:bg-surface-hover ${e.pinned ? 'text-text-primary' : 'text-text-muted opacity-0 group-hover:opacity-100'}`}
      >
        <PushPin size={12} weight={e.pinned ? 'fill' : 'regular'} />
      </button>
    </div>
  )
}
