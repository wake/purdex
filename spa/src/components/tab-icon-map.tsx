import { Plus, TerminalWindow, ChatCircleDots, House, ClockCounterClockwise, GearSix, SmileySad, Globe } from '@phosphor-icons/react'

// Filled TerminalWindow variant — inlined to avoid a named component export in
// this otherwise-constant module (react-refresh/only-export-components).
export const ICON_MAP: Record<string, React.ComponentType<{ size: number; className?: string }>> = {
  Plus,
  TerminalWindow: (props) => <TerminalWindow {...props} weight="fill" />,
  ChatCircleDots,
  House,
  ClockCounterClockwise,
  GearSix,
  SmileySad,
  Globe,
}
