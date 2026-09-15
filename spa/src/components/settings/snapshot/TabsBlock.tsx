import { collectLeaves } from '../../../lib/pane-tree'
import type { WorkspaceSnapshot } from '../../../lib/snapshot/types'
import { leafLabel, type TFn } from './shared'

/** The captured workspace → tab → pane tree (client-scoped: spans every host). */
export function TabsBlock({
  snap,
  busy,
  onRestoreLayout,
  t,
}: {
  snap: WorkspaceSnapshot
  busy: boolean
  onRestoreLayout: () => void
  t: TFn
}) {
  return (
    <div data-testid="snapshot-tabs-block" className="mt-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm text-text-primary">{t('settings.snapshot.tabs.title')}</h3>
        <button
          type="button"
          data-testid="snapshot-restore-tab-btn"
          onClick={onRestoreLayout}
          disabled={busy}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {t('settings.snapshot.tabs.restoreLayout')}
        </button>
      </div>

      <ul className="text-xs text-text-secondary flex flex-col gap-2">
        {snap.workspaces.map((ws) => (
          <li key={ws.id} data-testid={`snapshot-ws-${ws.id}`}>
            <span className="text-text-primary">{ws.name}</span>
            <ul className="ml-4 mt-1 flex flex-col gap-1">
              {ws.tabs.map((tabId) => {
                const tab = snap.tabs[tabId]
                if (!tab) return null
                const leaves = collectLeaves(tab.layout)
                return (
                  <li key={tabId} data-testid={`snapshot-tab-${tabId}`}>
                    <ul className="flex flex-col gap-0.5">
                      {leaves.map((pane) => (
                        <li key={pane.id} className="font-mono text-text-muted" data-testid="snapshot-pane-leaf">
                          {leafLabel(pane.content)}
                        </li>
                      ))}
                    </ul>
                  </li>
                )
              })}
            </ul>
          </li>
        ))}
      </ul>
    </div>
  )
}
