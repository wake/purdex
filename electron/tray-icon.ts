// Locating the tray template image. Kept free of `electron` imports so it can
// be unit-tested under vitest.
//
// The icon lives in spa/public/icons/ so Vite copies it to out/renderer/icons/,
// which is what electron-builder packages and what dev-update ships (both only
// carry out/**). The source-tree path is the fallback for running from source
// before out/renderer has been built. Electron's createFromPath picks up the
// sibling trayTemplate@2x.png automatically for retina.
import { join } from 'node:path'

export function resolveTrayIconPath(mainDir: string, exists: (p: string) => boolean): string | null {
  const candidates = [
    join(mainDir, '../renderer/icons/trayTemplate.png'),
    join(mainDir, '../../spa/public/icons/trayTemplate.png'),
  ]
  for (const p of candidates) {
    if (exists(p)) return p
  }
  return null
}
