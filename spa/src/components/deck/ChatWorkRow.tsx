// spa/src/components/deck/ChatWorkRow.tsx — one chain of work as one row (U3 spec §5, plan D9) and the file chip. A finished
// run reads 「處理了 2 分 13 秒 · 3 個指令、2 個編輯 · 1 失敗 ›」; a run with a running step is the live progress message,
// 「正在：<類別> <step>」 with a clock that ticks, and it is the SAME element when it finishes (it updates in place). A click
// hands the chain to the right panel.
import { useEffect, useState } from 'react'
import { CaretRight, FileText } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { formatSpan, type TurnRun } from '../../lib/conversations/turn-row'

function LiveClock({ since }: { since: number }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])
  return <span data-testid="chat-work-clock" className="tabular-nums text-text-muted">{formatSpan(Math.max(0, now - since))}</span>
}

/** The finished text split at its 「 · 」 so the failures read red and the denials grey (the text itself is iOS's). */
function Parts({ text }: { text: string }) {
  const parts = text.split(' · ')
  return (
    <>
      {parts.map((p, i) => (
        <span key={i}>
          {i > 0 && ' · '}
          <span className={p.endsWith(' 失敗') ? 'text-status-error' : p.endsWith(' 已拒絕') || p.endsWith(' 已中斷') ? 'text-text-muted' : undefined}>{p}</span>
        </span>
      ))}
    </>
  )
}

export function ChatWorkRow({ run, onOpen }: { run: TurnRun; onOpen: () => void }) {
  const t = useI18nStore((s) => s.t)
  return (
    <button type="button" data-testid="chat-work" data-running={run.running} onClick={onOpen}
      className="flex w-full cursor-pointer items-center gap-2 rounded-lg border border-border-subtle px-3 py-1.5 text-left text-xs text-text-secondary hover:bg-surface-secondary">
      {run.running ? (
        <>
          <span className="inline-block h-2 w-2 shrink-0 animate-pulse rounded-full bg-accent" />
          <span data-testid="chat-work-text" className="min-w-0 flex-1 truncate">{t('chat.progress', { latest: run.latest ?? '' })}</span>
          <LiveClock since={run.startedAt} />
        </>
      ) : (
        <span data-testid="chat-work-text" className="min-w-0 flex-1 truncate"><Parts text={run.text ?? ''} /></span>
      )}
      <CaretRight size={12} className="shrink-0 text-text-muted" />
    </button>
  )
}

export function FileChip({ files, added, removed, onOpen }: { files: number; added: number; removed: number; onOpen: () => void }) {
  const t = useI18nStore((s) => s.t)
  return (
    <button type="button" data-testid="chat-files" onClick={onOpen}
      className="inline-flex cursor-pointer items-center gap-1.5 rounded-full border border-border-subtle px-2.5 py-0.5 text-xs text-text-secondary hover:bg-surface-secondary">
      <FileText size={12} />
      {t('chat.files', { n: files, a: added, r: removed })}
    </button>
  )
}
