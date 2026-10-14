// spa/src/lib/fleet/probe-draft-memory.ts — the open 「只探查的主機」 dialog and its unsaved draft, kept outside the
// component (repo CLAUDE.md, Tab-hosted checklist). The Hosts page is a tab and `hosts` is not a light pane kind, so under
// the default keepAliveCount 0 a tab switch unmounts the section — and a Hosts sub-page switch does too; state in the
// component would start over. Memory only: a reload starts from the stored list. One draft per prober host (the dialog is
// inline on that host's page); the dialog is open exactly while a draft exists.
//
// The draft holds what the dialog edits — name, SSH host, icon, colours — plus the colour mode being edited and the last
// save failure, so a switch away and back shows the same dialog, the same text and the same message.
import { create } from 'zustand'
import { withColorLayer, withIcon } from '../../stores/useHostStore'
import type { HostColorLayer, HostColorLayerName, HostColorMode } from '../host-color'
import type { IconWeight } from '../../types/tab'
import type { ProbeHostDef } from './types'

export interface ProbeDraft {
  /** The row being edited; '' = a new one (the daemon makes its id). */
  probeId: string
  name: string
  ssh_alias: string
  icon?: string
  icon_weight?: IconWeight
  colors?: ProbeHostDef['colors']
  /** The colour mode the colour editor shows. */
  mode: HostColorMode
  /** The last save failure (the daemon's plain text, or a sentence about a 409), shown in the dialog. */
  error: string | null
}

export const newProbeDraft = (): ProbeDraft => ({ probeId: '', name: '', ssh_alias: '', mode: 'console', error: null })

export function draftOf(def: ProbeHostDef): ProbeDraft {
  const d: ProbeDraft = { probeId: def.id, name: def.name, ssh_alias: def.ssh_alias, mode: 'console', error: null }
  if (def.icon) d.icon = def.icon
  if (def.icon_weight) d.icon_weight = def.icon_weight
  if (def.colors) d.colors = def.colors
  return d
}

/** The draft as the row to save. A new row keeps id '' (the daemon assigns one). */
export function defOfDraft(d: ProbeDraft): ProbeHostDef {
  const def: ProbeHostDef = { id: d.probeId, name: d.name.trim(), ssh_alias: d.ssh_alias }
  if (d.icon) def.icon = d.icon
  if (d.icon_weight) def.icon_weight = d.icon_weight
  if (d.colors) def.colors = d.colors
  return def
}

/** The icon editor's rules (`withIcon`, shared with the App's host look) over a draft. */
export function draftIcon(d: ProbeDraft, icon: string | null, weight?: IconWeight): ProbeDraft {
  const look = withIcon({ icon: d.icon, iconWeight: d.icon_weight }, icon, weight)
  if (!look) return d
  const { icon: _i, icon_weight: _w, ...rest } = d
  return { ...rest, ...(look.icon ? { icon: look.icon } : {}), ...(look.iconWeight ? { icon_weight: look.iconWeight } : {}) }
}

/** The colour editor's rules (`withColorLayer`) over a draft; `null` clears the mode (`clearHostColorMode`). */
export function draftColor(d: ProbeDraft, mode: HostColorMode, layer: HostColorLayerName, value: HostColorLayer | null): ProbeDraft {
  const look = withColorLayer({ colors: d.colors }, mode, layer, value)
  if (!look) return d
  const { colors: _c, ...rest } = d
  return look.colors ? { ...rest, colors: look.colors } : rest
}

interface DraftState {
  byHost: Record<string, ProbeDraft>
  open: (hostId: string, draft: ProbeDraft) => void
  patch: (hostId: string, fn: (d: ProbeDraft) => ProbeDraft) => void
  close: (hostId: string) => void
  reset: () => void
}

export const useProbeDraftStore = create<DraftState>()((set) => ({
  byHost: {},
  open: (hostId, draft) => set((s) => ({ byHost: { ...s.byHost, [hostId]: draft } })),
  patch: (hostId, fn) => set((s) => {
    const cur = s.byHost[hostId]
    return cur ? { byHost: { ...s.byHost, [hostId]: fn(cur) } } : s
  }),
  close: (hostId) => set((s) => {
    const { [hostId]: _gone, ...rest } = s.byHost
    return { byHost: rest }
  }),
  reset: () => set({ byHost: {} }),
}))

/** A host was removed or re-pointed: the draft's SSH host was picked from the old daemon's list. */
export const forgetProbeDraft = (hostId: string): void => useProbeDraftStore.getState().close(hostId)
