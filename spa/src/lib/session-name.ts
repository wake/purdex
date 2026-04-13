/**
 * Session name validation — mirrors daemon's nameRegex
 * (internal/module/session/handler.go:13)
 */
const SESSION_NAME_REGEX = /^[a-zA-Z0-9_-]+$/

export function isValidSessionName(name: string): boolean {
  return SESSION_NAME_REGEX.test(name)
}
