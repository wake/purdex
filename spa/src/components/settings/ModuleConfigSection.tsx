// spa/src/components/settings/ModuleConfigSection.tsx
import { useEffect, useId, useRef, useState } from 'react'
import { getModulesWithGlobalConfig, getModulesWithWorkspaceConfig } from '../../lib/module-registry'
import type { ConfigDef } from '../../lib/module-registry'
import { useModuleConfigStore } from '../../stores/useModuleConfigStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useI18nStore } from '../../stores/useI18nStore'
import { useWorkspaceSettingsDraft } from '../../features/workspace/components/WorkspaceSettingsDraftContext'
import { ToggleSwitch } from './ToggleSwitch'

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
  const t = useI18nStore((s) => s.t)
  const draftContext = useWorkspaceSettingsDraft()
  const id = useId()
  const fieldId = id + '-' + def.key
  const draftFieldId = scope === 'global'
    ? `global:${moduleId}:${def.key}`
    : `workspace:${scope.workspaceId}:${moduleId}:${def.key}`

  const globalValue = useModuleConfigStore((s) => s.globalConfig[moduleId]?.[def.key])
  const wsValue = useWorkspaceStore((s) => {
    if (scope === 'global') return undefined
    const ws = s.workspaces.find((w) => w.id === scope.workspaceId)
    return ws?.moduleConfig?.[moduleId]?.[def.key]
  })

  const value = scope === 'global' ? globalValue : wsValue
  const displayValue = value ?? def.defaultValue ?? ''
  const displayText = String(displayValue)
  const [draft, setDraft] = useState(displayText)
  const draftRef = useRef(draft)
  const displayTextRef = useRef(displayText)

  useEffect(() => {
    setDraft(displayText)
  }, [displayText])

  useEffect(() => {
    draftRef.current = draft
    displayTextRef.current = displayText
    draftContext?.setDirty(draftFieldId, draft !== displayText)
  }, [draft, displayText, draftContext, draftFieldId])

  const handleChange = (newValue: unknown) => {
    if (scope === 'global') {
      useModuleConfigStore.getState().setGlobalModuleConfig(moduleId, def.key, newValue)
    } else {
      useWorkspaceStore.getState().setModuleConfig((scope as { workspaceId: string }).workspaceId, moduleId, def.key, newValue)
    }
  }

  const saveDraft = () => {
    const currentDraft = draftRef.current
    handleChange(def.type === 'number' ? Number(currentDraft) : currentDraft)
    draftContext?.setDirty(draftFieldId, false)
  }

  const cancelDraft = () => {
    setDraft(displayTextRef.current)
    draftContext?.setDirty(draftFieldId, false)
  }

  useEffect(() => {
    return draftContext?.register(draftFieldId, {
      save: saveDraft,
      cancel: cancelDraft,
    })
  // The registered functions read refs so they do not need to be rebound on every draft update.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draftContext, draftFieldId])

  return (
    <div className="grid grid-cols-1 md:grid-cols-[minmax(0,1fr)_minmax(280px,420px)] gap-4 md:gap-8 py-6 items-start border-y border-border-subtle -mt-px">
      {def.type === 'boolean' ? (
        <>
          <span className="text-sm font-semibold text-text-primary">{def.label}</span>
          <ToggleSwitch label={def.label} checked={!!displayValue} onChange={(v) => handleChange(v)} />
        </>
      ) : (
        <>
          <label htmlFor={fieldId} className="text-sm font-semibold text-text-primary">{def.label}</label>
          <div className="flex items-center gap-2">
            <input
              id={fieldId}
              className="w-full px-3 py-2 rounded-md border border-border-default bg-surface-primary text-sm text-text-primary focus:border-accent focus:outline-none"
              type={def.type === 'number' ? 'number' : 'text'}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter' && !e.nativeEvent.isComposing) saveDraft() }}
            />
            {!draftContext && (
              <>
                <button
                  type="button"
                  onClick={saveDraft}
                  disabled={draft === displayText}
                  className="px-2 py-0.5 rounded bg-accent text-white text-xs font-medium hover:bg-accent/90 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                >
                  {t('common.save') ?? 'Save'}
                </button>
                <button
                  type="button"
                  onClick={cancelDraft}
                  disabled={draft === displayText}
                  className="px-2 py-0.5 rounded border border-border-subtle text-xs text-text-secondary hover:bg-surface-muted disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                >
                  {t('common.cancel') ?? 'Cancel'}
                </button>
              </>
            )}
          </div>
        </>
      )}
    </div>
  )
}
