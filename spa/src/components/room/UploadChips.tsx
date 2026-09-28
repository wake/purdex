// spa/src/components/room/UploadChips.tsx — the files attached to the next
// worker message (worker-pane theme spec §9.1): one removable chip per file,
// uploading / done / failed, with a thumbnail for an image. Removing a chip
// only drops it from the message; nothing is deleted on disk.
import { CircleNotch, File as FileIcon, WarningCircle, X } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { uploadErrorKey, type Chip } from '../../lib/nex/worker-upload'

interface Props {
  chips: readonly Chip[]
  onRemove: (key: string) => void
}

export default function UploadChips({ chips, onRemove }: Props) {
  const t = useI18nStore((s) => s.t)
  if (chips.length === 0) return null
  return (
    <div data-testid="upload-chips" className="flex flex-wrap gap-1.5 px-3 pt-2">
      {chips.map((c) => {
        const failed = c.status === 'failed'
        const reason = failed ? t(uploadErrorKey(c.error)) : undefined
        return (
          <div key={c.key} data-testid="upload-chip" data-status={c.status} title={c.path ?? reason ?? c.name}
            className={`flex items-center gap-1.5 max-w-[16rem] rounded border px-1.5 py-0.5 text-xs ${
              failed ? 'border-status-error/60 text-status-error' : 'border-border-subtle text-text-secondary'
            }`}>
            {c.previewUrl
              ? <img src={c.previewUrl} alt="" draggable={false} className="h-5 w-5 shrink-0 rounded-sm object-cover" />
              : failed ? <WarningCircle size={14} className="shrink-0" /> : <FileIcon size={14} className="shrink-0" />}
            <span className="truncate">{c.name}</span>
            {c.status === 'uploading' && (
              <CircleNotch size={12} className="shrink-0 animate-spin text-text-muted" aria-label={t('worker.upload.uploading')} />
            )}
            {c.note && !failed && <span data-testid="upload-chip-note" className="shrink-0 text-text-muted">· {t(`worker.upload.${c.note}`)}</span>}
            {failed && <span className="shrink-0">· {reason}</span>}
            <button type="button" onClick={() => onRemove(c.key)} aria-label={t('worker.upload.remove', { name: c.name })}
              className="shrink-0 rounded p-0.5 text-text-muted hover:text-text-primary hover:bg-surface-hover cursor-pointer">
              <X size={10} />
            </button>
          </div>
        )
      })}
    </div>
  )
}
