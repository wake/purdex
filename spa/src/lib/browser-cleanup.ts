import type { Tab } from '../types/tab'
import { getPrimaryPane } from './pane-tree'

export function destroyBrowserViewIfNeeded(tab: Tab): void {
  const primary = getPrimaryPane(tab.layout)
  if (primary.content.kind === 'browser') {
    window.electronAPI?.destroyBrowserView(primary.id)
  }
}
