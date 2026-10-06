// spa/src/lib/nex/terminal-session-ids.ts — which conversations are already running in a terminal on a host.
import { useTabStore } from '../../stores/useTabStore'
import { scanPaneTree } from '../pane-tree'

/** Session ids the SPA knows are running in a terminal on hostId: tmux-session panes, not terminated, rebuild.agent.sessionId set and !agentExited. */
export function liveTerminalSessionIds(hostId: string): Set<string> {
  const ids = new Set<string>()
  for (const tab of Object.values(useTabStore.getState().tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (c.kind !== 'tmux-session' || c.hostId !== hostId || c.terminated) return
      const sid = c.rebuild?.agent?.sessionId
      if (sid && !c.rebuild?.agentExited) ids.add(sid)
    })
  }
  return ids
}
