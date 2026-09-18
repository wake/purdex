# Host Badge Preview (settings page) — Implementation Plan

**Goal:** On the Host settings page, right of the Icon row, show a live preview of the host badge as it appears on a tab row — one **Normal** (inactive) row and one **Hover · Active** row — using the three colors of the mode currently selected in the Color row above.

**Spec addendum (host-color-modes spec §6.4):** the Color row's mode selection is shared with the Icon row; the preview renders the real `HostBadge` inside two mock sidebar rows carrying the same `.group` / `data-active` markup as `InlineTab`, so the global `index.css` rule drives the hover/active state exactly as in the app.

**Tech:** React 19 / Zustand 5 / Vitest + Testing Library / Tailwind 4 / TS strict. Tests `cd spa && npx vitest run <path>`; lint `pnpm run lint`; typecheck `npx tsc --noEmit -p tsconfig.app.json`.

## Global Constraints

- Worktree `/Users/wake/Workspace/wake/purdex/.claude/worktrees/host-badge-preview`, branch `worktree-host-badge-preview`; every command `cd`s there.
- TDD; commits via `git commit --only <files>`.
- No new store fields: the selected mode is React state lifted to `OverviewSection`.
- `HostColorField` keeps working uncontrolled (its existing tests render it alone).
- The preview must use the real `HostBadge` and the real sidebar badge settings (`hostBadgeSidebar{Enabled,LineColor,Box,Inset,Radius}`), never a copy of the badge markup.
- Locale keys in both `en.json` and `zh-TW.json`.

---

### Task 1: `HostBadgePreview` + wire `mode` through `OverviewSection` → `HostColorField` / `HostIconField`

**Files:**
- Create: `spa/src/components/hosts/HostBadgePreview.tsx`, `spa/src/components/hosts/HostBadgePreview.test.tsx`
- Modify: `spa/src/components/hosts/HostColorField.tsx` (optional controlled `mode` / `onModeChange`)
- Modify: `spa/src/components/hosts/HostIconField.tsx` (optional `mode`; renders the preview right of the buttons)
- Modify: `spa/src/components/hosts/OverviewSection.tsx` (`const [colorMode, setColorMode] = useState<HostColorMode>('console')`, passed to both)
- Modify: `spa/src/components/hosts/OverviewSection.test.tsx` (one test: switching mode in the Color row changes the preview)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Interfaces:**
```ts
// HostBadgePreview
export interface HostBadgePreviewProps { hostId: string; mode: HostColorMode }
// renders data-testid="host-badge-preview" containing two rows:
//   data-testid="host-badge-preview-normal"  (class "group", data-active="false")
//   data-testid="host-badge-preview-active"  (class "group", data-active="true")
// each row: TerminalWindow icon (14px) + <HostBadge testId="host-badge-preview-badge-normal|active" …/> + host name
// when hasHostBadge(...) is false → rows still render (icon + name) but no badge, and a muted caption hosts.color.preview.none
// HostColorField
export function HostColorField(props: { hostId: string; mode?: HostColorMode; onModeChange?: (m: HostColorMode) => void })
// HostIconField
export function HostIconField(props: { hostId: string; mode?: HostColorMode })   // mode absent → no preview
```

Locale keys:
```json
  "hosts.color.preview.label": "Preview",
  "hosts.color.preview.normal": "Normal",
  "hosts.color.preview.active": "Hover · Active",
  "hosts.color.preview.none": "No color or icon set",
```
zh-TW: 預覽 / 一般 / Hover · 作用中 / 尚未設定顏色或圖示.

- [ ] **Step 1: Write the failing tests**

`spa/src/components/hosts/HostBadgePreview.test.tsx`:

```tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { HostBadgePreview } from './HostBadgePreview'
import { useHostStore } from '../../stores/useHostStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'

const HOST_ID = 'h1'
const BLUE = { color: '#3b82f6', alpha: 100 }
const RED = { color: '#ef4444', alpha: 100 }

beforeEach(() => {
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: {},
  })
  useUISettingsStore.setState({ hostBadgeSidebarEnabled: true, hostBadgeSidebarLineColor: 'host', hostBadgeSidebarBox: 16, hostBadgeSidebarInset: 2, hostBadgeSidebarRadius: 4 })
})

describe('HostBadgePreview', () => {
  it('renders a normal and an active mock row with the host name and the real badge', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    render(<HostBadgePreview hostId={HOST_ID} mode="console" />)
    const normal = screen.getByTestId('host-badge-preview-normal')
    const active = screen.getByTestId('host-badge-preview-active')
    expect(normal).toHaveAttribute('data-active', 'false')
    expect(active).toHaveAttribute('data-active', 'true')
    expect(normal.className).toContain('group')
    expect(active.className).toContain('group')
    expect(normal.textContent).toContain('mlab')
    const badge = screen.getByTestId('host-badge-preview-badge-normal')
    expect(badge).toHaveAttribute('data-host-badge')
    expect(badge.style.getPropertyValue('--hb-main')).toBe('rgba(59, 130, 246, 1)')
    expect(badge.style.getPropertyValue('--hb-middle')).toBe('rgba(59, 130, 246, 0.6)')
    expect(badge.style.background).toBe('rgba(59, 130, 246, 0.22)')
  })

  it('follows the selected mode', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    useHostStore.getState().setHostColorLayer(HOST_ID, 'terminal', 'main', RED)
    render(<HostBadgePreview hostId={HOST_ID} mode="terminal" />)
    expect(screen.getByTestId('host-badge-preview-badge-active').style.getPropertyValue('--hb-main')).toBe('rgba(239, 68, 68, 1)')
  })

  it('uses the sidebar badge geometry and line color settings', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    useUISettingsStore.setState({ hostBadgeSidebarBox: 20, hostBadgeSidebarRadius: 6, hostBadgeSidebarLineColor: 'neutral' })
    render(<HostBadgePreview hostId={HOST_ID} mode="console" />)
    const badge = screen.getByTestId('host-badge-preview-badge-normal')
    expect(badge.style.width).toBe('20px')
    expect(badge.style.borderRadius).toBe('6px')
    expect(badge.style.color).toBe('var(--text-muted)')
  })

  it('shows the rows without a badge plus a caption when the host has neither color nor icon', () => {
    render(<HostBadgePreview hostId={HOST_ID} mode="console" />)
    expect(screen.getByTestId('host-badge-preview-normal')).toBeInTheDocument()
    expect(screen.queryByTestId('host-badge-preview-badge-normal')).toBeNull()
    expect(screen.getByText('No color or icon set')).toBeInTheDocument()
  })

  it('renders the host icon in the badge', () => {
    useHostStore.getState().setHostIcon(HOST_ID, 'Laptop', 'duotone')
    render(<HostBadgePreview hostId={HOST_ID} mode="console" />)
    expect(screen.getByTestId('host-badge-preview-badge-normal').querySelector('svg')).not.toBeNull()
  })
})
```

