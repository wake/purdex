import { useState } from 'react'
import { ArrowDown, ArrowUp, Check, PencilSimple, Plus, Trash, X } from '@phosphor-icons/react'
import { DEFAULT_COMMAND_ICON } from '../../lib/command-icons'
import { type HostCommand } from '../../lib/host-config-api'
import { MAX_CONFIG_ITEMS, newConfigId } from '../../lib/host-config-validate'
import { useI18nStore } from '../../stores/useI18nStore'
import { ResumeTemplateSettings } from '../settings/ResumeTemplateSettings'
import { CommandEditDialog } from './CommandEditDialog'
import { CommandIconView } from './CommandIconView'
import { HostConfigNotice } from './HostConfigNotice'
import { useHostConfigCollection } from './useHostConfigCollection'

type Tab = 'normal' | 'resume'

export function CommandsSection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const [tab, setTab] = useState<Tab>('normal')
  // Everything this section shares with Projects — the gate, the limit, the
  // dialog's lifecycle, the delete confirmation and the queued, id-addressed
  // saves. What is left here is what a COMMAND is: its fields, its icon, its rows.
  const {
    items: commands, editable, notice, atLimit, pending, saveError,
    editing, isNew, openEditor, closeEditor, submit,
    deleting, askDelete, cancelDelete, confirmDelete, move,
  } = useHostConfigCollection<HostCommand>(hostId, 'commands')
  const locked = !editable

  const tabBtn = (id: Tab, key: string) => (
    <button type="button" data-testid={`commands-tab-${id}`} aria-pressed={tab === id} onClick={() => setTab(id)}
      className={`px-3 py-1 rounded text-xs cursor-pointer ${tab === id ? 'bg-accent/20 text-accent font-semibold' : 'text-text-secondary hover:text-text-primary'}`}>
      {t(key)}
    </button>
  )
  const iconBtn = 'p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed'

  return (
    <div className="max-w-3xl">
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-lg font-semibold">{t('hosts.commands')}</h2>
        {tab === 'normal' && (
          <button type="button" data-testid="command-add" disabled={locked || pending || atLimit}
            onClick={() => openEditor({ id: newConfigId(), name: '', command: '', icon: DEFAULT_COMMAND_ICON })}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">
            <Plus size={14} />{t('commands.add')}
          </button>
        )}
      </div>
      <div className="flex gap-1 mb-4">
        {tabBtn('normal', 'commands.tab.normal')}
        {tabBtn('resume', 'commands.tab.resume')}
      </div>

      <HostConfigNotice notice={notice} />

      {tab === 'resume' ? (
        <ResumeTemplateSettings hostId={hostId} busy={!editable} />
      ) : (
        <>
          {atLimit && (
            <p data-testid="commands-limit" className="mb-3 text-xs text-text-muted">{t('host_config.limit', { max: MAX_CONFIG_ITEMS })}</p>
          )}
          {saveError?.target === 'list' && (
            <p data-testid="commands-save-error" className="mb-3 text-xs text-status-warning whitespace-pre-wrap">{saveError.text}</p>
          )}
          {editing && (
            <CommandEditDialog key={editing.id} hostId={hostId} initial={editing} isNew={isNew}
              busy={locked || pending} error={saveError?.target === 'dialog' ? saveError.text : null}
              onSave={submit} onCancel={closeEditor} />
          )}
          {commands.length === 0 ? (
            <p className="text-sm text-text-muted">{t('commands.empty')}</p>
          ) : (
            <ul className="border border-border-subtle rounded-lg divide-y divide-border-subtle">
              {commands.map((command, index) => (
                <li key={command.id} data-testid={`command-row-${command.id}`} className="flex items-center gap-3 px-3 py-2">
                  <CommandIconView icon={command.icon} size={16} className="text-text-secondary" />
                  <span className="w-40 shrink-0 truncate text-sm text-text-primary" title={command.name}>{command.name}</span>
                  <span className="min-w-0 flex-1 truncate font-mono text-xs text-text-muted" title={command.command}>{command.command}</span>
                  <div className="flex items-center gap-1">
                    <button type="button" data-testid={`command-up-${command.id}`} title={t('host_config.move_up')}
                      disabled={locked || index === 0} onClick={() => void move(command.id, -1)}
                      className={iconBtn}><ArrowUp size={14} /></button>
                    <button type="button" data-testid={`command-down-${command.id}`} title={t('host_config.move_down')}
                      disabled={locked || index === commands.length - 1} onClick={() => void move(command.id, 1)}
                      className={iconBtn}><ArrowDown size={14} /></button>
                    <button type="button" data-testid={`command-edit-${command.id}`} title={t('common.edit')}
                      disabled={locked} onClick={() => openEditor(command)} className={iconBtn}><PencilSimple size={14} /></button>
                    {deleting === command.id ? (
                      <span className="flex items-center gap-1">
                        <button type="button" data-testid={`command-delete-confirm-${command.id}`} disabled={locked}
                          onClick={() => confirmDelete(command.id)}
                          className="p-1 text-red-400 cursor-pointer disabled:opacity-40"><Check size={14} /></button>
                        <button type="button" onClick={cancelDelete} className="p-1 text-text-muted cursor-pointer"><X size={14} /></button>
                      </span>
                    ) : (
                      <button type="button" data-testid={`command-delete-${command.id}`} title={t('common.delete')}
                        disabled={locked} onClick={() => askDelete(command.id)}
                        className={`${iconBtn} hover:text-red-400`}><Trash size={14} /></button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </div>
  )
}
