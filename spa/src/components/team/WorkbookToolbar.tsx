// spa/src/components/team/WorkbookToolbar.tsx — the workbook view's header tools (WA-2b-2, v2 only): the 紀錄｜待辦 switch and 重整.
// 重整 asks the store (`requestRefresh`); whether one is running is DERIVED from the conversation (`selectRefreshPending`), so
// nothing here can get stuck. The one local flag only bridges the request and the 409 `refresh_pending` refetch.
import { useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { selectRefreshPending, useWorkbookStore, type ConvState } from '../../stores/useWorkbookStore'
import type { WorkbookTab } from '../../lib/workbook/view-memory'

interface Props {
  v2: boolean
  tab: WorkbookTab
  onTab: (tab: WorkbookTab) => void
  hostId: string
  sessionId: string
  convKey: string | null
  conv: ConvState | undefined
}

function TabButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onMouseDown={(e) => e.preventDefault()}
      onClick={onClick}
      className={`px-1.5 py-0.5 rounded text-[11px] cursor-pointer ${active ? 'bg-surface-hover text-text-primary' : 'text-text-secondary hover:text-text-primary'}`}
    >
      {children}
    </button>
  )
}

function RefreshButton({ hostId, sessionId, convKey, conv }: Pick<Props, 'hostId' | 'sessionId' | 'convKey' | 'conv'>) {
  const t = useI18nStore((s) => s.t)
  const [asking, setAsking] = useState(false)
  const pending = asking || selectRefreshPending(conv)
  const available = !!conv?.refreshAvailable && convKey !== null
  async function refresh() {
    if (convKey === null || pending) return
    setAsking(true)
    try {
      const r = await useWorkbookStore.getState().requestRefresh(hostId, convKey)
      // Not live (the flag was stale) or already pending (we had not seen it): the conversation is asked again and the
      // flag / the pending entry come back from the answer.
      if (r.kind === 'not_live' || r.kind === 'refresh_pending') await useWorkbookStore.getState().resnapshot(hostId, sessionId)
    } finally {
      setAsking(false)
    }
  }
  const enabled = available && !pending
  return (
    <button
      type="button"
      data-testid="workbook-refresh"
      disabled={!enabled}
      title={available ? t('team.workbook.refresh_tip') : t('team.workbook.refresh_unavailable')}
      onMouseDown={(e) => e.preventDefault()}
      onClick={() => { void refresh() }}
      className="flex items-center gap-1 px-1.5 py-0.5 rounded text-[11px] text-text-secondary enabled:hover:text-text-primary enabled:hover:bg-surface-hover enabled:cursor-pointer disabled:opacity-50"
    >
      <ArrowsClockwise size={12} className={pending ? 'animate-spin' : undefined} />
      <span>{pending ? t('team.workbook.refreshing') : t('team.workbook.refresh')}</span>
    </button>
  )
}

export function WorkbookToolbar({ v2, tab, onTab, hostId, sessionId, convKey, conv }: Props) {
  const t = useI18nStore((s) => s.t)
  if (!v2) return null
  const openTodos = conv?.todos.open.length ?? 0
  return (
    <span className="flex items-center gap-1.5 flex-shrink-0">
      <RefreshButton hostId={hostId} sessionId={sessionId} convKey={convKey} conv={conv} />
      <span data-testid="workbook-tabs" className="flex items-center gap-0.5">
        <TabButton active={tab === 'log'} onClick={() => onTab('log')}>{t('team.workbook.tab_log')}</TabButton>
        <TabButton active={tab === 'todos'} onClick={() => onTab('todos')}>
          {t('team.workbook.tab_todos')}{openTodos > 0 && <span className="ml-1 text-text-muted">{openTodos}</span>}
        </TabButton>
      </span>
    </span>
  )
}
