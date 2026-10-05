// spa/src/components/room/prelude/PreludeMarker.tsx — a thin labelled rule
// in the prelude (spec D2): where the transcript switched between the
// terminal and headless turns, or was compacted.
export default function PreludeMarker({ label, testId }: { label: string; testId: string }) {
  return (
    <div data-testid={testId} role="separator" aria-label={label}
      className="flex items-center gap-2 text-[11px] text-text-muted select-none">
      <span className="flex-1 border-t border-border-subtle" />
      <span>{label}</span>
      <span className="flex-1 border-t border-border-subtle" />
    </div>
  )
}
