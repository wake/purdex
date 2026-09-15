import { useState } from 'react'
import { ArrowDown, ArrowUp, Check, PencilSimple, Plus, Trash, X } from '@phosphor-icons/react'
import { HostConfigConflictError, type HostProject } from '../../lib/host-config-api'
import { MAX_CONFIG_ITEMS, moveItem, newConfigId } from '../../lib/host-config-validate'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { HostConfigNotice, useHostConfigGate } from './HostConfigNotice'
import { PathStatusIcon, ProjectEditDialog } from './ProjectEditDialog'
import { usePathCheck } from './usePathCheck'

function RowPathStatus({ hostId, project }: { hostId: string; project: HostProject }) {
  const status = usePathCheck(hostId, project.path, 0)
  return <PathStatusIcon status={status} testId={`project-path-status-${project.id}`} />
}

/** Where a save failure is shown: inside the open dialog, or above the list. */
type ErrorTarget = 'dialog' | 'list'

export function ProjectsSection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const { entry, editable, notice } = useHostConfigGate(hostId)
  const projects = entry.projects
  const [editing, setEditing] = useState<HostProject | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<{ target: ErrorTarget; text: string } | null>(null)
  const locked = !editable || saving
  const atLimit = projects.length >= MAX_CONFIG_ITEMS

  const persist = async (next: HostProject[], target: ErrorTarget) => {
    setSaving(true)
    setSaveError(null)
    try {
      await useHostConfigStore.getState().saveProjects(hostId, next)
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

  const handleSave = async (project: HostProject) => {
    const exists = projects.some((p) => p.id === project.id)
    if (!exists && atLimit) {
      setSaveError({ target: 'dialog', text: t('host_config.limit', { max: MAX_CONFIG_ITEMS }) })
      return
    }
    const next = exists ? projects.map((p) => (p.id === project.id ? project : p)) : [...projects, project]
    if (await persist(next, 'dialog')) setEditing(null)
  }

  const openEditor = (project: HostProject) => {
    setSaveError(null)
    setEditing(project)
  }

  const closeEditor = () => {
    setEditing(null)
    setSaveError((e) => (e?.target === 'dialog' ? null : e))
  }

  const iconBtn = 'p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed'

  return (
    <div className="max-w-3xl">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold">{t('hosts.projects')}</h2>
        <button type="button" data-testid="project-add" disabled={locked || atLimit}
          onClick={() => openEditor({ id: newConfigId(), name: '', slug: '', path: '' })}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">
          <Plus size={14} />{t('projects.add')}
        </button>
      </div>

      <HostConfigNotice notice={notice} />
      {atLimit && (
        <p data-testid="projects-limit" className="mb-3 text-xs text-text-muted">{t('host_config.limit', { max: MAX_CONFIG_ITEMS })}</p>
      )}
      {saveError?.target === 'list' && (
        <p data-testid="projects-save-error" className="mb-3 text-xs text-status-warning whitespace-pre-wrap">{saveError.text}</p>
      )}

      {editing && (
        <ProjectEditDialog key={editing.id} hostId={hostId} initial={editing} others={projects}
          busy={locked} error={saveError?.target === 'dialog' ? saveError.text : null}
          onSave={(p) => { void handleSave(p) }} onCancel={closeEditor} />
      )}

      {projects.length === 0 ? (
        <p className="text-sm text-text-muted">{t('projects.empty')}</p>
      ) : (
        <div className="border border-border-subtle rounded-lg overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-surface-tertiary text-text-secondary text-xs">
                <th className="text-left px-3 py-2">{t('projects.col.name')}</th>
                <th className="text-left px-3 py-2">{t('projects.col.slug')}</th>
                <th className="text-left px-3 py-2">{t('projects.col.path')}</th>
                <th className="px-3 py-2" />
              </tr>
            </thead>
            <tbody>
              {projects.map((project, index) => (
                <tr key={project.id} data-testid={`project-row-${project.id}`} className="border-t border-border-subtle hover:bg-surface-secondary/30">
                  <td className="px-3 py-2 text-text-primary">{project.name}</td>
                  <td className="px-3 py-2 font-mono text-xs text-text-secondary">{project.slug}</td>
                  <td className="px-3 py-2 font-mono text-xs text-text-muted">
                    <span className="inline-flex items-center gap-1.5 max-w-[260px]">
                      <RowPathStatus hostId={hostId} project={project} />
                      <span className="truncate" title={project.path}>{project.path}</span>
                    </span>
                  </td>
                  <td className="px-3 py-2">
                    <div className="flex items-center justify-end gap-1">
                      <button type="button" data-testid={`project-up-${project.id}`} title={t('host_config.move_up')}
                        disabled={locked || index === 0} onClick={() => void persist(moveItem(projects, index, -1), 'list')}
                        className={iconBtn}><ArrowUp size={14} /></button>
                      <button type="button" data-testid={`project-down-${project.id}`} title={t('host_config.move_down')}
                        disabled={locked || index === projects.length - 1} onClick={() => void persist(moveItem(projects, index, 1), 'list')}
                        className={iconBtn}><ArrowDown size={14} /></button>
                      <button type="button" data-testid={`project-edit-${project.id}`} title={t('common.edit')}
                        disabled={locked} onClick={() => openEditor(project)} className={iconBtn}><PencilSimple size={14} /></button>
                      {deleting === project.id ? (
                        <span className="flex items-center gap-1">
                          <button type="button" data-testid={`project-delete-confirm-${project.id}`} disabled={locked}
                            onClick={() => { setDeleting(null); void persist(projects.filter((p) => p.id !== project.id), 'list') }}
                            className="p-1 text-red-400 cursor-pointer disabled:opacity-40"><Check size={14} /></button>
                          <button type="button" onClick={() => setDeleting(null)} className="p-1 text-text-muted cursor-pointer"><X size={14} /></button>
                        </span>
                      ) : (
                        <button type="button" data-testid={`project-delete-${project.id}`} title={t('common.delete')}
                          disabled={locked} onClick={() => setDeleting(project.id)}
                          className={`${iconBtn} hover:text-red-400`}><Trash size={14} /></button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
