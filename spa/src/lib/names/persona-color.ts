// spa/src/lib/names/persona-color.ts — the colour the initials sit on when a persona has no stored image (`<Avatar>`, everywhere it is drawn).
// Derived from the code, so the same persona always gets the same one.
export function personaColor(code: string): string {
  let h = 0
  for (const ch of code) h = (h * 31 + ch.charCodeAt(0)) >>> 0
  return `hsl(${h % 360} 40% 38%)`
}
