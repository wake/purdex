// spa/src/components/settings/ModuleConfigSection.tsx
import { getModulesWithGlobalConfig, getModulesWithWorkspaceConfig } from '../../lib/module-registry'
import type { ConfigDef } from '../../lib/module-registry'
import { useModuleConfigStore } from '../../stores/useModuleConfigStore'
import { useWorkspaceStore } from '../../features/workspace/store'

interface Props {
  scope: 'global' | { workspaceId: string }
}

export function ModuleConfigSection({ scope }: Props) {
  const modules = scope === 'global' ? getModulesWithGlobalConfig() : getModulesWithWorkspaceConfig()

  if (modules.length === 0) return null

  return (
    <div className="space-y-6">
      {modules.map((mod) => {
        const configs = scope === 'global' ? mod.globalConfig! : mod.workspaceConfig!
        return (
          <div key={mod.id}>
            <h3 className="text-sm font-medium text-text-primary mb-2">{mod.name}</h3>
            <div className="space-y-2">
              {configs.map((def) => (
                <ConfigField key={def.key} def={def} moduleId={mod.id} scope={scope} />
              ))}
            </div>
          </div>
        )
      })}
    </div>
  )
}

function ConfigField({ def, moduleId, scope }: { def: ConfigDef; moduleId: string; scope: Props['scope'] }) {
  const globalValue = useModuleConfigStore((s) => s.globalConfig[moduleId]?.[def.key])
  const wsValue = useWorkspaceStore((s) => {
    if (scope === 'global') return undefined
    const ws = s.workspaces.find((w) => w.id === scope.workspaceId)
    return ws?.moduleConfig?.[moduleId]?.[def.key]
  })

  const value = scope === 'global' ? globalValue : wsValue
  const displayValue = value ?? def.defaultValue ?? ''

  const handleChange = (newValue: unknown) => {
    if (scope === 'global') {
      useModuleConfigStore.getState().setGlobalModuleConfig(moduleId, def.key, newValue)
    } else {
      useWorkspaceStore.getState().setModuleConfig((scope as { workspaceId: string }).workspaceId, moduleId, def.key, newValue)
    }
  }

  return (
    <div className="flex items-center justify-between py-1">
      <label className="text-xs text-text-secondary">{def.label}</label>
      {def.type === 'boolean' ? (
        <button
          className={`w-8 h-4 rounded-full transition-colors ${displayValue ? 'bg-accent-base' : 'bg-surface-hover'}`}
          onClick={() => handleChange(!displayValue)}
        >
          <div className={`w-3 h-3 rounded-full bg-white transition-transform ${displayValue ? 'translate-x-4' : 'translate-x-0.5'}`} />
        </button>
      ) : (
        <input
          className="w-48 px-2 py-0.5 rounded border border-border-default bg-surface-primary text-xs text-text-primary"
          type={def.type === 'number' ? 'number' : 'text'}
          value={String(displayValue)}
          onChange={(e) => handleChange(def.type === 'number' ? Number(e.target.value) : e.target.value)}
        />
      )}
    </div>
  )
}
