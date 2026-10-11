// spa/src/lib/nex/send-error-text.ts — the wording of a worker's failed send, the room's (ExecutionView's own `errorText`): a code the
// daemon is known to answer with has its own line, an attachment code its upload line, anything else the generic line with the daemon's message.
import { isAttachmentError, uploadErrorKey } from './worker-upload'

const KNOWN_ERROR_KEYS = new Set(['invalid_text', 'execution_archived', 'execution_terminal', 'turn_failed_to_launch', 'turn_stalled', 'interrupt_unconfirmed'])

export function sendErrorText(t: (key: string, params?: Record<string, string | number>) => string, err: { code: string; message: string }): string {
  if (KNOWN_ERROR_KEYS.has(err.code)) return t(`execution.error.${err.code}`)
  if (isAttachmentError(err.code)) return t(uploadErrorKey(err.code))
  return t('execution.error.generic', { message: err.message })
}
