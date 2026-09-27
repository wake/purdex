// spa/src/lib/find-shortcut.ts — Mod+F, the find shortcut (R3 plan T3.3):
// Cmd+F on a Mac, Ctrl+F elsewhere. Only that modifier: Ctrl+F on a Mac is
// the terminal's / readline's forward-char, and Mod+Shift+F / Mod+Alt+F are
// left to whoever claims them. The key is F by `key` or, on a layout whose F
// key types another letter (Cyrillic, Hebrew…), by its physical `code`.

/** Whether this runs on a Mac (or iPad / iPhone, whose keyboards use Cmd). */
export const IS_MAC = typeof navigator !== 'undefined'
  && /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent)

export function isFindShortcut(
  e: Pick<KeyboardEvent, 'key' | 'metaKey' | 'ctrlKey' | 'shiftKey' | 'altKey'> & { code?: string },
  mac: boolean = IS_MAC,
): boolean {
  if ((e.key.toLowerCase() !== 'f' && e.code !== 'KeyF') || e.shiftKey || e.altKey) return false
  return mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey
}
