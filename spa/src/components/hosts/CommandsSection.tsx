import { useState } from 'react'
import { ArrowDown, ArrowUp, Check, PencilSimple, Plus, Trash, X } from '@phosphor-icons/react'
import { DEFAULT_COMMAND_ICON } from '../../lib/command-icons'
import { HostConfigConflictError, type HostCommand } from '../../lib/host-config-api'
import { MAX_CONFIG_ITEMS, moveItem, newConfigId } from '../../lib/host-config-validate'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { ResumeTemplateSettings } from '../settings/ResumeTemplateSettings'
import { CommandEditDialog } from './CommandEditDialog'
import { CommandIconView } from './CommandIconView'
import { HostConfigNotice, useHostConfigGate } from './HostConfigNotice'

type Tab = 'normal' | 'resume'

/** Where a save failure is shown: inside the open dialog, or above the list. */
type ErrorTarget = 'dialog' | 'list'

export function CommandsSection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const { entry, editable, notice } = useHostConfigGate(hostId)
  const commands = entry.commands
  const [tab, setTab] = useState<Tab>('normal')
  const [editing, setEditing] = useState<{ command: HostCommand; isNew: boolean } | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<{ target: ErrorTarget; text: string } | null>(null)
  const locked = !editable || saving
  const atLimit = commands.length >= MAX_CONFIG_ITEMS

  const persist = async (next: HostCommand[], target: ErrorTarget) => {
    setSaving(true)
    setSaveError(null)
    try {
      await useHostConfigStore.getState().saveCommands(hostId, next)
      return true
    } catch (err) {
      // A 400's message is the daemon's body text (host-config-api `failure`).
      const text = err instanceof HostConfigConflictError
        ? t('host_config.conflict')
        : t('host_config.save_failed', { reason: err instanceof Error ? err.message : String(err) })
      setSaveError({ target, text })
      return false
    } finally {
      setSaving(false)
    }
  }

  const handleSave = async (command: HostCommand) => {
    const exists = commands.some((c) => c.id === command.id)
    if (!exists && atLimit) {
      setSaveError({ target: 'dialog', text: t('host_config.limit', { max: MAX_CONFIG_ITEMS }) })
      return
    }
    const next = exists ? commands.map((c) => (c.id === command.id ? command : c)) : [...commands, command]
    if (await persist(next, 'dialog')) setEditing(null)
  }

  const openEditor = (command: HostCommand, isNew: boolean) => {
    setSaveError(null)
    setEditing({ command, isNew })
  }

  const closeEditor = () => {
    setEditing(null)
    setSaveError((e) => (e?.target === 'dialog' ? null : e))
  }

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
          <button type="button" data-testid="command-add" disabled={locked || atLimit}
            onClick={() => openEditor({ id: newConfigId(), name: '', command: '', icon: DEFAULT_COMMAND_ICON }, true)}
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
            <CommandEditDialog key={editing.command.id} hostId={hostId} initial={editing.command} isNew={editing.isNew}
              busy={locked} error={saveError?.target === 'dialog' ? saveError.text : null}
              onSave={(c) => { void handleSave(c) }} onCancel={closeEditor} />
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
                      disabled={locked || index === 0} onClick={() => void persist(moveItem(commands, index, -1), 'list')}
                      className={iconBtn}><ArrowUp size={14} /></button>
                    <button type="button" data-testid={`command-down-${command.id}`} title={t('host_config.move_down')}
                      disabled={locked || index === commands.length - 1} onClick={() => void persist(moveItem(commands, index, 1), 'list')}
                      className={iconBtn}><ArrowDown size={14} /></button>
                    <button type="button" data-testid={`command-edit-${command.id}`} title={t('common.edit')}
                      disabled={locked} onClick={() => openEditor(command, false)} className={iconBtn}><PencilSimple size={14} /></button>
                    {deleting === command.id ? (
                      <span className="flex items-center gap-1">
                        <button type="button" data-testid={`command-delete-confirm-${command.id}`} disabled={locked}
                          onClick={() => { setDeleting(null); void persist(commands.filter((c) => c.id !== command.id), 'list') }}
                          className="p-1 text-red-400 cursor-pointer disabled:opacity-40"><Check size={14} /></button>
                        <button type="button" onClick={() => setDeleting(null)} className="p-1 text-text-muted cursor-pointer"><X size={14} /></button>
                      </span>
                    ) : (
                      <button type="button" data-testid={`command-delete-${command.id}`} title={t('common.delete')}
                        disabled={locked} onClick={() => setDeleting(command.id)}
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
