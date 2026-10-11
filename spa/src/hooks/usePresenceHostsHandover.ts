// spa/src/hooks/usePresenceHostsHandover.ts — hands the persisted host list to the Mac App's main-process presence reporter
// (PA-2 D9); mounted once per window in App.tsx. Every window sends; main keeps the highest `hostsRev`, so a window with a
// stale list cannot win and all windows converge on the persisted one. A rehydrate from another window arrives as a
// `hostsRev` change, which is what triggers the send. Nothing happens without the bridge (plain browser, older shell).
// Tokens are only passed through to the bridge: never logged.
import { useEffect } from 'react'
import { useHostStore } from '../stores/useHostStore'

export function sendHostsToPresence(): void {
  const presence = window.electronAPI?.presence
  if (!presence?.setHosts) return
  const { hosts, hostOrder, hostsRev, getDaemonBase } = useHostStore.getState()
  const ids = hostOrder.filter((id) => hosts[id])
  for (const id of Object.keys(hosts)) if (!ids.includes(id)) ids.push(id)
  const payload = { rev: hostsRev, hosts: ids.map((id) => ({ hostId: id, baseUrl: getDaemonBase(id), token: hosts[id].token ?? null })) }
  // A rejected promise or `false` (invalid payload) is main's to log; nothing here can carry a token into a message.
  void Promise.resolve(presence.setHosts(payload)).catch(() => {})
}

export function usePresenceHostsHandover(): void {
  useEffect(() => {
    if (!window.electronAPI?.presence?.setHosts) return
    let sentRev = useHostStore.getState().hostsRev
    sendHostsToPresence()
    return useHostStore.subscribe((state) => {
      if (state.hostsRev === sentRev) return
      sentRev = state.hostsRev
      sendHostsToPresence()
    })
  }, [])
}
