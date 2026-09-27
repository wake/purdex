// spa/src/components/hosts/QuickReplySettings.tsx — Host › Commands › Quick
// replies: the per-host list the worker pane's quick-reply dock sends (R3,
// Q2/Q3). Same queued, id-addressed saves as Projects and Commands; a quick
// reply is only a text, so it is edited inline instead of in a dialog.
import { useState } from 'react'
import { ArrowDown, ArrowUp, Check, PencilSimple, Plus, Trash, X } from '@phosphor-icons/react'
import type { QuickReply } from '../../lib/host-config-api'
import { newConfigId } from '../../lib/host-config-validate'
import { MAX_QUICK_REPLIES, validateQuickReplyText } from '../../lib/quick-replies'
import { EMPTY_HOST_CONFIG, useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostConfigCollection } from './useHostConfigCollection'

const iconBtn = 'p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed'

function QuickReplyEditor({ initial, busy, error, onSave, onCancel }: {
  initial: QuickReply
  busy: boolean
  /** Save failure text from the host, shown under the field. */
  error: string | null
  onSave: (reply: QuickReply) => void
  onCancel: () => void
}) {
  const t = useI18nStore((s) => s.t)
  const [text, setText] = useState(initial.text)
  const [invalid, setInvalid] = useState<string | null>(null)

  const save = () => {
    const trimmed = text.trim()
    const problem = validateQuickReplyText(trimmed)
    if (problem) {
      setInvalid(problem)
      return
    }
    setInvalid(null)
    onSave({ id: initial.id, text: trimmed })
  }

  const shown = invalid ? t(invalid, { max: MAX_QUICK_REPLIES }) : error
  return (
    <div className="flex flex-col gap-1 px-3 py-2">
      <div className="flex items-center gap-2">
        <input data-testid="quick-reply-input" aria-label={t('hosts.quick_replies.text_label')} autoFocus value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.nativeEvent.isComposing) { e.preventDefault(); save() }
            else if (e.key === 'Escape') onCancel()
          }}
          className="min-w-0 flex-1 rounded border border-border-default bg-surface-secondary px-2 py-1 text-sm text-text-primary" />
        <button type="button" data-testid="quick-reply-save" title={t('common.save')} disabled={busy} onClick={save}
          className={iconBtn}><Check size={14} /></button>
        <button type="button" data-testid="quick-reply-cancel" title={t('common.cancel')} onClick={onCancel}
          className={iconBtn}><X size={14} /></button>
      </div>
      {shown && <p data-testid="quick-reply-error" className="text-xs text-status-warning whitespace-pre-wrap">{shown}</p>}
    </div>
  )
}

export function QuickReplySettings({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const entry = useHostConfigStore((s) => s.byHost[hostId] ?? EMPTY_HOST_CONFIG)
  const {
    items, editable, atLimit, pending, saveError,
    editing, isNew, openEditor, closeEditor, submit,
    deleting, askDelete, cancelDelete, confirmDelete, move,
  } = useHostConfigCollection<QuickReply>(hostId, 'quick-replies')
  const locked = !editable

  // The host-wide gate only knows the host config loaded; a daemon older than
  // this collection loads fine and simply has no `quickReplies` (plan review #3).
  const known = entry.status === 'ready' || entry.status === 'unsupported'
  if (known && !entry.quickRepliesSupported) {
    return (
      <div data-testid="quick-replies">
        <p data-testid="quick-replies-unsupported" className="text-xs text-text-muted">{t('hosts.quick_replies.unsupported')}</p>
      </div>
    )
  }

  // Never loaded (idle, loading, or failed before any success): there is no
  // list to show — not even the defaults, which the host may no longer hold.
  // The section's `HostConfigNotice` above says why (loading / load failed).
  if (!entry.quickRepliesSupported) return <div data-testid="quick-replies" />

  // A failed reload keeps the last known list, so this follows the collection,
  // not the current status.
  const neverWritten = entry.revisions.quickReplies === 0
  const editor = (reply: QuickReply) => (
    <QuickReplyEditor key={reply.id} initial={reply} busy={locked || pending}
      error={saveError?.target === 'dialog' ? saveError.text : null} onSave={submit} onCancel={closeEditor} />
  )

  return (
    <div data-testid="quick-replies">
      <div className="mb-3 flex items-center justify-between gap-3">
        <p className="text-xs text-text-muted">
          {neverWritten && <span data-testid="quick-replies-defaults">{t('hosts.quick_replies.defaults_note')}</span>}
        </p>
        <button type="button" data-testid="quick-reply-add" disabled={locked || pending || atLimit || !!editing}
          onClick={() => openEditor({ id: newConfigId(), text: '' })}
          className="flex shrink-0 items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">
          <Plus size={14} />{t('hosts.quick_replies.add')}
        </button>
      </div>
      {atLimit && (
        <p data-testid="quick-replies-limit" className="mb-3 text-xs text-text-muted">{t('host_config.limit', { max: MAX_QUICK_REPLIES })}</p>
      )}
      {saveError?.target === 'list' && (
        <p data-testid="quick-replies-save-error" className="mb-3 text-xs text-status-warning whitespace-pre-wrap">{saveError.text}</p>
      )}
      {items.length === 0 && !(editing && isNew) ? (
        <p data-testid="quick-replies-empty" className="text-sm text-text-muted">{t('hosts.quick_replies.empty')}</p>
      ) : (
        <ul className="border border-border-subtle rounded-lg divide-y divide-border-subtle">
          {items.map((reply, index) => (
            <li key={reply.id} data-testid={`quick-reply-row-${reply.id}`}>
              {editing?.id === reply.id ? editor(editing) : (
                <div className="flex items-center gap-3 px-3 py-2">
                  <span className="min-w-0 flex-1 truncate text-sm text-text-primary" title={reply.text}>{reply.text}</span>
                  <div className="flex items-center gap-1">
                    <button type="button" data-testid={`quick-reply-up-${reply.id}`} title={t('host_config.move_up')}
                      disabled={locked || index === 0} onClick={() => void move(reply.id, -1)}
                      className={iconBtn}><ArrowUp size={14} /></button>
                    <button type="button" data-testid={`quick-reply-down-${reply.id}`} title={t('host_config.move_down')}
                      disabled={locked || index === items.length - 1} onClick={() => void move(reply.id, 1)}
                      className={iconBtn}><ArrowDown size={14} /></button>
                    <button type="button" data-testid={`quick-reply-edit-${reply.id}`} title={t('common.edit')}
                      disabled={locked} onClick={() => openEditor(reply)} className={iconBtn}><PencilSimple size={14} /></button>
                    {deleting === reply.id ? (
                      <span className="flex items-center gap-1">
                        <button type="button" data-testid={`quick-reply-delete-confirm-${reply.id}`} disabled={locked}
                          onClick={() => confirmDelete(reply.id)}
                          className="p-1 text-red-400 cursor-pointer disabled:opacity-40"><Check size={14} /></button>
                        <button type="button" onClick={cancelDelete} className="p-1 text-text-muted cursor-pointer"><X size={14} /></button>
                      </span>
                    ) : (
                      <button type="button" data-testid={`quick-reply-delete-${reply.id}`} title={t('common.delete')}
                        disabled={locked} onClick={() => askDelete(reply.id)}
                        className={`${iconBtn} hover:text-red-400`}><Trash size={14} /></button>
                    )}
                  </div>
                </div>
              )}
            </li>
          ))}
          {editing && isNew && <li data-testid="quick-reply-new">{editor(editing)}</li>}
        </ul>
      )}
    </div>
  )
}
