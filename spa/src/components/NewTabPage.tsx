import { useMemo, useState, useEffect } from 'react'
import { getNewTabProviders } from '../lib/new-tab-registry'
import { useI18nStore } from '../stores/useI18nStore'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { useSessionStore } from '../stores/useSessionStore'
import { useBreakpoint } from '../hooks/useBreakpoint'
import { resolveProfile } from '../lib/resolve-profile'
import { colsClass } from '../lib/cols-class'
import { countLeaves, getPrimaryPane } from '../lib/pane-tree'
import { getPaneLabel } from '../lib/pane-labels'
import { moveTabContentIntoPane, MOVABLE_KINDS } from '../lib/pane-move'
import type { PaneContent } from '../types/tab'

interface Props {
  onSelect: (content: PaneContent) => void
  /**
   * When this new-tab pane is mounted inside a real tab (the normal case), the
   * pane renderer passes the owning tab + pane ids. Their presence — AND the
   * owning tab being split (more than one pane) — unlocks the "Bring in an open
   * tab" section, which pulls another single-pane tab's content into THIS pane.
   * A full-page new tab is a single pane, not a split target, so the section
   * stays hidden there. Omitted on unwired call paths (the section then simply
   * does not render).
   */
  currentTabId?: string
  currentPaneId?: string
}

