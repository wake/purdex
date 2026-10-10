// spa/src/components/team/WorkbookToolbar.tsx — the workbook view's header tools (WA-2b-2): the 紀錄｜待辦 switch (v2 only).
import { useI18nStore } from '../../stores/useI18nStore'
import type { WorkbookTab } from '../../lib/workbook/view-memory'

interface Props {
  v2: boolean
  tab: WorkbookTab
  onTab: (tab: WorkbookTab) => void
  openTodos: number
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

export function WorkbookToolbar({ v2, tab, onTab, openTodos }: Props) {
  const t = useI18nStore((s) => s.t)
  if (!v2) return null
  return (
    <span data-testid="workbook-tabs" className="flex items-center gap-0.5 flex-shrink-0">
      <TabButton active={tab === 'log'} onClick={() => onTab('log')}>{t('team.workbook.tab_log')}</TabButton>
      <TabButton active={tab === 'todos'} onClick={() => onTab('todos')}>
        {t('team.workbook.tab_todos')}{openTodos > 0 && <span className="ml-1 text-text-muted">{openTodos}</span>}
      </TabButton>
    </span>
  )
}
