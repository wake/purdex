import { Plus, TerminalWindow, ChatCircleDots, House, ClockCounterClockwise, GearSix, SmileySad, Globe } from '@phosphor-icons/react'

const TerminalWindowFill = (props: { size: number; className?: string }) => <TerminalWindow {...props} weight="fill" />

export const ICON_MAP: Record<string, React.ComponentType<{ size: number; className?: string }>> = {
  Plus,
  TerminalWindow: TerminalWindowFill,
  ChatCircleDots,
  House,
  ClockCounterClockwise,
  GearSix,
  SmileySad,
  Globe,
}
