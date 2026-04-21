# Tab Icon Baseline — 2026-04-17

Snapshot of the tab icon / status / subagent indicator setup I've tuned with
Wake. Capture point before trying an alternative visual. Restore by diffing
each numeric value below against the target file.

## Files and exact values

### `spa/src/components/SortableTab.tsx`

```tsx
// renderTabIcon() call sites — icon size 14 (both pinned + normal branches)
{renderTabIcon(IconComponent, agentStatus, tabIndicatorStyle, isActive, 14, subagentCount)}
```

Replace-mode branch — when `IconComponent` exists, show icon + overlay dot +
subagent dots instead of the lone-dot replacement:

```tsx
if (tabIndicatorStyle === 'replace' && agentStatus) {
  if (IconComponent) {
    return (
      <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
        <IconComponent size={iconSize} className="flex-shrink-0" />
        <TabStatusDot status={agentStatus} style="overlay" isActive={isActive} />
        {subagentCount > 0 && <SubagentDots count={subagentCount} isActive={isActive} />}
      </span>
    )
  }
  return (
    <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
      <TabStatusDot status={agentStatus} style="replace" isActive={isActive} />
      {subagentCount > 0 && <SubagentDots count={subagentCount} isActive={isActive} />}
    </span>
  )
}
```

### `spa/src/components/TabStatusDot.tsx`

- Overlay dot: `width: 6px`, `height: 6px`, `top: 0`, `right: 0`
- Ring (all three styles): `boxShadow: 0 0 0 1px ${breatheBg}`

### `spa/src/index.css`

```css
@keyframes breathe {
  0%, 100% { opacity: 1; }
  50%       { opacity: 0; }
}

@utility animate-breathe {
  animation: breathe 2s ease-in-out infinite;
}
```

(Originally animated `background-color` between dot colour and tab bg, which
caused a visible block over icons. Opacity fade sits cleanly over any
background.)

### `spa/src/components/SubagentDots.tsx`

```ts
const ARC_POSITIONS: Record<number, [number, number][]> = {
  1: [[-11, 0]],
  2: [[-11, -4], [-11, 4]],
  3: [[-11, -5.5], [-11, 0], [-11, 5.5]],
}
```

`DOT_SIZES` and colour `#60a5fa` unchanged from earlier baseline.

### `spa/src/lib/agent-icons.tsx`

```tsx
import { OpenAiLogo } from '@phosphor-icons/react'
import ClaudeCodeSvg from '@lobehub/icons-static-svg/icons/claude.svg?react'

export const AGENT_ICONS: Record<string, AgentIconComponent> = {
  cc: wrap(ClaudeCodeSvg),              // lobe-icons Claude star
  codex: OpenAiLogo as unknown as AgentIconComponent, // Phosphor OpenAI
}
```

## Dev hacks (must be removed before commit)

### `spa/src/components/TabBar.tsx`

Normal-tab map passes `devForceAgent` + `devForceSubagents` to the first two
tabs so the layout is visible without real hook events:

```tsx
devForceAgent={i === 0 ? 'cc' : i === 1 ? 'codex' : undefined}
devForceSubagents={i === 0 ? 2 : i === 1 ? 3 : undefined}
```

### `spa/src/components/SortableTab.tsx`

- `Props` gains `devForceAgent?: string` and `devForceSubagents?: number`
- Destructure both in the function signature
- `agentTypeFromStore` and `agentStatusFromStore` renames; final values
  resolve via `devForceAgent ?? agentTypeFromStore` and the status is forced
  to `'running'` when `devForceAgent` is set
- `subagentCount = devForceSubagents ?? subagentCountFromStore`

## Restoration checklist when trying another layout

1. Keep the non-hack edits above — they are the actual visual baseline.
2. Keep the dev hack props until a real hook-driven session is available for
   verification; strip them right before pushing.
3. When exploring a new layout, branch from this snapshot, not from `main` —
   the official-logo + opacity-breathe work is not on `main` yet (PR #414
   still open at time of writing).