`spa/src/components/hosts/OverviewSection.test.tsx` — add (reuse that file's render helper and host seeding; read it first):

```tsx
  it('switching the Color row mode drives the badge preview next to the icon', () => {
    useHostStore.getState().setHostColorLayer(hostId, 'console', 'main', { color: '#3b82f6', alpha: 100 })
    useHostStore.getState().setHostColorLayer(hostId, 'terminal', 'main', { color: '#ef4444', alpha: 100 })
    renderOverview()
    expect(screen.getByTestId('host-badge-preview-badge-active').style.getPropertyValue('--hb-main')).toBe('rgba(59, 130, 246, 1)')
    fireEvent.click(screen.getByRole('button', { name: 'Terminal' }))
    expect(screen.getByTestId('host-badge-preview-badge-active').style.getPropertyValue('--hb-main')).toBe('rgba(239, 68, 68, 1)')
  })
```

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/components/hosts/HostBadgePreview.test.tsx src/components/hosts/OverviewSection.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

`HostBadgePreview.tsx`:

```tsx
import { TerminalWindow } from '@phosphor-icons/react'
import { useMemo } from 'react'
import { useHostStore } from '../../stores/useHostStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { HostBadge } from '../HostBadge'
import { hasHostBadge, isIconWeight, isPhosphorIconName, resolveHostColors, type HostColorMode } from '../../lib/host-color'

export interface HostBadgePreviewProps {
  hostId: string
  mode: HostColorMode
}

/**
 * Two mock sidebar rows showing the host badge for `mode` — one inactive, one
 * hovered/active. Same `.group` + `data-active` markup as `InlineTab`, so the
 * global rule in `index.css` switches `--hb-icon` exactly as in the real rows.
 */
export function HostBadgePreview({ hostId, mode }: HostBadgePreviewProps) {
  const t = useI18nStore((s) => s.t)
  const name = useHostStore((s) => s.hosts[hostId]?.name ?? '')
  const colors = useHostStore((s) => s.hosts[hostId]?.colors)
  const legacy = useHostStore((s) => s.hosts[hostId]?.color)
  const rawIcon = useHostStore((s) => s.hosts[hostId]?.icon)
  const rawWeight = useHostStore((s) => s.hosts[hostId]?.iconWeight)
  const enabled = useUISettingsStore((s) => s.hostBadgeSidebarEnabled)
  const lineColor = useUISettingsStore((s) => s.hostBadgeSidebarLineColor)
  const box = useUISettingsStore((s) => s.hostBadgeSidebarBox)
  const inset = useUISettingsStore((s) => s.hostBadgeSidebarInset)
  const radius = useUISettingsStore((s) => s.hostBadgeSidebarRadius)

  const resolved = useMemo(() => resolveHostColors({ colors, color: legacy }, mode), [colors, legacy, mode])
  const badge = { colors: resolved, icon: isPhosphorIconName(rawIcon) ? rawIcon : undefined, iconWeight: isIconWeight(rawWeight) ? rawWeight : undefined }
  const show = enabled && hasHostBadge(badge)

  const rows: { key: 'normal' | 'active'; active: boolean; label: string; cls: string }[] = [
    { key: 'normal', active: false, label: t('hosts.color.preview.normal'), cls: 'text-text-muted bg-surface-secondary' },
    { key: 'active', active: true, label: t('hosts.color.preview.active'), cls: 'text-white bg-surface-active' },
  ]

  return (
    <div data-testid="host-badge-preview" className="flex flex-col gap-1.5 min-w-[180px]">
      <span className="text-[11px] text-text-muted">{t('hosts.color.preview.label')}</span>
      {rows.map((r) => (
        <div key={r.key} className="flex items-center gap-2">
          <div
            data-testid={`host-badge-preview-${r.key}`}
            data-active={String(r.active)}
            className={`group flex items-center gap-1.5 pl-2 pr-2 py-1 rounded-md text-xs w-40 ${r.cls}`}
          >
            <TerminalWindow size={14} className="flex-shrink-0" />
            {show && (
              <HostBadge
                testId={`host-badge-preview-badge-${r.key}`}
                colors={badge.colors}
                icon={badge.icon}
                iconWeight={badge.iconWeight}
                box={box}
                inset={inset}
                radius={radius}
                lineColor={lineColor}
              />
            )}
            <span className="truncate">{name}</span>
          </div>
          <span className="text-[11px] text-text-muted">{r.label}</span>
        </div>
      ))}
      {!show && <span className="text-[11px] text-text-muted">{t('hosts.color.preview.none')}</span>}
    </div>
  )
}
```

`HostColorField.tsx`: signature `({ hostId, mode: controlledMode, onModeChange }: { hostId: string; mode?: HostColorMode; onModeChange?: (m: HostColorMode) => void })`; keep `const [innerMode, setInnerMode] = useState<HostColorMode>('console')`; `const mode = controlledMode ?? innerMode`; the SegmentControl `onChange` calls `setInnerMode(m); onModeChange?.(m); setOpen(null)`.

`HostIconField.tsx`: add `mode?: HostColorMode` prop; wrap the existing button row and the preview in `<div className="flex items-start gap-6">` — left column = the current `space-y-2` block (buttons + expanded picker), right column = `{mode && <HostBadgePreview hostId={hostId} mode={mode} />}`.

`OverviewSection.tsx`: `const [colorMode, setColorMode] = useState<HostColorMode>('console')`; `<HostColorField hostId={hostId} mode={colorMode} onModeChange={setColorMode} />`, `<HostIconField hostId={hostId} mode={colorMode} />`.

- [ ] **Step 4: Run to verify pass + full suite**

Run: `cd spa && npx vitest run src/components/hosts && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint && npx vitest run`
Expected: all green.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/components/hosts/HostBadgePreview.tsx spa/src/components/hosts/HostBadgePreview.test.tsx spa/src/components/hosts/HostColorField.tsx spa/src/components/hosts/HostIconField.tsx spa/src/components/hosts/OverviewSection.tsx spa/src/components/hosts/OverviewSection.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): live host badge preview (normal / hover) next to the host icon, following the selected color mode"
```
