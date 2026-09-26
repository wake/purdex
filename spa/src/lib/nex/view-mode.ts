import type { ExecutionContent, ExecutionViewMode } from '../../types/tab'

/**
 * The view an execution pane shows its worker in. Only `'chat'` is chat:
 * an absent mode, or one this build does not know (written by an older or
 * newer client), reads as room.
 */
export function viewModeOf(content: ExecutionContent): ExecutionViewMode {
  return content.mode === 'chat' ? 'chat' : 'room'
}

/** The same content showing `mode`; every other field (`from`, `host`, …) is kept. */
export function withViewMode(content: ExecutionContent, mode: ExecutionViewMode): ExecutionContent {
  return { ...content, mode }
}
