// spa/src/lib/conversations/stage-mark.ts — the mark a pending message wears for the stage the daemon reported (message-stages plan §1.4).
import type { StageMark } from './send-queue'

export interface Mark { key: string; params?: Record<string, string> }

/** `read` has no mark: the message is a normal one. A `failed` stage names its reason, in words when the terminal-side refusals have them. */
export function stageMarkOf(s: StageMark): Mark | null {
  switch (s.stage) {
    case 'received':
    case 'filled':
    case 'entered':
      return { key: 'deck.stage.host' }
    case 'queued': return { key: 'deck.stage.queued' }
    case 'failed': return { key: 'deck.stage.failed', params: { reason: s.reason ?? '-' } }
    case 'unknown': return { key: 'deck.stage.unknown' }
    default: return null // read / recalled / a stage a later daemon adds
  }
}
