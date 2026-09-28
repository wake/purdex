/**
 * Leading markers agents write into the tmux pane title, keyed by agentType
 * (`AGENT_NAMES` keys). Claude Code 2.1.276 sets `✳ <summary>` (U+2733 + U+0020,
 * measured 2026-09-18). Codex sets the cwd basename only (no marker) — a Codex
 * entry is added here once the user supplies a title sample (spec D9).
 */
export const AGENT_TITLE_MARKERS: Readonly<Record<string, RegExp>> = {
  cc: /^✳️?\s*/,
}

/** Strips one leading marker for the given agent; anything else passes through unchanged. */
export function stripAgentTitleMarker(title: string, agentType: string | undefined): string {
  if (!agentType) return title
  const re = AGENT_TITLE_MARKERS[agentType]
  return re ? title.replace(re, '') : title
}

/**
 * Strips a leading marker when the title happens to match any known agent's
 * marker shape, without needing an `agentType` to key the lookup by (e.g. the
 * handoff snapshot, recorded before `useAgentStore` has classified the
 * session). Each rule's regex is anchored at the start (`^`) but written so
 * it can match a zero-length prefix when the marker is absent, so a match
 * alone doesn't mean a marker was actually stripped — only a non-empty match
 * does, which is what keeps an ordinary title untouched.
 */
export function stripAnyKnownAgentTitleMarker(title: string): string {
  for (const re of Object.values(AGENT_TITLE_MARKERS)) {
    const m = re.exec(title)
    if (m && m[0].length > 0) return title.slice(m[0].length)
  }
  return title
}
