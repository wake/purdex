import { useId } from 'react'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useI18nStore } from '../../stores/useI18nStore'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'

interface Props {
  ctx: SettingsContextFor<'workspace'>
}

export function FilesWorkspaceSettingsSection({ ctx }: Props) {
  if (ctx.scope !== 'workspace') return null
  return <Body workspaceId={ctx.workspaceId} />
}

function Body({ workspaceId }: { workspaceId: string }) {
  const t = useI18nStore((s) => s.t)
  const projectPath = useWorkspaceStore(
    (s) => s.workspaces.find((w) => w.id === workspaceId)?.moduleConfig?.['files']?.['projectPath'],
  )
  const value = typeof projectPath === 'string' ? projectPath : ''
  const inputId = useId()

  const handleChange = (next: string) => {
    useWorkspaceStore.getState().setModuleConfig(workspaceId, 'files', 'projectPath', next)
  }

  return (
    <div className="flex items-center justify-between py-1">
      <label htmlFor={inputId} className="text-xs text-text-secondary">
        {t('settings.files.project_path.label')}
      </label>
      <input
        id={inputId}
        className="w-48 px-2 py-0.5 rounded border border-border-default bg-surface-primary text-xs text-text-primary"
        type="text"
        value={value}
        onChange={(e) => handleChange(e.target.value)}
      />
    </div>
  )
}
