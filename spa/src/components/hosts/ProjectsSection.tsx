import { ArrowDown, ArrowUp, Check, PencilSimple, Plus, Trash, X } from '@phosphor-icons/react'
import { type HostProject } from '../../lib/host-config-api'
import { MAX_CONFIG_ITEMS, newConfigId } from '../../lib/host-config-validate'
import { useI18nStore } from '../../stores/useI18nStore'
import { HostConfigNotice } from './HostConfigNotice'
import { PathStatusIcon, ProjectEditDialog } from './ProjectEditDialog'
import { useHostConfigCollection } from './useHostConfigCollection'
import { usePathCheck } from './usePathCheck'

function RowPathStatus({ hostId, project }: { hostId: string; project: HostProject }) {
  const status = usePathCheck(hostId, project.path, 0)
  return <PathStatusIcon status={status} testId={`project-path-status-${project.id}`} />
}

export function ProjectsSection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  // Everything this section shares with Commands — the gate, the limit, the
  // dialog's lifecycle, the delete confirmation and the queued, id-addressed
  // saves. What is left here is what a PROJECT is: its fields and its rows.
  const {
    items: projects, editable, notice, atLimit, pending, saveError,
    editing, openEditor, closeEditor, submit,
    deleting, askDelete, cancelDelete, confirmDelete, move,
  } = useHostConfigCollection<HostProject>(hostId, 'projects')
  const locked = !editable

  const iconBtn = 'p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed'

  return (
    <div className="max-w-3xl">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold">{t('hosts.projects')}</h2>
        <button type="button" data-testid="project-add" disabled={locked || pending || atLimit}
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
          busy={locked || pending} error={saveError?.target === 'dialog' ? saveError.text : null}
          onSave={submit} onCancel={closeEditor} />
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
                        disabled={locked || index === 0} onClick={() => void move(project.id, -1)}
                        className={iconBtn}><ArrowUp size={14} /></button>
                      <button type="button" data-testid={`project-down-${project.id}`} title={t('host_config.move_down')}
                        disabled={locked || index === projects.length - 1} onClick={() => void move(project.id, 1)}
                        className={iconBtn}><ArrowDown size={14} /></button>
                      <button type="button" data-testid={`project-edit-${project.id}`} title={t('common.edit')}
                        disabled={locked} onClick={() => openEditor(project)} className={iconBtn}><PencilSimple size={14} /></button>
                      {deleting === project.id ? (
                        <span className="flex items-center gap-1">
                          <button type="button" data-testid={`project-delete-confirm-${project.id}`} disabled={locked}
                            onClick={() => confirmDelete(project.id)}
                            className="p-1 text-red-400 cursor-pointer disabled:opacity-40"><Check size={14} /></button>
                          <button type="button" onClick={cancelDelete} className="p-1 text-text-muted cursor-pointer"><X size={14} /></button>
                        </span>
                      ) : (
                        <button type="button" data-testid={`project-delete-${project.id}`} title={t('common.delete')}
                          disabled={locked} onClick={() => askDelete(project.id)}
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
