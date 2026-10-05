// spa/src/components/room/prelude/Placeholders.tsx — what the prelude draws
// for content the daemon left out or cut (spec §4.3, D6): an image or
// document as its kind and size (never data), and a one-line hint after
// any block or note that was cut at max_block_bytes. The predicates live in
// placeholder-utils.ts.
import type { ContentBlock } from '../../../lib/nex/message-types'
import { formatBytes } from '../../../lib/nex/format'
import { useI18nStore } from '../../../stores/useI18nStore'

export function OmittedMedia({ block }: { block: ContentBlock }) {
  const t = useI18nStore((s) => s.t)
  const type = (block.source?.media_type ?? '').replace(/^[a-z]+\//, '') || '?'
  const size = formatBytes(block.source?.bytes ?? 0)
  return (
    <div data-testid="prelude-media" className="text-xs text-text-muted font-mono">
      {t(block.type === 'document' ? 'worker.prelude.document' : 'worker.prelude.image', { type, size })}
    </div>
  )
}

export function TruncatedHint({ shown, total }: { shown: number; total: number }) {
  const t = useI18nStore((s) => s.t)
  return (
    <div data-testid="prelude-truncated" className="text-xs text-text-muted">
      {t('worker.prelude.truncated', { shown: formatBytes(shown), total: formatBytes(total) })}
    </div>
  )
}
