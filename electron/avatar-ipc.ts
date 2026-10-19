// electron/avatar-ipc.ts — the `avatar:fetch` channel of the persona page (name pool v2 plan P2). The rules are in
// avatar-fetch.ts; this only wires them to Electron's network stack and to the renderer.
import { ipcMain, net } from 'electron'
import { handleAvatarFetch } from './avatar-fetch'

export function registerAvatarIpc(): void {
  ipcMain.handle('avatar:fetch', (_event, url: unknown) =>
    handleAvatarFetch(url, { fetch: (u, init) => net.fetch(u, init) }),
  )
}
