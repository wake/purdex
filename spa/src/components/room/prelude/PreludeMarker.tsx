// spa/src/components/room/prelude/PreludeMarker.tsx — a thin labelled rule
// in the prelude (spec D2): where the transcript switched between the
// terminal and headless turns, or was compacted. `pos` is its entry's
// (scroll anchor, #1534); the closing handoff marker has none.
export default function PreludeMarker({ label, testId, pos }: { label: string; testId: string; pos?: string }) {
  return (
    <div data-testid={testId} data-prelude-pos={pos} role="separator" aria-label={label}
      className="flex items-center gap-2 text-[11px] text-text-muted select-none">
      <span className="flex-1 border-t border-border-subtle" />
      <span>{label}</span>
      <span className="flex-1 border-t border-border-subtle" />
    </div>
  )
}
