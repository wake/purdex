import { useMemo } from 'react'
import { useQuickCommandStore } from '../stores/useQuickCommandStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { getModulesWithCommands, type CommandContext } from '../lib/module-registry'

export interface ResolvedCommand {
  id: string
  name: string
  command: string
  icon?: string
  category?: string
  source: string // 'store' or module id
}

export function useCommands(filter: { hostId: string; workspaceId?: string | null }): ResolvedCommand[] {
  const storeCmds = useQuickCommandStore((s) => s.getCommands(filter.hostId))
  const workspaces = useWorkspaceStore((s) => s.workspaces)

  return useMemo(() => {
    // 1. Store commands
    const resolved: ResolvedCommand[] = storeCmds.map((c) => ({
      id: c.id,
      name: c.name,
      command: c.command,
      icon: c.icon,
      category: c.category,
      source: 'store',
    }))

    // 2. Module contributions
    const ws = filter.workspaceId
      ? workspaces.find((w) => w.id === filter.workspaceId)
      : undefined

    const modulesWithCmds = getModulesWithCommands()
    for (const mod of modulesWithCmds) {
      if (!mod.commands) continue
      const ctx: CommandContext = {
        hostId: filter.hostId,
        workspaceId: filter.workspaceId,
        moduleConfig: ws?.moduleConfig?.[mod.id],
      }
      for (const contrib of mod.commands) {
        const command = typeof contrib.command === 'function'
          ? contrib.command(ctx)
          : contrib.command
        resolved.push({
          id: contrib.id,
          name: contrib.name,
          command,
          icon: contrib.icon,
          category: contrib.category,
          source: mod.id,
        })
      }
    }

    return resolved
  }, [storeCmds, workspaces, filter.hostId, filter.workspaceId])
}
