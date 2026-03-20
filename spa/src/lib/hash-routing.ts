// spa/src/lib/hash-routing.ts — v1 hash routing: #/tab/{tabId}/{viewMode?}

export function parseHash(): { tabId: string | null; viewMode: string | null } {
  const hash = window.location.hash.replace(/^#\/?/, '')
  if (!hash) return { tabId: null, viewMode: null }
  const parts = hash.split('/')
  if (parts[0] === 'tab' && parts[1]) {
    return { tabId: parts[1], viewMode: parts[2] || null }
  }
  return { tabId: null, viewMode: null }
}

export function setHash(tabId: string, viewMode?: string) {
  const newHash = viewMode ? `#/tab/${tabId}/${viewMode}` : `#/tab/${tabId}`
  if (window.location.hash !== newHash) {
    window.location.hash = newHash
  }
}
