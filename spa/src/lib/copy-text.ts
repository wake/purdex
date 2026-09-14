/**
 * copyText — copy a string to the clipboard, working around insecure
 * (non-HTTPS) origins where `navigator.clipboard` is undefined (e.g. the
 * Electron window loading the dev SPA server over plain http://).
 *
 * Prefers the async Clipboard API; falls back to the classic hidden
 * `<textarea>` + `document.execCommand('copy')` dance. Rejects if neither
 * mechanism is available so callers can surface the failure to the user.
 */
export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text)
    return
  }

  if (typeof document.execCommand !== 'function') {
    throw new Error('copy unsupported')
  }

  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  try {
    textarea.select()
    const ok = document.execCommand('copy')
    if (!ok) throw new Error('copy unsupported')
  } finally {
    document.body.removeChild(textarea)
  }
}