export function NewTabPage({ onSelect, currentTabId, currentPaneId }: Props) {
  const t = useI18nStore((s) => s.t)
  const [hydrated, setHydrated] = useState(useNewTabLayoutStore.persist.hasHydrated())
  useEffect(() => {
    if (hydrated) return
    return useNewTabLayoutStore.persist.onFinishHydration(() => setHydrated(true))
  }, [hydrated])

  const { isWide, isMid } = useBreakpoint()
  const profiles = useNewTabLayoutStore((s) => s.profiles)
  const profileKey = resolveProfile(isWide, isMid, profiles)
  const profile = profiles[profileKey]

  // P1 reload-required contract (matches PaneLayoutRenderer + file-opener
  // registry): module enable/disable does NOT flip the New Tab UI live —
  // a fresh registerBuiltinModules() bootstrap is the canonical way to make
  // the change visible. Reading isEnabled via getState() avoids a subscription
  // that would otherwise hide editor entries the moment the user toggles
  // Editor in the Switchboard while the actual openers / terminal-link
  // bindings still wait for the next bootstrap. Going fully-immediate is
  // tracked in issue #678.
  const providers = useMemo(() => {
    // A2-4 / A2-5: providers carrying a `moduleId` are hidden when the owning
    // module is disabled. Legacy providers with no `moduleId` are always
    // visible (back-compat, spec §4.9.3).
    const isEnabled = useModuleEnabledStore.getState().isEnabled
    return getNewTabProviders().filter((p) => !p.moduleId || isEnabled(p.moduleId))
    // Empty deps: providers / module enable state are deliberately captured
    // at render-creation time only. Subsequent toggles wait for re-bootstrap.
  }, [])
  const byId = useMemo(() => Object.fromEntries(providers.map((p) => [p.id, p])), [providers])

  // "Bring in an open tab" — enumerate every OTHER open tab (across all
  // workspaces) whose content can be relocated into this pane. Subscribing to
  // `tabs` / `workspaces` / `sessions` (not empty-deps snapshots) keeps the
  // list live as tabs open, close, rename, or move.
  const tabs = useTabStore((s) => s.tabs)
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const sessions = useSessionStore((s) => s.sessions)
  const bringInCandidates = useMemo(() => {
    const currentTab = currentTabId ? tabs[currentTabId] : undefined
    // Gate on the owning tab being a split (>1 pane). A full-page new tab is a
    // single pane — not a pane-split target — so the section must stay hidden
    // there; it only belongs when this new-tab pane is one cell of a split.
    if (!currentTab || !currentPaneId || countLeaves(currentTab.layout) <= 1) return []
    const workspaceLookup = { getById: (id: string) => workspaces.find((w) => w.id === id) }
    const movable = MOVABLE_KINDS as readonly string[]
    const out: { tabId: string; label: string; workspaceName: string }[] = []
    for (const ws of workspaces) {
      for (const tabId of ws.tabs) {
        if (tabId === currentTabId) continue
        const tab = tabs[tabId]
        if (!tab || tab.locked) continue
        if (countLeaves(tab.layout) !== 1) continue
        const content = getPrimaryPane(tab.layout).content
        if (!movable.includes(content.kind)) continue
        // Scope the session lookup to THIS content's own host — session codes
        // are only unique per host, so a flat cross-host code→session map would
        // mislabel one tab with another host's session name.
        const sessionLookup = {
          getByCode: (code: string) =>
            content.kind === 'tmux-session'
              ? (sessions[content.hostId] ?? []).find((sess) => sess.code === code)
              : undefined,
        }
        out.push({
          tabId,
          label: getPaneLabel(content, sessionLookup, workspaceLookup, t),
          workspaceName: ws.name,
        })
      }
    }
    return out
  }, [currentTabId, currentPaneId, tabs, workspaces, sessions, t])

  if (!hydrated) {
    return <div className="h-full w-full" />
  }

  if (providers.length === 0) {
    return (
      <div className="h-full w-full flex items-center justify-center" data-testid="newtab-empty-state">
        <p className="text-sm text-text-secondary">{t('page.newtab.empty')}</p>
      </div>
    )
  }

  // v1.4 §4.9.7 (F8): a user's pinned profile may reference only
  // providers that are currently filtered out (e.g. pinned `editor` +
  // `editor-buffers`, then disabled the Editor module). In that case
  // every column resolves to an empty list — the old code rendered
  // nothing but the grid, leaving the tab visually blank with no cue.
  // Fall back to the existing empty state when NO column has anything
  // visible; keep the grid otherwise.
  const hasAnyVisibleEntry = profile.columns.some((col) => col.some((id) => byId[id]))
  if (!hasAnyVisibleEntry) {
    return (
      <div className="h-full w-full flex items-center justify-center" data-testid="newtab-empty-state">
        <p className="text-sm text-text-secondary">{t('page.newtab.empty')}</p>
      </div>
    )
  }

  const gridCols = colsClass(profile.columns.length)

  const grid = (
    <div className={`h-full w-full grid overflow-hidden gap-6 px-6 pt-8 ${gridCols}`}>
      {profile.columns.map((col, i) => (
        <div key={`${profileKey}-${i}`} className="flex flex-col gap-6 overflow-y-auto">
          {col.map((id) => {
            const p = byId[id]
            if (!p) return null
            return (
              <section key={id} className="w-full">
                <h3 className="text-sm font-medium text-text-secondary mb-2 px-2">
                  {t(p.label)}
                  {p.disabled && p.disabledReason && (
                    <span className="text-text-muted text-xs ml-2">— {t(p.disabledReason)}</span>
                  )}
                </h3>
                {!p.disabled && <p.component onSelect={onSelect} />}
              </section>
            )
          })}
        </div>
      ))}
    </div>
  )

  // No pane context, or nothing movable → render the bare grid unchanged so the
  // start-screen layout (and its h-full scroll contract) is untouched.
  if (bringInCandidates.length === 0) return grid

  return (
    <div className="h-full w-full flex flex-col overflow-hidden">
      {/* Cap the section at half the pane so a long candidate list can never
          push the provider grid below the fold — the overflow scrolls inside
          the list instead. min-h-0 lets the flex child shrink; flex-shrink-0
          keeps a SHORT list at its natural height. */}
      <section
        className="w-full flex-shrink-0 flex flex-col min-h-0 max-h-[50%] px-6 pt-6"
        data-testid="newtab-bring-in"
      >
        <h3 className="flex-shrink-0 text-sm font-medium text-text-secondary mb-2 px-2">
          {t('page.newtab.bringInTab')}
        </h3>
        <div className="flex flex-col gap-1 min-h-0 overflow-y-auto" data-testid="newtab-bring-in-list">
          {bringInCandidates.map((c) => (
            <button
              key={c.tabId}
              type="button"
              onClick={() => moveTabContentIntoPane(c.tabId, currentTabId!, currentPaneId!)}
              className="flex items-center justify-between gap-3 rounded-md px-2 py-1.5 text-left text-sm text-text-primary hover:bg-white/5 cursor-pointer"
            >
              <span className="truncate">{c.label}</span>
              <span className="flex-shrink-0 text-xs text-text-muted">{c.workspaceName}</span>
            </button>
          ))}
        </div>
      </section>
      <div className="flex-1 min-h-0">{grid}</div>
    </div>
  )
}
