// spa/src/lib/statusline-test-debug.ts
// Flag-gated debug logger for the statusline pipeline self-test rail.
//
// Enable in DevTools console:
//   localStorage.setItem('pdx:debug:statusline-test', '1')
// Disable:
//   localStorage.removeItem('pdx:debug:statusline-test')
//
// When enabled, the WS dispatcher, event bus, and useMultiHostEventWs emit
// timeline logs tagged `[pdx:stl-test]` so we can see where the chain breaks
// between daemon broadcast and SPA bus subscriber invocation.

const KEY = 'pdx:debug:statusline-test'

export function isStatuslineTestDebugEnabled(): boolean {
  try {
    return typeof localStorage !== 'undefined' && localStorage.getItem(KEY) === '1'
  } catch {
    return false
  }
}

export function debugStatuslineTest(label: string, data?: unknown): void {
  if (!isStatuslineTestDebugEnabled()) return
  if (data === undefined) {
    console.debug('[pdx:stl-test]', label)
  } else {
    console.debug('[pdx:stl-test]', label, data)
  }
}
