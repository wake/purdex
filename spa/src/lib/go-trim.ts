// spa/src/lib/go-trim.ts — Go's `strings.TrimSpace`, for texts the daemon trims before it stores them. Its own module
// (no store import) so the host-config parser can use it without a cycle through `quick-replies.ts`.

/**
 * Go's `unicode.IsSpace` — the Unicode White_Space property — which the
 * daemon's `strings.TrimSpace` strips before storing a quick reply. JS
 * `trim()` is not the same set: it keeps U+0085 (NEL) and strips U+FEFF (BOM).
 */
const GO_SPACE = '\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000'
const GO_TRIM = new RegExp(`^[${GO_SPACE}]+|[${GO_SPACE}]+$`, 'g')

/** `strings.TrimSpace`, so the SPA checks and sends exactly what the daemon stores. */
export function trimLikeGo(text: string): string {
  return text.replace(GO_TRIM, '')
}
