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
