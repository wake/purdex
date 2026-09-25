// spa/src/components/room/RoomSubagentLine.tsx — the prompt an agent wrote for
// its subagent (#1263). It arrives as a `user` frame, but the human did not
// say it, so it must not look like RoomUserLine: no gutter mark, no stronger
// text — a secondary-toned line led by a robot glyph, read as the agent
// speaking to its child.
import { Robot } from '@phosphor-icons/react'

export default function RoomSubagentLine({ text }: { text: string }) {
  return (
    <div data-testid="room-subagent-line" className="flex items-baseline gap-1.5 text-sm text-text-secondary">
      <Robot size={12} aria-hidden="true" className="shrink-0 translate-y-0.5" />
      <p className="whitespace-pre-wrap break-words">{text}</p>
    </div>
  )
}
