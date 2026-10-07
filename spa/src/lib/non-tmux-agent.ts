/**
 * Agent code of a Claude Code session that is NOT inside tmux (an sdk-cli
 * session, a Nexen worker's `claude -p`): `cc-<CC session id>`, derived by the
 * daemon (internal/module/agent/nontmux.go NonTmuxAgentCode). A tmux session
 * code is a 6-char base36 token (never contains '-') and a worker is `exec-…`,
 * so the prefix cannot collide with either. Like every agent code it is only
 * unique per host — always keyed with the host id (compositeKey).
 */
export const NON_TMUX_PREFIX = 'cc-'

export function isNonTmuxAgentCode(code: string): boolean {
  return code.startsWith(NON_TMUX_PREFIX)
}
