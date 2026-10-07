// spa/src/lib/relay-prompt-check.ts — the one client-side check of a relay prompt body (lead-team-relay spec §8.8,
// U21 (d)), mirroring internal/team/relay_prompts.go `ValidateRelayPromptBody`. Two callers, one rule set (#1913):
// the Hosts › 接力 editor disables 儲存 on it, and the host config parser drops a stored body that a toggle's PUT of
// the whole row would carry back. No imports, so the parser can use it without a cycle through a store.

/** `team.RelayPromptMaxBytes`: the longest body, in UTF-8 bytes. */
export const RELAY_PROMPT_MAX_BYTES = 16 << 10
const RELAY_TAG = '[pdx-relay'
const utf8 = new TextEncoder()

export type RelayPromptProblem = 'not_utf8' | 'too_long' | 'control_chars' | 'has_tag'

/**
 * An unpaired UTF-16 surrogate: a JS string can hold one, UTF-8 cannot. TextEncoder, and JSON decoded by Go, turn it
 * into U+FFFD, so the daemon would store a text other than the one typed.
 */
export function hasLoneSurrogate(text: string): boolean {
  for (let i = 0; i < text.length; i++) {
    const c = text.charCodeAt(i)
    if (c >= 0xd800 && c <= 0xdbff) {
      const next = text.charCodeAt(i + 1) // NaN past the end
      if (!(next >= 0xdc00 && next <= 0xdfff)) return true
      i++
    } else if (c >= 0xdc00 && c <= 0xdfff) {
      return true
    }
  }
  return false
}

export function relayPromptBytes(text: string): number {
  return utf8.encode(text).length
}

/**
 * `ValidateRelayPromptBody` on a non-blank body (a blank one, Go's TrimSpace → "", is the default and never checked):
 * an unpaired surrogate (before the bytes are counted, which would count it as U+FFFD); over 16 384 UTF-8 bytes; a
 * control character other than newline and tab (Go's `unicode.IsControl`: C0, DEL and C1, so a lone `\r` too); the
 * machine tag anywhere.
 */
export function checkRelayPromptBody(text: string): RelayPromptProblem | null {
  if (hasLoneSurrogate(text)) return 'not_utf8'
  if (relayPromptBytes(text) > RELAY_PROMPT_MAX_BYTES) return 'too_long'
  for (const ch of text) {
    const c = ch.codePointAt(0) ?? 0
    if (c !== 0x09 && c !== 0x0a && (c < 0x20 || (c >= 0x7f && c <= 0x9f))) return 'control_chars'
  }
  if (text.includes(RELAY_TAG)) return 'has_tag'
  return null
}
