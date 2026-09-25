// spa/src/components/StreamCursor.tsx — the blinking typewriter cursor that
// room/RoomProse and room/RoomThinking append while a partial block streams
// (P-B2 spec §4.4 R1). One definition so the markup can't drift between them.
export default function StreamCursor() {
  return <span data-testid="stream-cursor" className="stream-cursor" aria-hidden="true">▌</span>
}
