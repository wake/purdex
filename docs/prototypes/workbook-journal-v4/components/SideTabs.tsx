// Prototype of the v4 roadmap tab and tasks tab, plus the 待確認 detail panel.
import { X, Square, CheckSquare } from '@phosphor-icons/react'
import { KIND_COLOR, STAGE_COLOR, TASK_STATE, isResolved, type Entry, type Feature, type Task } from './types'
import { KindChip } from './JournalCardV4'

const HEAD = 'px-3 pt-2.5 pb-2 border-b border-border-subtle text-sm font-medium flex-shrink-0'

export function StatusDot({ color, label, strike }: { color: string; label: string; strike?: boolean }) {
  return (
    <span className={`inline-flex items-center gap-1 whitespace-nowrap ${strike ? 'line-through text-text-muted' : ''}`} style={strike ? undefined : { color }}>
      <span className="w-1.5 h-1.5 rounded-full" style={{ background: color }} />{label}
    </span>
  )
}

export function RoadmapTab({ features }: { features: Feature[] }) {
  return (
    <div data-testid="roadmap-tab" className="h-full flex flex-col text-xs bg-surface-elevated">
      <div className={HEAD}>路線圖</div>
      <div className="flex-1 min-h-0 overflow-y-auto px-3 py-2 space-y-3">
        {features.map((f) => (
          <section key={f.name}>
            <h3 className="text-[11.5px] font-medium mb-1">{f.name}</h3>
            <table className="w-full border-collapse text-[11px]">
              <thead>
                <tr className="text-text-muted text-left">
                  <th className="font-normal py-0.5 pr-2 border-b border-border-subtle">階段</th>
                  <th className="font-normal py-0.5 pr-2 border-b border-border-subtle">說明</th>
                  <th className="font-normal py-0.5 border-b border-border-subtle">狀態</th>
                </tr>
              </thead>
              <tbody>
                {f.rows.map((r) => (
                  <tr key={r.stage} className="align-top">
                    <td className="py-1 pr-2 font-mono text-[10.5px] whitespace-nowrap border-b border-border-subtle/50">{r.stage}</td>
                    <td className="py-1 pr-2 text-text-secondary border-b border-border-subtle/50">{r.desc}</td>
                    <td className="py-1 border-b border-border-subtle/50"><StatusDot color={STAGE_COLOR[r.status]} label={r.status} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        ))}
      </div>
    </div>
  )
}

export function TasksTab({ tasks, solo, hoverTask, onHoverTask }: { tasks: Task[]; solo: boolean; hoverTask: string | null; onHoverTask: (id: string | null) => void }) {
  return (
    <div data-testid="tasks-tab" className="h-full flex flex-col text-xs bg-surface-elevated">
      <div className={HEAD}>任務<span className="ml-2 text-[10.5px] font-normal text-text-muted">{solo ? '待辦（Haiku 整理）' : 'team 任務'}</span></div>
      <div className="flex-1 min-h-0 overflow-y-auto px-2 py-2 space-y-1">
        {tasks.map((t) => {
          const st = TASK_STATE[t.state]
          const dropped = t.state === 'dropped'
          const on = hoverTask === t.id
          return (
            <div
              key={t.id} data-testid="task-item" data-linked={on || undefined}
              onMouseEnter={() => onHoverTask(t.id)} onMouseLeave={() => onHoverTask(null)}
              className={`rounded border px-2 py-1.5 ${on ? 'border-accent bg-accent-muted' : 'border-border-subtle hover:bg-surface-hover'} ${t.state === 'done' ? 'opacity-80' : ''}`}
            >
              <div className="flex items-center gap-1.5">
                <span className="font-mono text-[10.5px] text-text-muted">{solo ? `#${t.no}` : t.id}</span>
                <span className="ml-auto text-[10.5px]"><StatusDot color={st.color} label={st.label} strike={dropped} /></span>
              </div>
              <div className={`mt-0.5 leading-[17px] ${dropped ? 'line-through text-text-muted' : ''}`}>{t.title}</div>
              <div className="mt-0.5 flex flex-wrap items-center gap-1 text-[10.5px] text-text-secondary">
                <span>{t.owner}</span>
                {t.refs?.map((r) => <span key={r} className="px-1 rounded bg-surface-hover text-[10px] text-accent font-mono">{r}</span>)}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

export function ConfirmDetail({ e, onClose }: { e: Entry; onClose: () => void }) {
  const resolved = isResolved(e)
  return (
    <div data-testid="confirm-detail" className="h-full flex flex-col text-xs bg-surface-elevated">
      <div className="px-3 pt-2.5 pb-2 border-b border-border-subtle flex-shrink-0">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium flex-1">待確認 · 原始提問</span>
          <button type="button" onClick={onClose} aria-label="關閉" className="p-1 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover"><X size={12} /></button>
        </div>
        <div className="mt-1 flex items-center gap-1.5 flex-wrap text-[11px] text-text-muted">
          <KindChip e={e} /><span>{e.date} {e.time}</span><span>{e.actor}</span>
          <span className="px-1 rounded" style={resolved ? undefined : { background: `${KIND_COLOR.confirm}33`, color: KIND_COLOR.confirm }}>{resolved ? '已答完，不再釘選' : '等待回覆，已自動釘選'}</span>
        </div>
      </div>
      <div className="flex-1 min-h-0 overflow-y-auto px-3 py-2.5">
        <div className="text-[10.5px] text-text-muted mb-1">原文（照原樣複製）</div>
        <pre className="whitespace-pre-wrap break-words font-sans text-[12px] leading-[19px] text-text-primary rounded border border-border-subtle bg-surface-secondary/60 p-2.5">{e.detail}</pre>
        <div className="text-[10.5px] text-text-muted mt-3 mb-1">逐項狀態（唯讀，由 Haiku 勾選）</div>
        <ul className="space-y-1">
          {e.items?.map((it) => (
            <li key={it.t} className="flex items-start gap-1.5 leading-[17px]">
              {it.done ? <CheckSquare size={14} weight="fill" className="flex-shrink-0 text-status-success" /> : <Square size={14} className="flex-shrink-0" style={{ color: KIND_COLOR.confirm }} />}
              <span className={it.done ? 'text-text-muted' : ''}>{it.t}</span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}
